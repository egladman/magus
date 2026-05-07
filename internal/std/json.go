package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/codec"
)

//go:generate go run ../../cmd/magus-bindings-gen -module json -lang lua -out gen/lua/json.go
//go:generate go run ../../cmd/magus-bindings-gen -module json -lang buzz -out gen/buzz/json.go

func init() { Register(JSON) }

// JSON is the "json" host module: JSON encode/decode for spells.
var JSON = Module{
	Name: "json",
	Doc:  "JSON encode/decode.",
	Methods: []Method{
		{
			Name:    "parse",
			Doc:     "Decode a JSON string into a Lua value (table, string, number, or boolean).",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    JSONParse,
		},
		{
			Name:    "stringify",
			Doc:     "Encode a Lua value as a JSON string.",
			Args:    []Arg{{Name: "value", Type: TypeAny}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    JSONStringify,
		},
	},
}

// JSONParse decodes a JSON string into a Lua-compatible value (table, string, number, or boolean).
func JSONParse(_ context.Context, s string) (any, error) {
	var v any
	if err := codec.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("json.parse: %w", err)
	}
	return v, nil
}

// JSONStringify encodes a Lua value as a JSON string.
func JSONStringify(_ context.Context, value any) (string, error) {
	b, err := codec.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("json.stringify: %w", err)
	}
	return string(b), nil
}
