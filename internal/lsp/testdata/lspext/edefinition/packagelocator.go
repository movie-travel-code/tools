package edefinition

import (
	"golang.org/x/tools/internal/lsp/foo"
	"golang.org/x/tools/internal/lsp/godef/a"
	"golang.org/x/tools/internal/lsp/godef/b"
	"golang.org/x/tools/internal/lsp/types"
)

// NOTE: EDefinition will give different result for the repository URI, will give the import path without the network
// access. And give actual repository URI with the network enabled.

// Disable the test temporarily until we find the consistent solution.

func packageLocator() {
	b.Bar() //packagelocator("B", "b", "https://go.googlesource.com/tools")

	s := b.S1{} //packagelocator("S", "b", "https://go.googlesource.com/tools")

	str := a.A //packagelocator("A", "a", "https://go.googlesource.com/tools")

	var i foo.IntFoo //packagelocator("I", "foo", "https://go.googlesource.com/tools")

	var bob types.Bob //packagelocator("B", "types", "https://go.googlesource.com/tools")

	var x types.X
	x.Bobby() //packagelocator("B", "types", "https://go.googlesource.com/tools")
}
