package lsp

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"golang.org/x/tools/internal/lsp/source"
	"golang.org/x/tools/internal/lsp/tests"
	"golang.org/x/tools/internal/span"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages/packagestest"
	"golang.org/x/tools/internal/lsp/cache"
	"golang.org/x/tools/internal/lsp/protocol"
)

func TestLSPExt(t *testing.T) {
	packagestest.TestAll(t, testLSPExt)
}

const extViewName = "lspext_test"

func testLSPExt(t *testing.T, exporter packagestest.Exporter) {
	ctx := tests.Context(t)
	const dir = "testdata"

	// We hardcode the expected number of test cases to ensure that all tests
	// are being executed. If a test is added, this number must be changed.
	const expectedQNameKindCount = 7
	const expectedPkgLocatorCount = 6
	const expectedFullSymbolCount = 14

	files := packagestest.MustCopyFileTree(dir)
	for fragment, operation := range files {
		if trimmed := strings.TrimSuffix(fragment, ".in"); trimmed != fragment {
			delete(files, fragment)
			files[trimmed] = operation
		}
	}
	modules := []packagestest.Module{
		{
			Name:  "golang.org/x/tools/internal/lsp",
			Files: files,
		},
	}
	exported := packagestest.Export(t, exporter, modules)
	defer exported.Cleanup()

	// Merge the exported.Config with the view.Config.
	cfg := *exported.Config
	cfg.Fset = token.NewFileSet()
	cfg.Context = context.Background()
	cfg.ParseFile = func(fset *token.FileSet, filename string, src []byte) (*ast.File, error) {
		return parser.ParseFile(fset, filename, src, parser.AllErrors|parser.ParseComments)
	}

	cache := cache.New()
	session := cache.NewSession(ctx)
	options := session.Options()
	options.Env = cfg.Env
	var viewRoot string
	if strings.Contains(cfg.Dir, "primarymod") {
		viewRoot = filepath.Join(cfg.Dir, "lspext")
	} else {
		viewRoot = filepath.Join(cfg.Dir, "golang.org/x/tools/internal/lsp/lspext")
	}
	session.NewView(cfg.Context, extViewName, span.FileURI(viewRoot), options)
	s := &Server{
		session:     session,
		undelivered: make(map[span.URI][]source.Diagnostic),
	}
	es := &ElasticServer{*s, nil}

	expectedQNameKinds := make(QnameKindMap)
	expectedPkgLocators := make(PkgMap)
	expectedFullSymbol := make(FullSymMap)

	// Collect any data that needs to be used by subsequent tests.
	if err := exported.Expect(map[string]interface{}{
		"packagelocator": expectedPkgLocators.collect,
		"qnamekind":      expectedQNameKinds.collect,
		"fullsym":        expectedFullSymbol.collect,
	}); err != nil {
		t.Fatal(err)
	}

	t.Run("QNameKind", func(t *testing.T) {
		t.Helper()
		if len(expectedQNameKinds) != expectedQNameKindCount {
			t.Errorf("got %v qnamekinds expected %v", len(expectedQNameKinds), expectedQNameKindCount)
		}
		expectedQNameKinds.test(t, es)
	})
	t.Run("PKG", func(t *testing.T) {
		t.Helper()
		if len(expectedPkgLocators) != expectedPkgLocatorCount {
			t.Errorf("got %v pkgs expected %v", len(expectedPkgLocators), expectedPkgLocatorCount)
		}
		expectedPkgLocators.test(t, es)
	})
	t.Run("Full", func(t *testing.T) {
		t.Helper()
		if len(expectedFullSymbol) != expectedFullSymbolCount {
			t.Errorf("got %v full symbols expected %v", len(expectedFullSymbol), expectedFullSymbolCount)
		}
		expectedFullSymbol.test(t, es)
	})
}

type QNameKindResult struct {
	Qname string
	Kind  int64
}

type PkgResultTuple struct {
	PkgName string
	RepoURI string
}

type PackageLocator struct {
	Version string
	Name    string
	RepoURI string
}
type DetailSymInfo struct {
	Name          string
	Kind          int64
	ContainerName string

	Qname  string
	PkgLoc PackageLocator
}

type QnameKindMap map[protocol.Location]QNameKindResult
type PkgMap map[protocol.Location]PkgResultTuple
type FullSymMap map[protocol.Location]DetailSymInfo

func (qk QnameKindMap) test(t *testing.T, s *ElasticServer) {
	for src, target := range qk {
		params := &protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: src.URI,
				},
				Position: src.Range.Start,
			},
		}
		var symLocators []protocol.SymbolLocator
		var err error
		symLocators, err = s.EDefinition(context.Background(), params)
		if err != nil {
			t.Fatalf("failed for %v: %v", src, err)
		}
		if len(symLocators) != 1 {
			t.Errorf("got %d locations for qnamekind, expected 1", len(symLocators))
		}
		if symLocators[0].Qname != target.Qname {
			t.Errorf("Qname: for %v got %v want %v", src, symLocators[0].Qname, target.Qname)
		}

		if symLocators[0].Kind != protocol.SymbolKind(target.Kind) {
			t.Errorf("Kind: for %v got %v want %v", src, symLocators[0].Kind, target.Kind)
		}
	}
}

func (qk QnameKindMap) collect(e *packagestest.Exported, fset *token.FileSet, src packagestest.Range, qname string, kind int64) {
	sSrc, mSrc := testLocation(e, fset, src)
	lSrc, err := mSrc.Location(sSrc)
	if err != nil {
		return
	}

	qk[lSrc] = QNameKindResult{Qname: qname, Kind: kind}
}

