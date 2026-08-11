// Package json is the "json" host module. See std/encoding/register.go for
// why this and its eight siblings each get their own leaf package instead of
// living in std's flat root, and how this directory's Module reaches the rest
// of magus without std importing back down to collect it.
package json

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module json -lang buzz -out ../../../internal/interp/bindings/gen/json.go

// Module is the "json" host module: JSON encode/decode for spells.
var Module = std.Module{
	Name: "json",
	Path: "encoding/json",
	Doc:  "JSON encode/decode.",
	Methods: []std.Method{
		{
			Name:    "parse",
			Doc:     "Decode a JSON string into a value (map, list, string, number, or boolean).",
			Args:    []std.Arg{{Name: "s", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeAny}},
			Raises:  true,
			Impl:    JSONParse,
		},
		{
			Name: "stringify",
			Doc:  "Encode a value as a JSON string. With no indent (or \"\") the output is compact; pass an indent string (e.g. \"  \" or \"\\t\") for pretty, multi-line output.",
			Args: []std.Arg{
				{Name: "value", Type: std.TypeAny},
				{Name: "indent", Type: std.TypeString, Optional: true},
			},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    JSONStringify,
		},
	},
}

// JSONParse decodes a JSON string into a value (map, list, string, number, or boolean).
func JSONParse(_ context.Context, s string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("json.parse: %w", err)
	}
	return v, nil
}

// JSONStringify encodes a value as a JSON string. An empty indent (the
// omitted-arg case - the binding layer passes "" rather than a missing value)
// yields compact output; any non-empty indent selects pretty, multi-line output
// with that string per nesting level. The two share one entry point because
// "compact" is just the no-indent case, so an author has a single call to learn.
func JSONStringify(_ context.Context, value any, indent string) (string, error) {
	marshal := func() ([]byte, error) {
		if indent == "" {
			return json.Marshal(value)
		}
		return json.MarshalIndent(value, "", indent)
	}
	b, err := marshal()
	if err != nil {
		return "", fmt.Errorf("json.stringify: %w", err)
	}
	return string(b), nil
}
