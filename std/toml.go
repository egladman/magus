//go:build !wasm

package std

import (
	"context"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"
)

//go:generate go run ../cmd/magus-utils bindings -module toml -lang buzz -out ../host/gen/toml.go

func init() { Register(TOML) }

// TOML is the "toml" host module: TOML parse and stringify via pelletier/go-toml/v2.
// It mirrors the json and yaml modules so a magusfile can read a value out of a
// pyproject.toml / Cargo.toml the same way it reads package.json.
var TOML = Module{
	Name: "toml",
	Doc:  "TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2).",
	Methods: []Method{
		{
			Name:    "parse",
			Doc:     "Decode a TOML document into a value (tables become maps, arrays become lists, plus strings, numbers, bools, and datetimes); errors on invalid input.",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    TOMLParse,
		},
		{
			Name:    "stringify",
			Doc:     "Encode a value to a TOML string; the top level must be a table/map, as TOML requires. Errors on unencodable input.",
			Args:    []Arg{{Name: "value", Type: TypeAny}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    TOMLStringify,
		},
	},
}

// TOMLParse decodes source as TOML. Unmarshaling into interface{} yields
// map[string]interface{} for tables (a TOML document is always a top-level
// table), so the result is safe to pass across the Buzz boundary.
func TOMLParse(_ context.Context, source string) (any, error) {
	var out any
	if err := toml.Unmarshal([]byte(source), &out); err != nil {
		return nil, fmt.Errorf("toml.parse: %w", err)
	}
	return out, nil
}

// TOMLStringify encodes value to a TOML string.
func TOMLStringify(_ context.Context, value any) (string, error) {
	b, err := toml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("toml.stringify: %w", err)
	}
	return string(b), nil
}
