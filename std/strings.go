package std

import (
	"context"

	"github.com/samber/lo"
)

//go:generate go run ../cmd/magus-bindings-gen -module strings -lang buzz -out ../hostbuzz/gen/strings.go

func init() { Register(Strings) }

// Strings is the "strings" host module: case-conversion and word helpers Buzz's
// builtins lack. Buzz strings already do upper/lower/trim/split/replace/sub, but
// codegen and naming tasks constantly need to re-case an identifier
// (snake↔camel↔kebab↔Pascal) or split prose into words — operations with fiddly
// edge cases (acronyms, separators) that are easy to get subtly wrong in script.
// These delegate to samber/lo so the behavior matches a well-tested Go
// implementation. Pure string transforms: no filesystem or environment access.
var Strings = Module{
	Name: "strings",
	Doc:  "Case conversion and word helpers (camel/snake/kebab/Pascal, capitalize, words, ellipsis).",
	Methods: []Method{
		{
			Name:    "camel_case",
			Doc:     "Convert s to camelCase.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsCamelCase,
		},
		{
			Name:    "snake_case",
			Doc:     "Convert s to snake_case.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsSnakeCase,
		},
		{
			Name:    "kebab_case",
			Doc:     "Convert s to kebab-case.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsKebabCase,
		},
		{
			Name:    "pascal_case",
			Doc:     "Convert s to PascalCase.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsPascalCase,
		},
		{
			Name:    "capitalize",
			Doc:     "Uppercase the first rune of s and lowercase the rest.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsCapitalize,
		},
		{
			Name:    "words",
			Doc:     "Split s into its constituent words (splitting on case changes, digits, and separators).",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    StringsWords,
		},
		{
			Name:    "ellipsis",
			Doc:     "Trim s to at most length runes, appending \"...\" when truncated.",
			Args:    []Arg{{Name: "s", Type: TypeString}, {Name: "length", Type: TypeInt}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsEllipsis,
		},
	},
}

// StringsCamelCase converts s to camelCase.
func StringsCamelCase(_ context.Context, s string) (string, error) {
	return lo.CamelCase(s), nil
}

// StringsSnakeCase converts s to snake_case.
func StringsSnakeCase(_ context.Context, s string) (string, error) {
	return lo.SnakeCase(s), nil
}

// StringsKebabCase converts s to kebab-case.
func StringsKebabCase(_ context.Context, s string) (string, error) {
	return lo.KebabCase(s), nil
}

// StringsPascalCase converts s to PascalCase.
func StringsPascalCase(_ context.Context, s string) (string, error) {
	return lo.PascalCase(s), nil
}

// StringsCapitalize uppercases the first rune of s and lowercases the rest.
func StringsCapitalize(_ context.Context, s string) (string, error) {
	return lo.Capitalize(s), nil
}

// StringsWords splits s into its constituent words.
func StringsWords(_ context.Context, s string) ([]string, error) {
	return lo.Words(s), nil
}

// StringsEllipsis trims s to at most length runes, appending "..." when truncated.
func StringsEllipsis(_ context.Context, s string, length int) (string, error) {
	return lo.Ellipsis(s, length), nil
}