func (ps PkgMap) test(t *testing.T, s *ElasticServer) {
	for src, target := range ps {
		params := &protocol.DefinitionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{
					URI: src.URI,
				},
				Position: src.Range.Start,
			},
		}
		var symLocators []protocol.SymbolLocator
		var err error
		symLocators, err = s.EDefinition(context.Background(), params)
		if err != nil {
			t.Fatalf("failed for %v: %v", src, err)
		}
		if len(symLocators) != 1 {
			t.Errorf("got %d locations for package locators, expected 1", len(symLocators))
		}

		if symLocators[0].Package.Name != target.PkgName {
			t.Errorf("PkgName: for %v got %v want %v", src, symLocators[0].Package.Name, target.PkgName)
		}

		if symLocators[0].Package.RepoURI != target.RepoURI {
			t.Errorf("PkgRepoURI: for %v got %v want %v", src, symLocators[0].Package.RepoURI, target.RepoURI)
		}
	}
}

func (ps PkgMap) collect(e *packagestest.Exported, fset *token.FileSet, src packagestest.Range, pkgname, repouri string) {
	sSrc, mSrc := testLocation(e, fset, src)
	lSrc, err := mSrc.Location(sSrc)
	if err != nil {
		return
	}

	ps[lSrc] = PkgResultTuple{PkgName: pkgname, RepoURI: repouri}
}

func (fs FullSymMap) test(t *testing.T, s *ElasticServer) {
	if len(fs) == 0 {
		return
	}
	var result protocol.FullResponse
	// For now, we just test only source file.
	for src := range fs {
		params := &protocol.FullParams{
			TextDocument: protocol.TextDocumentIdentifier{
				URI: src.URI,
			},
			Reference: false,
		}
		var err error
		result, err = s.Full(context.Background(), params)
		if err != nil {
			t.Fatalf("failed for %v: %v", src, err)
		}
		break
	}

	var resultsMap map[float64]protocol.DetailSymbolInformation
	resultsMap = make(map[float64]protocol.DetailSymbolInformation)
	// Rearrange the results so we can compare them with test data more easily.
	for _, result := range result.Symbols {
		resultsMap[result.Symbol.Location.Range.Start.Line] = result
	}

	var dataMap map[float64]DetailSymInfo
	dataMap = make(map[float64]DetailSymInfo)
	// Rearrange the collected data.
	for src, data := range fs {
		dataMap[src.Range.Start.Line] = data
	}

	for index := range resultsMap {
		data, ok := dataMap[index]
		if !ok {
			t.Errorf("Full Symbol: got unexpected result %v at %v", resultsMap[index], index)
			continue
		}

		if data.Name != resultsMap[index].Symbol.Name {
			t.Errorf("Full Symbol Name: for line %v got %v want %v", index, resultsMap[index].Symbol.Name, dataMap[index].Name)
		}
		if protocol.SymbolKind(data.Kind) != resultsMap[index].Symbol.Kind {
			t.Errorf("Full Symbol Kind: for line %v got %v want %v", index, resultsMap[index].Symbol.Kind, protocol.SymbolKind(dataMap[index].Kind))
		}
		if data.ContainerName != resultsMap[index].Symbol.ContainerName {
			t.Errorf("Full Symbol Container Name: for line %v got %v want %v", index, resultsMap[index].Symbol.ContainerName, dataMap[index].ContainerName)
		}
		if data.Qname != resultsMap[index].Qname {
			t.Errorf("Full Symbol Qname: for line %v got %v want %v", index, resultsMap[index].Qname, dataMap[index].Qname)
		}
		if data.PkgLoc.Name != resultsMap[index].Package.Name {
			t.Errorf("Full Pkg Name: for line %v got %v want %v", index, resultsMap[index].Package.Name, dataMap[index].PkgLoc.Name)
		}
		if data.PkgLoc.Version != resultsMap[index].Package.Version {
			t.Errorf("Full Pkg Version: for line %v got %v want %v", index, resultsMap[index].Package.Version, dataMap[index].PkgLoc.Version)
		}
		if data.PkgLoc.RepoURI != resultsMap[index].Package.RepoURI {
			t.Errorf("Full Pkg RepoURI: for line %v got %v want %v", index, resultsMap[index].Package.RepoURI, dataMap[index].PkgLoc.RepoURI)
		}
	}
}

func (fs FullSymMap) collect(e *packagestest.Exported, fset *token.FileSet, src packagestest.Range, name string, kind int64, containerName, qname, version, pkgName, repoURI string) {
	sSrc, mSrc := testLocation(e, fset, src)
	lSrc, err := mSrc.Location(sSrc)
	if err != nil {
		return
	}

	fs[lSrc] = DetailSymInfo{Name: name, Kind: kind, ContainerName: containerName, Qname: qname, PkgLoc: PackageLocator{Version: version, Name: pkgName, RepoURI: repoURI}}
}

func testLocation(e *packagestest.Exported, fset *token.FileSet, rng packagestest.Range) (span.Span, *protocol.ColumnMapper) {
	spn, err := span.NewRange(fset, rng.Start, rng.End).Span()
	if err != nil {
		return spn, nil
	}
	f := fset.File(rng.Start)
	content, err := e.FileContents(f.Name())
	if err != nil {
		return spn, nil
	}
	converter := span.NewContentConverter(spn.URI().Filename(), content)
	m := &protocol.ColumnMapper{
		URI:       spn.URI(),
		Converter: converter,
		Content:   content,
	}
	return spn, m
}
