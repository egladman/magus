package std

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/samber/lo"
)

//go:generate go run ../cmd/magus-utils bindings -module strings -lang buzz -out ../internal/interp/bindings/gen/strings.go

func init() { Register(Strings) }

// Strings is the "strings" host module: the string operations Buzz's builtins
// lack. Buzz strings already do upper/lower/trim/split/replace/sub, but two
// classes of work fall outside that. Codegen and naming tasks need to re-case an
// identifier (snake↔camel↔kebab↔Pascal) or split prose into words - operations
// with fiddly edge cases (acronyms, separators) that are easy to get subtly
// wrong in script; those delegate to samber/lo so the behavior matches a
// well-tested Go implementation. The rest are the primitives a magusfile reaches
// for while reading a command's output: comparing, trimming a known affix,
// splitting into lines or fields, padding a column.
//
// `compare` in particular is not a convenience. Buzz has no `<` operator on str,
// so sorting strings at all means writing a byte-comparison loop by hand -
// docs/lib/text.buzz carries one (strLess) for exactly that reason. A comparator
// list.sort can be handed is what that loop was standing in for.
//
// Pure string transforms: no filesystem or environment access.
var Strings = Module{
	Name: "strings",
	Doc:  "String helpers Buzz's builtins lack: case conversion, comparison, affix trimming, padding, and splitting into lines or fields.",
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
		{
			Name:    "upper_first",
			Doc:     "Uppercase the first rune of s, leaving the rest untouched. Unlike capitalize, which lowercases the remainder, this preserves interior casing - the form a label or breadcrumb built from an existing string needs.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsUpperFirst,
		},
		{
			Name:    "compare",
			Doc:     "Compare a and b lexicographically by byte, returning -1, 0, or 1. Buzz has no < operator on str, so this is what a list.sort comparator over strings is built from.",
			Args:    []Arg{{Name: "a", Type: TypeString}, {Name: "b", Type: TypeString}},
			Returns: []Ret{{Type: TypeInt}},
			Impl:    StringsCompare,
		},
		{
			Name:    "contains",
			Doc:     "Report whether s contains substr.",
			Args:    []Arg{{Name: "s", Type: TypeString}, {Name: "substr", Type: TypeString}},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    StringsContains,
		},
		{
			Name:    "trim_prefix",
			Doc:     "Remove prefix from the start of s if present, otherwise return s unchanged. Buzz's str.trim only strips whitespace, so there is no built-in way to drop a known affix.",
			Args:    []Arg{{Name: "s", Type: TypeString}, {Name: "prefix", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsTrimPrefix,
		},
		{
			Name:    "trim_suffix",
			Doc:     "Remove suffix from the end of s if present, otherwise return s unchanged.",
			Args:    []Arg{{Name: "s", Type: TypeString}, {Name: "suffix", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsTrimSuffix,
		},
		{
			Name: "pad_left",
			Doc:  "Left-pad s with pad until it is length runes wide; s is returned unchanged when already that wide or wider. pad defaults to a space. Zero-padding a number so it sorts lexically is the usual reason.",
			Args: []Arg{
				{Name: "s", Type: TypeString},
				{Name: "length", Type: TypeInt},
				{Name: "pad", Type: TypeString, Optional: true, Default: " "},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    StringsPadLeft,
		},
		{
			Name: "pad_right",
			Doc:  "Right-pad s with pad until it is length runes wide; s is returned unchanged when already that wide or wider. pad defaults to a space. Aligning a column of output is the usual reason.",
			Args: []Arg{
				{Name: "s", Type: TypeString},
				{Name: "length", Type: TypeInt},
				{Name: "pad", Type: TypeString, Optional: true, Default: " "},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    StringsPadRight,
		},
		{
			Name:    "lines",
			Doc:     "Split s into lines on \\n, tolerating \\r\\n endings and dropping the trailing empty element a final newline would produce. Reading a command's stdout line by line is what this is for; fs.read_lines is the same shape over a file.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    StringsLines,
		},
		{
			Name:    "fields",
			Doc:     "Split s around runs of whitespace, discarding empties. Picking a column out of a tool's version banner is what this is for; str.split(\" \") cannot, because it yields an empty element per extra space.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    StringsFields,
		},
		{
			Name:    "split_n",
			Doc:     "Split s on sep into at most n pieces, leaving any remaining separators in the final piece; n of -1 means no limit. Parsing a KEY=VALUE line whose value itself contains the separator needs this rather than str.split.",
			Args:    []Arg{{Name: "s", Type: TypeString}, {Name: "sep", Type: TypeString}, {Name: "n", Type: TypeInt}},
			Returns: []Ret{{Type: TypeStringSlice}},
			Impl:    StringsSplitN,
		},
		{
			Name:    "collapse_ws",
			Doc:     "Fold every run of whitespace in s into a single space and trim the ends, so a multi-line value reads as one clean line.",
			Args:    []Arg{{Name: "s", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    StringsCollapseWs,
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

// StringsUpperFirst uppercases the first rune of s, leaving the rest untouched.
func StringsUpperFirst(_ context.Context, s string) (string, error) {
	if s == "" {
		return s, nil
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r), nil
}

// StringsCompare compares a and b lexicographically by byte, returning -1, 0, or 1.
func StringsCompare(_ context.Context, a, b string) (int, error) {
	return strings.Compare(a, b), nil
}

// StringsContains reports whether s contains substr.
func StringsContains(_ context.Context, s, substr string) (bool, error) {
	return strings.Contains(s, substr), nil
}

// StringsTrimPrefix removes prefix from the start of s if present.
func StringsTrimPrefix(_ context.Context, s, prefix string) (string, error) {
	return strings.TrimPrefix(s, prefix), nil
}

// StringsTrimSuffix removes suffix from the end of s if present.
func StringsTrimSuffix(_ context.Context, s, suffix string) (string, error) {
	return strings.TrimSuffix(s, suffix), nil
}

// StringsPadLeft left-pads s with pad until it is length runes wide.
func StringsPadLeft(_ context.Context, s string, length int, pad string) (string, error) {
	fill, err := padding(s, length, pad)
	return fill + s, err
}

// StringsPadRight right-pads s with pad until it is length runes wide.
func StringsPadRight(_ context.Context, s string, length int, pad string) (string, error) {
	fill, err := padding(s, length, pad)
	return s + fill, err
}

// padding builds the fill that brings s up to length runes, truncated to land
// exactly on the boundary when pad is multi-rune. Width is counted in runes, not
// bytes, so padding a column of non-ASCII labels aligns as it reads. An empty pad
// is the one input with no sensible answer - repeating it can never reach length,
// so it raises rather than looping or silently returning s.
func padding(s string, length int, pad string) (string, error) {
	if pad == "" {
		return "", fmt.Errorf("strings.pad: pad must not be empty")
	}
	missing := length - len([]rune(s))
	if missing <= 0 {
		return "", nil
	}
	padRunes := []rune(pad)
	fill := make([]rune, 0, missing)
	for len(fill) < missing {
		fill = append(fill, padRunes...)
	}
	return string(fill[:missing]), nil
}

// StringsLines splits s into lines on \n, tolerating \r\n and dropping the
// trailing empty element a final newline produces.
func StringsLines(_ context.Context, s string) ([]string, error) {
	if s == "" {
		return []string{}, nil
	}
	s = strings.TrimSuffix(s, "\n")
	out := strings.Split(s, "\n")
	for i, l := range out {
		out[i] = strings.TrimSuffix(l, "\r")
	}
	return out, nil
}

// StringsFields splits s around runs of whitespace, discarding empties.
func StringsFields(_ context.Context, s string) ([]string, error) {
	f := strings.Fields(s)
	if f == nil {
		f = []string{}
	}
	return f, nil
}

// StringsSplitN splits s on sep into at most n pieces.
func StringsSplitN(_ context.Context, s, sep string, n int) ([]string, error) {
	out := strings.SplitN(s, sep, n)
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// StringsCollapseWs folds every run of whitespace into a single space and trims.
func StringsCollapseWs(_ context.Context, s string) (string, error) {
	return strings.Join(strings.Fields(s), " "), nil
}
