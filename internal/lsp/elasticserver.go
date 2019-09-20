package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/types"
	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/vcs"
	"golang.org/x/tools/internal/jsonrpc2"
	"golang.org/x/tools/internal/lsp/protocol"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/telemetry"
	"golang.org/x/tools/internal/semver"
	"golang.org/x/tools/internal/span"
	"golang.org/x/tools/internal/telemetry/log"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	pkgMod = filepath.Join(os.Getenv("GOPATH"), "pkg", "mod")
	goRoot = os.Getenv("GOROOT")
)

// NewElasticServer starts an LSP server on the supplied stream, and waits until the
// stream is closed.
func NewElasticServer(ctx context.Context, cache source.Cache, stream jsonrpc2.Stream) (context.Context, *ElasticServer) {
	s := &ElasticServer{}
	ctx, s.Conn, s.client = protocol.NewElasticServer(ctx, stream, s)
	s.session = cache.NewSession(ctx)
	return ctx, s
}

// RunElasticServerOnPort starts an LSP server on the given port and does not exit.
// This function exists for debugging purposes.
func RunElasticServerOnPort(ctx context.Context, cache source.Cache, port int, h func(ctx context.Context, s *ElasticServer)) error {
	return RunElasticServerOnAddress(ctx, cache, fmt.Sprintf(":%v", port), h)
}

// RunElasticServerOnAddress starts an LSP server on the given port and does not exit.
// This function exists for debugging purposes.
func RunElasticServerOnAddress(ctx context.Context, cache source.Cache, addr string, h func(ctx context.Context, s *ElasticServer)) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		h(NewElasticServer(ctx, cache, jsonrpc2.NewHeaderStream(conn, conn)))
	}
}

// ElasticServer "inherits" from lsp.server and is used to implement the elastic extension for the official go lsp.
type ElasticServer struct {
	Server
	FolderNeedsCleanup []string
}

func (s *ElasticServer) RunElasticServer(ctx context.Context) error {
	return s.Conn.Run(ctx)
}

// EDefinition has almost the same functionality with Definition except for the qualified name and symbol kind.
func (s *ElasticServer) EDefinition(ctx context.Context, params *protocol.DefinitionParams) ([]protocol.SymbolLocator, error) {
	uri := span.NewURI(params.TextDocument.URI)
	view := s.session.ViewOf(uri)
	f, err := getGoFile(ctx, view, uri)
	if err != nil {
		return nil, err
	}
	ident, err := source.Identifier(ctx, view, f, params.Position)
	if err != nil {
		return nil, err
	}
	declRange, err := ident.Declaration.Range()
	if err != nil {
		return nil, err
	}
	// Check whether the definition is in the current view, i.e. workspace folders. One repo may has several workspace folders.
	if strings.HasPrefix(ident.Declaration.URI().Filename(), view.Folder().Filename()) {
		// If it is the same-workspace folder jump, return early.
		return []protocol.SymbolLocator{{
			Loc: &protocol.Location{
				URI:   protocol.NewURI(ident.Declaration.URI()),
				Range: declRange,
			},
			Package: protocol.PackageLocator{},
		}}, nil
	}
	// If it is the cross-view jump, only return the qname, symbol kind and package locator.
	declObj := ident.GetDeclObject()
	declURI := ident.Declaration.URI()
	declFile, err := getGoFile(ctx, view, declURI)
	if err != nil {
		return nil, err
	}
	kind := getSymbolKind(declObj)
	if kind == 0 {
		return nil, fmt.Errorf("no corresponding symbol kind for '" + ident.Name + "'")
	}
	qname := getQName(ctx, declFile, declObj, kind)
	declPath := declURI.Filename()
	pkgLocator := collectPkgMetadata(declObj.Pkg(), view.Folder().Filename(), declPath)
	return []protocol.SymbolLocator{{Qname: qname, Kind: kind, Package: pkgLocator}}, nil
}

const (
	folderSkip = string(filepath.Separator) + "vendor" + string(filepath.Separator)
)

