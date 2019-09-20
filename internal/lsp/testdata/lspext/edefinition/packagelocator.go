package edefinition //Test fail here, edefinition("def", "edefinition", 4)

import (
	jsoniter "github.com/json-iterator/go"
	"golang.org/x/tools/internal/lsp/foo"
	"golang.org/x/tools/internal/lsp/godef/a"
	"golang.org/x/tools/internal/lsp/godef/b"
	"golang.org/x/tools/internal/lsp/types"
)

func packageLocator() {
	b.Bar() //@packagelocator("B", "b", "https://go.googlesource.com/tools")

	s := b.S1{} //@packagelocator("S", "b", "https://go.googlesource.com/tools")

	str := a.A //@packagelocator("A", "a", "https://go.googlesource.com/tools")

	var i foo.IntFoo //@packagelocator("I", "foo", "https://go.googlesource.com/tools")

	var bob types.Bob //@packagelocator("B", "types", "https://go.googlesource.com/tools")

	var x types.X
	x.Bobby() //@packagelocator("B", "types", "https://go.googlesource.com/tools")
}
