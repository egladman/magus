// Package yaml is the "yaml" host module. See std/encoding/register.go for
// why this and its eight siblings each get their own leaf package instead of
// living in std's flat root, and how this directory's Module reaches the rest
// of magus without std importing back down to collect it.
package yaml

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
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
			Name: "positions",
			Doc: "Report where each value of a YAML document starts, as {lines, columns}: 1-based, each keyed by " +
				"the value's JSON pointer (\"\" for the root, /jobs/build/steps/0 for a list item, ~0 and ~1 escaping " +
				"~ and / in a key). A mapping entry is keyed at its value, so /jobs/build is where that job's body " +
				"starts. An alias is keyed where it appears and not followed. What parse returns carries no positions; " +
				"this is how a check over it points at a line. Errors on invalid input.",
			Args:    []std.Arg{{Name: "source", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeAnyMap, Object: "YamlPositions"}},
			Raises:  true,
			Impl:    Positions,
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

// Positions reports the position of every value in source's first document. An empty
// document has only the root, at line 0.
func Positions(_ context.Context, source string) (types.YAMLPositions, error) {
	out := types.YAMLPositions{Lines: map[string]int{}, Columns: map[string]int{}}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(source), &doc); err != nil {
		return out, fmt.Errorf("yaml.positions: %w", err)
	}
	root := &doc
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		root = doc.Content[0]
	}
	var walk func(n *yaml.Node, pointer string)
	walk = func(n *yaml.Node, pointer string) {
		out.Lines[pointer] = n.Line
		out.Columns[pointer] = n.Column
		switch n.Kind {
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1], pointer+"/"+pointerEscaper.Replace(n.Content[i].Value))
			}
		case yaml.SequenceNode:
			for i, c := range n.Content {
				walk(c, pointer+"/"+strconv.Itoa(i))
			}
		}
	}
	walk(root, "")
	return out, nil
}

// pointerEscaper escapes a key as RFC 6901 requires: ~ before /, so an escaped / is not
// read back as a ~.
var pointerEscaper = strings.NewReplacer("~", "~0", "/", "~1")

// YAMLStringify encodes value to a YAML string.
func YAMLStringify(_ context.Context, value any) (string, error) {
	b, err := yaml.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("yaml.stringify: %w", err)
	}
	return string(b), nil
}