// Full collects the symbols defined in the current file and the references.
func (s *ElasticServer) Full(ctx context.Context, fullParams *protocol.FullParams) (protocol.FullResponse, error) {
	params := protocol.DocumentSymbolParams{TextDocument: fullParams.TextDocument}
	fullResponse := protocol.FullResponse{
		Symbols:    []protocol.DetailSymbolInformation{},
		References: []protocol.Reference{},
	}
	uri := span.NewURI(fullParams.TextDocument.URI)
	// Intercept the 'full' request for 'vendor' folder.
	// TODO(henrywong) Support the code intelligence for 'vendor' folder
	if ok := strings.Contains(uri.Filename(), folderSkip); ok {
		return fullResponse, nil
	}
	view := s.session.ViewOf(uri)
	f, err := getGoFile(ctx, view, uri)
	if err != nil {
		return fullResponse, err
	}
	path := f.URI().Filename()
	cphs, err := f.CheckPackageHandles(ctx)
	if err != nil {
		return fullResponse, err
	}
	cph := source.NarrowestCheckPackageHandle(cphs)
	pkg, err := cph.Check(ctx)
	if err != nil {
		return fullResponse, err
	}
	pkgLocator := collectPkgMetadata(pkg.GetTypes(), view.Folder().Filename(), path)

	detailSyms, err := constructDetailSymbol(s, ctx, &params, &pkgLocator)
	if err != nil {
		return fullResponse, err
	}
	fullResponse.Symbols = detailSyms

	// TODO(henrywong) We won't collect the references for now because of the performance issue. Once the 'References'
	//  option is true, we will implement the references collecting feature.
	if !fullParams.Reference {
		return fullResponse, nil
	}
	return fullResponse, nil
}

// ManageDeps will explore the workspace folders sent from the client and give a whole picture of them. Besides that,
// ManageDeps will try its best to convert the folders to modules. The core functions, like deps downloading and deps
// management, will be implemented in the package 'cache'.
func (s *ElasticServer) ManageDeps(ctx context.Context, folders *[]protocol.WorkspaceFolder, option interface{}) error {
	installGoDeps := false
	if v, ok := option.(bool); v && ok {
		installGoDeps = true
	}
	// In order to handle the modules separately, we consider different modules as different workspace folders, so we
	// can manage the dependency of different modules separately.
	for _, folder := range *folders {
		if folder.URI == "" {
			continue
		}
		metadata := &ModuleConverter{folder: span.NewURI(folder.URI).Filename(), installGoDeps: installGoDeps}
		err := metadata.collectMetadata(ctx)
		s.FolderNeedsCleanup = append(s.FolderNeedsCleanup, metadata.FolderNeedsCleanup...)
		if err != nil {
			return err
		}
		// Convert the module folders to the workspace folders.
		for _, folder := range metadata.moduleFolders {
			uri := span.NewURI(folder)
			notExists := true
			for _, wf := range *folders {
				if filepath.Clean(string(uri)) == filepath.Clean(wf.URI) {
					notExists = false
				}
			}
			if notExists {
				*folders = append(*folders, protocol.WorkspaceFolder{URI: string(uri), Name: filepath.Base(folder)})
			}
		}
	}
	return nil
}

func (s ElasticServer) Cleanup() {
	for _, folder := range s.FolderNeedsCleanup {
		goMod := filepath.Join(folder, "go.mod")
		goSum := filepath.Join(folder, "go.sum")
		if _, err := os.Stat(goMod); err == nil {
			os.Remove(goMod) // ignore the errors
		}
		if _, err := os.Stat(goSum); err == nil {
			os.Remove(goSum) // ignore the errors
		}
	}
}

// getSymbolKind get the symbol kind for a single position.
func getSymbolKind(declObj types.Object) protocol.SymbolKind {
	switch declObj.(type) {
	case *types.Const:
		return protocol.Constant
	case *types.Var:
		v, _ := declObj.(*types.Var)
		if v.IsField() {
			return protocol.Field
		}
		return protocol.Variable
	case *types.Nil:
		return protocol.Null
	case *types.PkgName:
		return protocol.Package
	case *types.Func:
		s, _ := declObj.Type().(*types.Signature)
		if s.Recv() == nil {
			return protocol.Function
		}
		return protocol.Method
	case *types.TypeName:
		switch declObj.Type().Underlying().(type) {
		case *types.Struct:
			return protocol.Struct
		case *types.Interface:
			return protocol.Interface
		case *types.Slice:
			return protocol.Array
		case *types.Array:
			return protocol.Array
		case *types.Basic:
			b, _ := declObj.Type().Underlying().(*types.Basic)
			if b.Info()&types.IsNumeric != 0 {
				return protocol.Number
			} else if b.Info()&types.IsBoolean != 0 {
				return protocol.Boolean
			} else if b.Info()&types.IsString != 0 {
				return protocol.String
			}
		}
	}

	// TODO(henrywong) For now, server use 0 represent the unknown symbol kind, however this is not a good practice, see
	//  https://github.com/Microsoft/language-server-protocol/issues/129.
	return protocol.SymbolKind(0)
}

