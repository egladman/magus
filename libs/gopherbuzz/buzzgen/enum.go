package buzzgen

import (
	"fmt"
	"go/constant"
	"go/types"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/gopherbuzz/token"
)

// Enum is a Go named string type mirrored as a Buzz enum<str>.
type Enum struct {
	// Name is the Buzz name.
	Name   string
	GoType *types.Named
	// Cases are in declaration order.
	Cases []EnumCase
}

// EnumCase is one case, derived from one Go constant.
type EnumCase struct {
	// Name is the Buzz identifier.
	Name string
	// Value is the string the case carries.
	Value string
	// Const is the Go constant it came from.
	Const string
}

// EnumOf derives an enum from the exported constants of named's exact
// type in its own package, layered with ref's settings. A case is named
// for its value (executable-bit is executableBit), else for its constant
// less the leading words it shares with the type. A cases setting
// overrides either. An empty, reserved or repeated name is an error.
func EnumOf(named *types.Named, ref TypeRef) (Enum, error) {
	basic, ok := named.Underlying().(*types.Basic)
	if !ok || basic.Kind() != types.String {
		return Enum{}, fmt.Errorf("buzzgen: %s is not a named string type", named)
	}
	consts := declaredConstants(named)
	if len(consts) == 0 {
		return Enum{}, fmt.Errorf("buzzgen: %s declares no constants to derive cases from", named)
	}
	typeWords := camelWords(named.Obj().Name())
	enum := Enum{Name: ref.BuzzName(), GoType: named}
	byName := map[string]string{}
	add := func(c EnumCase, from string) error {
		switch {
		case c.Name == "":
			return fmt.Errorf("buzzgen: %s: %s leaves no case name; name it under cases", named, from)
		case token.IsKeyword(c.Name):
			return fmt.Errorf("buzzgen: %s: %s becomes case %s, a Buzz keyword; name it under cases", named, from, c.Name)
		}
		if prior, dup := byName[c.Name]; dup {
			return fmt.Errorf("buzzgen: %s: %s and %s both become case %s", named, prior, from, c.Name)
		}
		byName[c.Name] = from
		enum.Cases = append(enum.Cases, c)
		return nil
	}
	zero := EnumCase{Name: ref.ZeroCase()}
	for _, c := range consts {
		if constant.StringVal(c.Val()) == "" {
			zero.Const = c.Name()
		}
	}
	if zero.Name != "" {
		if err := add(zero, `the zero value ""`); err != nil {
			return Enum{}, err
		}
	}
	for _, c := range consts {
		value := constant.StringVal(c.Val())
		if value == "" {
			continue
		}
		name, set := ref.Settings.Cases[value]
		if !set {
			name = caseName(value, camelWords(c.Name()), typeWords)
		}
		if err := add(EnumCase{Name: name, Value: value, Const: c.Name()}, "constant "+c.Name()); err != nil {
			return Enum{}, err
		}
	}
	for value := range ref.Settings.Cases {
		if !slices.ContainsFunc(enum.Cases, func(c EnumCase) bool { return c.Value == value }) {
			return Enum{}, fmt.Errorf("buzzgen: %s: cases names value %q, which no constant declares", named, value)
		}
	}
	return enum, nil
}

// declaredConstants are the exported constants of exactly named's type in
// its package, in source order. Scope order is alphabetical, so they are
// sorted by position.
func declaredConstants(named *types.Named) []*types.Const {
	scope := named.Obj().Pkg().Scope()
	var consts []*types.Const
	for _, name := range scope.Names() {
		c, ok := scope.Lookup(name).(*types.Const)
		if ok && c.Exported() && types.Identical(c.Type(), named) {
			consts = append(consts, c)
		}
	}
	slices.SortFunc(consts, func(a, b *types.Const) int { return int(a.Pos() - b.Pos()) })
	return consts
}

// CaseName is the case name buzzgen derives for value, declared as
// constant constName of type typeName, before any cases setting.
func CaseName(value, constName, typeName string) string {
	return caseName(value, camelWords(constName), camelWords(typeName))
}

// caseName is the value in lower camel case when the value is a word or
// a dashed or underscored run of them. Otherwise it is the constant's
// name less the leading words it shares with the type name.
func caseName(value string, constWords, typeWords []string) string {
	if name, ok := valueName(value); ok {
		return name
	}
	shared := 0
	for shared < len(constWords) && shared < len(typeWords) && constWords[shared] == typeWords[shared] {
		shared++
	}
	rest := constWords[shared:]
	if len(rest) == 0 {
		return ""
	}
	return strings.ToLower(rest[0]) + strings.Join(rest[1:], "")
}

// valueName spells executable-bit as executableBit and Bisector as
// bisector.
func valueName(value string) (string, bool) {
	if value == "" || strings.Trim(value, "-_") != value {
		return "", false
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		for j, r := range p {
			if !unicode.IsLetter(r) && (j == 0 || !unicode.IsDigit(r)) {
				return "", false
			}
		}
		if i > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	name := strings.Join(parts, "")
	return strings.ToLower(name[:1]) + name[1:], true
}

// camelWords splits a Go identifier at each CamelCase word boundary. A
// run of capitals is one word (HTTP in HTTPServer), and digits stay with
// the word before them (AMD64).
func camelWords(ident string) []string {
	runes := []rune(ident)
	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		prev, cur := runes[i-1], runes[i]
		next := rune(0)
		if i+1 < len(runes) {
			next = runes[i+1]
		}
		lowerToUpper := !unicode.IsUpper(prev) && unicode.IsUpper(cur)
		acronymEnd := unicode.IsUpper(prev) && unicode.IsUpper(cur) && unicode.IsLower(next)
		if lowerToUpper || acronymEnd {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	return append(words, string(runes[start:]))
}
