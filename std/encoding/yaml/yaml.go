// Package yaml is the "yaml" host module. See std/encoding/register.go for
// why this and its eight siblings each get their own leaf package instead of
// living in std's flat root, and how this directory's Module reaches the rest
// of magus without std importing back down to collect it.
package yaml

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module yaml -lang buzz -out ../../../internal/interp/bindings/gen/yaml.go

// Module is the "yaml" host module: YAML parse and stringify via gopkg.in/yaml.v3.
var Module = std.Module{
	Name: "yaml",
	WASM: true,
	Path: "encoding/yaml",
	Doc:  "YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).",
	Methods: []std.Method{
		{
			Name:    "parse",
			Doc:     "Decode a YAML string into a value (maps, lists, strings, numbers, bools, null); errors on invalid input.",
			Args:    []std.Arg{{Name: "source", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeAny}},
			Raises:  true,
			Impl:    YAMLParse,
		},
		{
			Name:    "stringify",
			Doc:     "Encode a value to a YAML string; errors on unencodable input.",
			Args:    []std.Arg{{Name: "value", Type: std.TypeAny}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    YAMLStringify,
		},
	},
}

// YAMLParse decodes source as YAML. gopkg.in/yaml.v3 decodes maps as
// map[string]interface{} when the target is interface{}, so the result is safe
// to pass directly to the Buzz boundary.
func YAMLParse(_ context.Context, source string) (any, error) {
	var out any
	if err := yaml.Unmarshal([]byte(source), &out); err != nil {
		return nil, fmt.Errorf("yaml.parse: %w", err)
	}
	return out, nil
}

// YAMLStringify encodes value to a YAML string.
func YAMLStringify(_ context.Context, value any) (string, error) {
	b, err := yaml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("yaml.stringify: %w", err)
	}
	return string(b), nil
}