// getQName returns the qualified name for a position in a file. Qualified name mainly served as the cross repo code
// search and code intelligence. The qualified name pattern as bellow:
//  qname = package.name + struct.name* + function.name* | (struct.name + method.name)* + struct.name* + symbol.name
//
// TODO(henrywong) It's better to use the scope chain to give a qualified name for the symbols, however there is no
// APIs can achieve this goals, just traverse the ast node path for now.
func getQName(ctx context.Context, f source.GoFile, declObj types.Object, kind protocol.SymbolKind) string {
	qname := declObj.Name()
	if kind == protocol.Package {
		return qname
	}
	fh := f.Handle(ctx)
	fAST, _, _, err := f.View().Session().Cache().ParseGoHandle(fh, source.ParseExported).Parse(ctx)
	if err != nil {
		return ""
	}
	pos := declObj.Pos()
	astPath, _ := astutil.PathEnclosingInterval(fAST, pos, pos)
	// TODO(henrywong) Should we put a check here for the case of only one node?
	for id, n := range astPath[1:] {
		switch n.(type) {
		case *ast.StructType:
			// Check its father to decide whether the ast.StructType is a named type or an anonymous type.
			switch astPath[id+2].(type) {
			case *ast.TypeSpec:
				// ident is located in a named struct declaration, add the type name into the qualified name.
				ts, _ := astPath[id+2].(*ast.TypeSpec)
				qname = ts.Name.Name + "." + qname
			case *ast.Field:
				// ident is located in a anonymous struct declaration which used to define a field, like struct fields,
				// function parameters, function named return parameters, add the field name into the qualified name.
				field, _ := astPath[id+2].(*ast.Field)
				if len(field.Names) != 0 {
					// If there is a bunch of fields declared with same anonymous struct type, just consider the first field's
					// name.
					qname = field.Names[0].Name + "." + qname
				}

			case *ast.ValueSpec:
				// ident is located in a anonymous struct declaration which used define a variable, add the variable name into
				// the qualified name.
				vs, _ := astPath[id+2].(*ast.ValueSpec)
				if len(vs.Names) != 0 {
					// If there is a bunch of variables declared with same anonymous struct type, just consider the first
					// variable's name.
					qname = vs.Names[0].Name + "." + qname
				}
			}
		case *ast.InterfaceType:
			// Check its father to get the interface name.
			switch astPath[id+2].(type) {
			case *ast.TypeSpec:
				ts, _ := astPath[id+2].(*ast.TypeSpec)
				qname = ts.Name.Name + "." + qname
			}

		case *ast.FuncDecl:
			f, _ := n.(*ast.FuncDecl)
			// If n is method, add the struct name as a prefix.
			if f.Recv != nil {
				var typeName string
				switch r := f.Recv.List[0].Type.(type) {
				case *ast.StarExpr:
					typeName = r.X.(*ast.Ident).Name
				case *ast.Ident:
					typeName = r.Name
				}
				qname = typeName + "." + qname
			}
		}
	}
	if declObj.Pkg() == nil {
		return qname
	}
	return declObj.Pkg().Name() + "." + qname
}

// collectPackageMetadata collects metadata for the packages where the specified symbols located and the scheme, i.e.
// URL prefix, of the repository which the packages belong to.
func collectPkgMetadata(pkg *types.Package, dir string, loc string) protocol.PackageLocator {
	if pkg == nil {
		return protocol.PackageLocator{}
	}
	pkgLocator := protocol.PackageLocator{
		Name:    pkg.Name(),
		RepoURI: pkg.Path(),
	}
	// If the package is located in the standard library, there is no need to resolve the revision.
	if strings.HasPrefix(loc, dir) || strings.HasPrefix(loc, goRoot) {
		return pkgLocator
	}
	getPkgVersion(dir, &pkgLocator, loc)
	repoRoot, err := vcs.RepoRootForImportPath(pkg.Path(), false)
	if err == nil {
		pkgLocator.RepoURI = repoRoot.Repo
		return pkgLocator
	}
	return pkgLocator
}

