package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/codec"
)

//go:generate go run ../cmd/magus-utils bindings -module json -lang buzz -out ../host/gen/json.go

func init() { Register(JSON) }

// JSON is the "json" host module: JSON encode/decode for spells.
var JSON = Module{
	Name: "json",
	Doc:  "JSON encode/decode.",
	Methods: []Method{
		{
			Name:    "parse",
			Doc:     "Decode a JSON string into a value (map, list, string, number, or boolean).",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    JSONParse,
		},
		{
			Name: "stringify",
			Doc:  "Encode a value as a JSON string. With no indent (or \"\") the output is compact; pass an indent string (e.g. \"  \" or \"\\t\") for pretty, multi-line output.",
			Args: []Arg{
				{Name: "value", Type: TypeAny},
				{Name: "indent", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeString}},
			Impl:    JSONStringify,
		},
	},
}

// JSONParse decodes a JSON string into a value (map, list, string, number, or boolean).
func JSONParse(_ context.Context, s string) (any, error) {
	var v any
	if err := codec.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("json.parse: %w", err)
	}
	return v, nil
}

// JSONStringify encodes a value as a JSON string. An empty indent (the
// omitted-arg case — the binding layer passes "" rather than a missing value)
// yields compact output; any non-empty indent selects pretty, multi-line output
// with that string per nesting level. The two share one entry point because
// "compact" is just the no-indent case, so an author has a single call to learn.
func JSONStringify(_ context.Context, value any, indent string) (string, error) {
	marshal := func() ([]byte, error) {
		if indent == "" {
			return codec.Marshal(value)
		}
		return codec.MarshalIndent(value, "", indent)
	}
	b, err := marshal()
	if err != nil {
		return "", fmt.Errorf("json.stringify: %w", err)
	}
	return string(b), nil
}
