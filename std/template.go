//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/cbroglie/mustache"
)

//go:generate go run ../cmd/magus-utils bindings -module template -lang buzz -out ../host/gen/template.go

func init() { Register(Template) }

// Template is the "template" host module: logic-less Mustache rendering via
// github.com/cbroglie/mustache, which tracks the mustache/spec. Chosen over Go's
// text/template: Mustache is a cross-language spec with a conformance suite, and
// being logic-less it keeps generated config files predictable.
var Template = Module{
	Name: "template",
	Doc:  "Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache).",
	Methods: []Method{
		{
			Name:    "render",
			Doc:     "Render a Mustache template against a context value (usually a name->value map; lists drive sections, absent/false keys hide them). Returns the filled string; errors on a malformed template.",
			Args:    []Arg{{Name: "template", Type: TypeString}, {Name: "data", Type: TypeAny}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    TemplateRender,
		},
	},
}

// TemplateRender renders tmpl against data with Mustache semantics.
func TemplateRender(_ context.Context, tmpl string, data any) (string, error) {
	out, err := mustache.Render(tmpl, data)
	if err != nil {
		return "", fmt.Errorf("template.render: %w", err)
	}
	return out, nil
}
