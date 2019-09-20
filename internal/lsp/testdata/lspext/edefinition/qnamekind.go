package edefinition //Test fail here, edefinition("def", "edefinition", 4)

import (
	"fmt"
	"golang.org/x/tools/internal/lsp/foo"
	"golang.org/x/tools/internal/lsp/godef/a"
	"golang.org/x/tools/internal/lsp/godef/b"
	"golang.org/x/tools/internal/lsp/types"
)

// FileSymbol          SymbolKind = 1
// ModuleSymbol        SymbolKind = 2
// NamespaceSymbol     SymbolKind = 3
// PackageSymbol       SymbolKind = 4
// ClassSymbol         SymbolKind = 5
// MethodSymbol        SymbolKind = 6
// PropertySymbol      SymbolKind = 7
// FieldSymbol         SymbolKind = 8
// ConstructorSymbol   SymbolKind = 9
// EnumSymbol          SymbolKind = 10
// InterfaceSymbol     SymbolKind = 11
// FunctionSymbol      SymbolKind = 12
// VariableSymbol      SymbolKind = 13
// ConstantSymbol      SymbolKind = 14
// StringSymbol        SymbolKind = 15
// NumberSymbol        SymbolKind = 16
// BooleanSymbol       SymbolKind = 17
// ArraySymbol         SymbolKind = 18
// ObjectSymbol        SymbolKind = 19
// KeySymbol           SymbolKind = 20
// NullSymbol          SymbolKind = 21
// EnumMemberSymbol    SymbolKind = 22
// StructSymbol        SymbolKind = 23
// EventSymbol         SymbolKind = 24
// OperatorSymbol      SymbolKind = 25
// TypeParameterSymbol SymbolKind = 26

func qnameKind() {
	// FunctionSymbol
	b.Bar() //@qnamekind("B", "b.Bar", 12)

	// StructSymbol
	s := b.S1{} //@qnamekind("S", "b.S1", 23)

	// FieldSymbol
	fmt.Println(s.F1) //@qnamekind("F1", "b.S1.F1", 8)

	// StringSymbol
	str := a.A //@qnamekind("A", "a.A", 15)

	// NumberSymbol
	var i foo.IntFoo //@qnamekind("I", "foo.IntFoo", 16)

	// InterfaceSymbol
	var bob types.Bob //@qnamekind("B", "types.Bob", 11)

	var x types.X
	// MethodSymbol
	x.Bobby() //@qnamekind("B", "types.X.Bobby", 6)
}
