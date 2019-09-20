package protocol

type PackageLocator struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	RepoURI string `json:"uri"`
}

// SymbolLocator is the response type for the `textDocument/edefinition` extension.
type SymbolLocator struct {
	// The fully qualified name of the symbol.
	Qname string `json:"qname,omitempty"`

	Kind SymbolKind `json:"kind,omitempty"`

	// The file path relative to the repo root URI of the specified symbol.
	Path string `json:"path,omitempty"`

	// A concrete location at which the definition is located.
	// TODO(henrywong) 'encoding/json' doesn't support the zero values of structs with omitempty for now, see
	//  https://github.com/golang/go/issues/11939. That's why we use pointer here.
	Loc *Location `json:"location,omitempty"`

	Package PackageLocator `json:"package,omitempty"`
}

type FullParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Reference    bool                   `json:"reference"`
}

type DetailSymbolInformation struct {
	Symbol SymbolInformation `json:"symbolInformation"`
	Qname  string            `json:"qname"`
	// Use for hover
	// contents MarkupContent MarkedString MarkedString[] `json:"content"`
	Package PackageLocator `json:"package"`
}

type ReferenceCategory int

const (
	UNCATEGORIZED ReferenceCategory = iota
	READ
	WRITE
	INHERIT
	IMPLEMENT
)

type Reference struct {
	Category ReferenceCategory `json:"category"`
	Loc      Location          `json:"location"`
	Symbol   SymbolInformation `json:"symbol"`
	Target   SymbolLocator     `json:"target"`
}

type FullResponse struct {
	Symbols    []DetailSymbolInformation `json:"symbols"`
	References []Reference               `json:"references"`
}
