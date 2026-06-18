package std

import (
	"context"
	"fmt"

	"gopkg.in/yaml.v3"
)

//go:generate go run ../cmd/magus-bindings-gen -module yaml -lang buzz -out ../hostbuzz/gen/yaml.go

func init() { Register(YAML) }

// YAML is the "yaml" host module: YAML parse and stringify via gopkg.in/yaml.v3.
var YAML = Module{
	Name: "yaml",
	Doc:  "YAML parse and stringify (YAML 1.2 via gopkg.in/yaml.v3).",
	Methods: []Method{
		{
			Name:    "parse",
			Doc:     "Decode a YAML string into a value (maps, lists, strings, numbers, bools, null); errors on invalid input.",
			Args:    []Arg{{Name: "source", Type: TypeString}},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    YAMLParse,
		},
		{
			Name:    "stringify",
			Doc:     "Encode a value to a YAML string; errors on unencodable input.",
			Args:    []Arg{{Name: "value", Type: TypeAny}},
			Returns: []Ret{{Type: TypeString}},
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