// getPkgVersion collects the version information for a specified package, the version information will be one of the
// two forms semver format and prefix of a commit hash.
func getPkgVersion(dir string, pkgLoc *protocol.PackageLocator, loc string) {
	rev := getPkgVersionFast(strings.TrimPrefix(loc, filepath.Join(pkgMod, dir)))
	if rev == "" {
		if err := getPkgVersionSlow(); err != nil {
			return
		}
	}
	// In general, the module version is in semver format and it's bound to be accompanied by a semver tag. But
	// sometimes, like when there is no tag or try to get the latest commit, the module version is in pseudo-version
	// pseudo-version format. Strip off the prefix to get the commit hash part which is a prefix of the full commit
	// hash.
	if strings.Count(rev, "-") == 2 {
		rev = strings.TrimSuffix(rev, "+incompatible")
		i := strings.LastIndex(rev, "-")
		rev = rev[i+1:]
	}
	pkgLoc.Version = rev
}

// getPkgVersionSlow get the pkg revision with a more accurate approach, call 'go list' again is an option, but it not
// wise to call 'go list' twice.
// TODO(henrywong) Use correct API to get the revision.
func getPkgVersionSlow() error {
	return fmt.Errorf("for now, there is no proper and efficient API to get the revision")
}

// getPkgVersionFast extract the revision in a fast manner. 'go list' will create a folder whose name will contain the
// revision, we can extract it from the path, like '.../modulename@v1.2.3/...', this approach can avoid call 'go list'
// multiple times. If there are multiple valid version substrings, give up.
func getPkgVersionFast(loc string) string {
	strs := strings.SplitAfter(loc, "@")
	var validVersion []string
	for i := 1; i < len(strs); i++ {
		substrs := strings.Split(strs[i], string(filepath.Separator))
		if semver.IsValid(substrs[0]) {
			validVersion = append(validVersion, substrs[0])
		}
	}
	if len(validVersion) != 1 {
		// give up
		return ""
	}
	return validVersion[0]
}

var (
	importCommentRE         = regexp.MustCompile(`(?m)^package[ \t]+[^ \t\r\n/]+[ \t]+//[ \t]+import[ \t]+(\"[^"]+\")[ \t]*\r?\n`)
	DependencyControlSystem = []string{
		"GLOCKFILE",
		"Godeps/Godeps.json",
		"Gopkg.lock",
		"dependencies.tsv",
		"glide.lock",
		"vendor.conf",
		"vendor.yml",
		"vendor/manifest",
		"vendor/vendor.json",
	}
)

// ModuleConverter serves the following two purposes:
// - Convert the folder to module to get rid of the '$GOPATH/src' limitation.
// - Recognize the potential multi-module cases.
type ModuleConverter struct {
	folder             string
	moduleFolders      []string
	installGoDeps      bool
	FolderNeedsCleanup []string
}

func (metadata *ModuleConverter) goModInit(folder string) error {
	// For the folders which have pure vendor folder, delay the 'go.mod' construction into initialize handler.
	if isPureVendor(folder) {
		metadata.FolderNeedsCleanup = append(metadata.FolderNeedsCleanup, folder)
		return nil
	}
	modulePath := getModulePath(folder)
	if metadata.installGoDeps {
		cmd := exec.Command("go", "mod", "init", modulePath)
		cmd.Dir = folder
		return cmd.Run()
	} else {
		metadata.FolderNeedsCleanup = append(metadata.FolderNeedsCleanup, folder)
		return constructGoModManually(folder, modulePath)
	}
}

// collectWorkspaceFolderMetadata explores the workspace folder to collects the meta information of the folder. And
// create a new 'go.mod' if necessary to cover all the source files.
func (metadata *ModuleConverter) collectMetadata(ctx context.Context) error {
	// Collect 'go.mod' and record them as workspace folders.
	if err := filepath.Walk(metadata.folder, func(path string, info os.FileInfo, err error) error {
		base := filepath.Base(path)
		if (base[0] == '.' || base == "vendor") && info.IsDir() {
			return filepath.SkipDir
		} else if info.Name() == "go.mod" {
			dir := filepath.Dir(path)
			metadata.moduleFolders = append(metadata.moduleFolders, dir)
		}
		return nil
	}); err != nil {
		return err
	}
	folderUncovered, folderNeedMod, err := collectUncoveredSrc(metadata.folder)
	if err != nil {
		return nil
	}
	// If folders need to be covered exist, a new 'go.mod' will be created manually.
	if len(folderUncovered) > 0 {
		longestPrefix := string(filepath.Separator)
		// Compute the longest common prefix of the folders which need to be covered by 'go.mod'.
	DONE:
		for i, name := range folderUncovered[0] {
			same := true
			for _, folder := range folderUncovered[1:] {
				if len(folder) <= i || folder[i] != name {
					same = false
					break DONE
				}
			}
			if same {
				longestPrefix = filepath.Join(longestPrefix, name)
			}
		}
		folderNeedMod = append(folderNeedMod, filepath.Clean(longestPrefix))
	}

	for _, folder := range folderNeedMod {
		if err := metadata.goModInit(folder); err != nil {
			log.Error(ctx, "error when initializing module", err, telemetry.File)
			continue
		}
		metadata.moduleFolders = append(metadata.moduleFolders, folder)
	}
	return nil
}

