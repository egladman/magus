package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/json"
)

//go:generate go run ../cmd/magus-utils bindings -module merge -lang buzz -out ../internal/interp/bindings/gen/merge.go

func init() { Register(Merge) }

// Merge is the "merge" host module: combine two values.
//
// Buzz already merges map literals with `+`, one level, right-hand keys
// winning. That does not help a value that arrived as data, such as a
// document json\parse just returned, and it does not recurse, so a fragment that
// patches one nested key replaces the whole object and drops the sibling
// keys a person added. shallow is `+` for those values. deep is the form
// that walks objects.
//
// Arrays are replaced, not concatenated and not merged by index. A managed
// list in a host config is already the whole list, with the person's own
// entries kept in place by whoever built the overlay; appending would
// duplicate those entries, and pairing by index would join unrelated ones.
//
// json exists because a Buzz number cannot promise the digits of a JSON
// integer past 2^53: the language's ints are 48-bit and a parsed number is
// a float. Merging the text in Go, where the integer stays a number token,
// is what keeps the spelling.
var Merge = Module{
	Name: "merge",
	WASM: true,
	Doc:  "Combine two values. shallow replaces one level of an object; deep recurses into objects.",
	Methods: []Method{
		{
			Name: "shallow",
			Doc:  "Return a new value with overlay applied one level deep. When both are objects, overlay's keys replace base's and nested objects are replaced whole, so a sibling key inside a nested object is dropped. Any other overlay replaces base entirely. Neither input is modified.",
			Args: []Arg{
				{Name: "base", Type: TypeAny},
				{Name: "overlay", Type: TypeAny},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    MergeShallow,
		},
		{
			Name: "deep",
			Doc:  "Return a new value with overlay applied through every nested object. Objects merge key by key; an array, a scalar, or null in overlay replaces the base value at that key, because a managed list is already the whole list. Neither input is modified.",
			Args: []Arg{
				{Name: "base", Type: TypeAny},
				{Name: "overlay", Type: TypeAny},
			},
			Returns: []Ret{{Type: TypeAny}},
			Impl:    MergeDeep,
		},
		{
			Name: "json",
			Doc:  "Deep-merge two JSON documents and return the indented text, with a trailing newline. Integers keep the digits that were in the text, which merge\\deep cannot promise once a number has been a Buzz value. Objects merge key by key; arrays and other values are replaced by overlay. Object keys are emitted sorted, so the result does not keep base's key order.",
			Args: []Arg{
				{Name: "base", Type: TypeString},
				{Name: "overlay", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    MergeJSON,
		},
	},
}

// MergeShallow applies overlay one object level deep and returns the result.
func MergeShallow(_ context.Context, base, overlay any) (any, error) {
	return mergeValues(base, overlay, false), nil
}

// MergeDeep applies overlay through every nested object and returns the result.
func MergeDeep(_ context.Context, base, overlay any) (any, error) {
	return mergeValues(base, overlay, true), nil
}

// MergeJSON deep-merges overlay JSON into base JSON. Numbers stay the tokens
// from the text, so an integer past 2^53 is still that integer in the result.
func MergeJSON(_ context.Context, base, overlay string) (string, error) {
	var baseVal, overlayVal any
	if err := json.UnmarshalLossless([]byte(base), &baseVal); err != nil {
		return "", fmt.Errorf("merge.json: base: %w", err)
	}
	if err := json.UnmarshalLossless([]byte(overlay), &overlayVal); err != nil {
		return "", fmt.Errorf("merge.json: overlay: %w", err)
	}
	encoded, err := json.MarshalIndent(mergeValues(baseVal, overlayVal, true), "", "  ")
	if err != nil {
		return "", fmt.Errorf("merge.json: %w", err)
	}
	return string(encoded) + "\n", nil
}

// mergeValues applies overlay onto base. Objects combine; everything else is
// the overlay. deep recurses into a key both sides hold as an object.
//
// The result is a new map. Replaced values are overlay's own values, and a
// key only base has keeps base's value, so this does not clone the world:
// it only copies the object layers it combines. Callers that need to keep
// mutating an input do so on the input, not on the result's shared leaves.
func mergeValues(base, overlay any, deep bool) any {
	baseMap, baseOK := base.(map[string]any)
	overMap, overOK := overlay.(map[string]any)
	if !baseOK || !overOK {
		return overlay
	}
	out := make(map[string]any, len(baseMap)+len(overMap))
	for key, value := range baseMap {
		out[key] = value
	}
	for key, value := range overMap {
		if deep {
			if have, ok := out[key]; ok {
				out[key] = mergeValues(have, value, true)
				continue
			}
		}
		out[key] = value
	}
	return out
}
