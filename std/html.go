package std

import (
	"context"
	"html"
)

// HTML is the "html" host module. One escaper serves HTML, XML feeds and SVG,
// so a script never carries its own entity table.
var HTML = Module{
	Name: "html",
	WASM: true,
	Doc:  "HTML, XML and SVG text escaping.",
	Methods: []Method{
		{
			Name:    "escape",
			Doc:     "Replace & < > \" ' in s with entities; valid in element text and in a quoted attribute of HTML, XML or SVG.",
			Args:    []Arg{{Name: "s"}},
			Returns: []Ret{{}},
			Impl:    HTMLEscape,
		},
	},
}

// HTMLEscape returns s with & < > " ' replaced by entities.
func HTMLEscape(_ context.Context, s string) (string, error) {
	return html.EscapeString(s), nil
}