// collectUncoveredSrc explores the rootPath recursively, collects
//  - folders need to be covered, which we will create a module to cover all these folders.
//  - folders need to create a module.
func collectUncoveredSrc(path string) ([][]string, []string, error) {
	var folderUncovered [][]string
	var folderNeedMod []string
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return nil, nil, nil
	}
	// existDepControlFile determines if dependency control files exist in the specified folder.
	existDepControlFile := func(dir string) bool {
		for _, name := range DependencyControlSystem {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return true
			}
		}
		return false
	}
	// Given that we have to respect the original dependency control data, if there is a dependency control file, we
	// we will create a 'go.mod' accordingly.
	if existDepControlFile(path) {
		folderNeedMod = append(folderNeedMod, path)
		return nil, folderNeedMod, nil
	}
	if _, err := os.Stat(filepath.Join(path, "vendor")); err == nil {
		folderNeedMod = append(folderNeedMod, path)
		return nil, folderNeedMod, nil
	}
	// If there are remaining '.go' source files under the current folder, that means they will not be covered by
	// any 'go.mod'.
	shouldBeCovered := false
	fileInfo, err := ioutil.ReadDir(path)
	if err != nil {
		return nil, nil, err
	}
	for _, info := range fileInfo {
		if !shouldBeCovered && filepath.Ext(info.Name()) == ".go" && !strings.HasSuffix(info.Name(), "_test.go") {
			shouldBeCovered = true
		}
		if info.IsDir() && info.Name()[0] != '.' {
			uncovered, mod, e := collectUncoveredSrc(filepath.Join(path, info.Name()))
			folderNeedMod = append(folderNeedMod, mod...)
			folderUncovered = append(folderUncovered, uncovered...)
			err = e
		}
	}
	if shouldBeCovered {
		folderUncovered = append(folderUncovered, strings.Split(path, string(filepath.Separator)))
	}
	return folderUncovered, folderNeedMod, err
}

func constructDetailSymbol(s *ElasticServer, ctx context.Context, params *protocol.DocumentSymbolParams, pkgLocator *protocol.PackageLocator) (detailSyms []protocol.DetailSymbolInformation, err error) {
	docSyms, err := (*Server).DocumentSymbol(&s.Server, ctx, params)

	var flattenDocumentSymbol func(*[]protocol.DocumentSymbol, string, string)
	// Note: The reason why we construct the qname during the flatten process is that we can't construct the qname
	// through the 'SymbolInformation.ContainerName' because of the possibilities of the 'ContainerName' collision.
	flattenDocumentSymbol = func(symbols *[]protocol.DocumentSymbol, prefix string, container string) {
		for _, symbol := range *symbols {
			sym := protocol.SymbolInformation{
				Name:          symbol.Name,
				Kind:          symbol.Kind,
				Deprecated:    symbol.Deprecated,
				ContainerName: container,
				Location: protocol.Location{
					URI:   params.TextDocument.URI,
					Range: symbol.SelectionRange,
				},
			}
			var qnamePrefix string
			if prefix != "" {
				qnamePrefix = prefix + "." + symbol.Name
			} else {
				qnamePrefix = symbol.Name
			}
			detailSyms = append(detailSyms, protocol.DetailSymbolInformation{
				Symbol:  sym,
				Qname:   pkgLocator.Name + "." + qnamePrefix,
				Package: *pkgLocator,
			})
			if len(symbol.Children) > 0 {
				flattenDocumentSymbol(&symbol.Children, qnamePrefix, symbol.Name)
			}
		}
	}

	flattenDocumentSymbol(&docSyms, "", "")
	return
}

