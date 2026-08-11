// Package toml is the "toml" host module. See std/encoding/register.go for
// why this and its eight siblings each get their own leaf package instead of
// living in std's flat root, and how this directory's Module reaches the rest
// of magus without std importing back down to collect it.
package toml

import (
	"context"
	"fmt"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module toml -lang buzz -out ../../../internal/interp/bindings/gen/toml.go

// Module is the "toml" host module: TOML parse and stringify via pelletier/go-toml/v2.
// It mirrors the json and yaml modules so a magusfile can read a value out of a
// pyproject.toml / Cargo.toml the same way it reads package.json.
var Module = std.Module{
	Name: "toml",
	Path: "encoding/toml",
	Doc:  "TOML parse and stringify (TOML 1.0 via pelletier/go-toml/v2).",
	Methods: []std.Method{
		{
			Name:    "parse",
			Doc:     "Decode a TOML document into a value (tables become maps, arrays become lists, plus strings, numbers, bools, and datetimes); errors on invalid input.",
			Args:    []std.Arg{{Name: "source", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeAny}},
			Impl:    TOMLParse,
		},
		{
			Name:    "stringify",
			Doc:     "Encode a value to a TOML string; the top level must be a table/map, as TOML requires. Errors on unencodable input.",
			Args:    []std.Arg{{Name: "value", Type: std.TypeAny}},
			Returns: []std.Ret{{Type: std.TypeString}},
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