func getModulePath(folder string) string {
	// findModulePath is copied from 'go/src/cmd/go/internal/modload/init.go'.
	// TODO(henrywong) The best approach to guess the module path is `go mod init`, see
	//  https://github.com/golang/go/blob/release-branch.go1.12/src/cmd/go/alldocs.go#L1040. However in order to get rid
	//  of the external binary invoke, copy the key part which used to guess the module path.
	findModulePath := func() (string, error) {
		findImportComment := func(file string) string {
			data, err := ioutil.ReadFile(file)
			if err != nil {
				return ""
			}
			m := importCommentRE.FindSubmatch(data)
			if m == nil {
				return ""
			}
			path, err := strconv.Unquote(string(m[1]))
			if err != nil {
				return ""
			}
			return path
		}
		// TODO(bcmills): once we have located a plausible module path, we should
		// query version control (if available) to verify that it matches the major
		// version of the most recent tag.
		// See https://golang.org/issue/29433, https://golang.org/issue/27009, and
		// https://golang.org/issue/31549.

		// Cast about for import comments,
		// first in top-level directory, then in subdirectories.
		list, _ := ioutil.ReadDir(folder)
		for _, info := range list {
			if info.Mode().IsRegular() && strings.HasSuffix(info.Name(), ".go") {
				if com := findImportComment(filepath.Join(folder, info.Name())); com != "" {
					return com, nil
				}
			}
		}
		for _, info1 := range list {
			if info1.IsDir() {
				files, _ := ioutil.ReadDir(filepath.Join(folder, info1.Name()))
				for _, info2 := range files {
					if info2.Mode().IsRegular() && strings.HasSuffix(info2.Name(), ".go") {
						if com := findImportComment(filepath.Join(folder, info1.Name(), info2.Name())); com != "" {
							return path.Dir(com), nil
						}
					}
				}
			}
		}

		// Look for Godeps.json declaring import path.
		data, _ := ioutil.ReadFile(filepath.Join(folder, "Godeps/Godeps.json"))
		var cfg1 struct{ ImportPath string }
		json.Unmarshal(data, &cfg1)
		if cfg1.ImportPath != "" {
			return cfg1.ImportPath, nil
		}

		// Look for vendor.json declaring import path.
		data, _ = ioutil.ReadFile(filepath.Join(folder, "vendor/vendor.json"))
		var cfg2 struct{ RootPath string }
		json.Unmarshal(data, &cfg2)
		if cfg2.RootPath != "" {
			return cfg2.RootPath, nil
		}
		msg := `cannot determine module path for source directory %s (outside GOPATH, module path must be specified)`
		return "", fmt.Errorf(msg, folder)
	}
	modulePath, err := findModulePath()
	if err != nil {
		list := strings.Split(folder, string(filepath.Separator)+"__")
		if len(list) != 2 {
			return folder
		}
		prefixList := strings.Split(list[0], string(filepath.Separator))
		suffixList := strings.Split(list[1], string(filepath.Separator))
		if len(prefixList) < 4 {
			return folder
		}
		// concatenate 'code host/owner/repo'
		modulePath = strings.Join(prefixList[len(prefixList)-3:], "/")
		if len(suffixList) < 3 {
			return modulePath
		}
		// Skip the dummy hash folder and branch name, concatenates remain elements for the submodule cases.
		modulePath = strings.Join(append([]string{modulePath}, suffixList[2:]...), "/")
	}
	return modulePath
}

func constructGoModManually(folder string, modulePath string) error {
	if _, err := os.Stat(filepath.Join(folder, "go.mod")); err == nil {
		return nil
	}
	// construct the 'go.mod' manually.
	goMod, err := os.Create(filepath.Join(folder, "go.mod"))
	if err != nil {
		return err
	}
	defer goMod.Close()
	data := "module " + modulePath
	if _, err := goMod.WriteString(data); err != nil {
		return err
	}
	return nil
}

func isPureVendor(folder string) bool {
	if _, err := os.Stat(filepath.Join(folder, "go.mod")); err == nil {
		return false
	}
	for _, name := range DependencyControlSystem {
		if _, err := os.Stat(filepath.Join(folder, name)); err == nil {
			return false
		}
	}
	if _, err := os.Stat(filepath.Join(folder, "vendor")); err == nil {
		return true
	}
	return false
}
