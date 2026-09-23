package knowledge

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The neutral declaration kinds the naming lens compares. Every language's reader maps its own
// vocabulary onto these, so a family never mixes a Go struct with a TypeScript function.
const (
	declFunction  = "function"
	declMethod    = "method"
	declType      = "type"
	declInterface = "interface"
	declStruct    = "struct"
	declValue     = "value"
	declField     = "field"
)

// namingParam is one parameter as a language reader reports it: its name, and its type as the
// declaration spells it. Either may be empty.
type namingParam struct {
	Name string
	Type string
}

// declShape is what a language reader says about one declaration.
type declShape struct {
	// Kind is one of the decl* kinds, or empty for something the lens never compares (a
	// parameter, a local, a namespace).
	Kind string
	// Visible reports whether code outside the declaring scope can name it. A reader that
	// cannot tell says true: a language with no visibility in its index is judged on what it
	// declares at the top level.
	Visible bool
	// Key is the comparable declaration shape, rendered for a reader. Empty when the language
	// reported no signature, and families then form on kind and name alone.
	Key string
	// Meaning is what the declaration binds its name to (a struct, a string, a function),
	// compared across the carriers of one word. Empty when unknown.
	Meaning string
	// Params is the parameter list of a function or method, in order.
	Params []namingParam
}

// symbolFacts is the language-neutral view of one symbol node that a reader starts from.
type symbolFacts struct {
	Kind      string // the indexer's own classifier, often empty
	Signature string
	// Path is the symbol's descriptor chain after its package or file.
	Path []descriptor
	// Kind the descriptor grammar alone implies, before any language refines it.
	Neutral string
}

// shapeReader reads one language's declarations. Only this layer knows a language: the scorer
// sees declShape and nothing else.
type shapeReader interface {
	read(s symbolFacts) declShape
}

// shapeReaders maps an index's language to its reader. A language missing here falls back to
// descriptorShapes: families by kind and name only.
var shapeReaders = map[string]shapeReader{
	"go":         goShapes{},
	"typescript": colonShapes{},
}

func readerFor(language string) shapeReader {
	if r, ok := shapeReaders[language]; ok {
		return r
	}
	return descriptorShapes{}
}

// descriptorShapes reads only the SCIP descriptor grammar, which every indexer emits. It is the
// fallback for a language whose signatures no reader parses (scip-typescript sets no kind and
// renders its declarations in a form nothing here reads).
type descriptorShapes struct{}

func (descriptorShapes) read(s symbolFacts) declShape {
	return declShape{Kind: s.Neutral, Visible: true}
}

// colonShapes reads the parameter names of a declaration rendered `name(a: T, b?: U): R`, the
// form scip-typescript uses, and leaves the rest to the descriptor grammar: its rendered types
// are not compared, so families form on kind and name as with descriptorShapes.
type colonShapes struct{}

func (colonShapes) read(s symbolFacts) declShape {
	shape := declShape{Kind: s.Neutral, Visible: true}
	if shape.Kind != declFunction && shape.Kind != declMethod {
		return shape
	}
	open := strings.IndexByte(s.Signature, '(')
	if open < 0 {
		return shape
	}
	end := matchingClose(s.Signature, open)
	if end < 0 {
		return shape
	}
	for list := s.Signature[open+1 : end]; list != ""; {
		part, rest, _ := cutTopLevel(list, ',')
		list = rest
		name, typ, _ := strings.Cut(strings.TrimSpace(part), ":")
		name = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(name), "..."), "?")
		if name != "" {
			shape.Params = append(shape.Params, namingParam{Name: name, Type: strings.TrimSpace(typ)})
		}
	}
	return shape
}

// descriptor is one step of a SCIP symbol's descriptor chain.
type descriptor struct {
	Name   string
	Suffix byte // '/', '#', '.', ':', '!', '(' for a method, 'p' for a parameter, '[' for a type parameter
}

// parseDescriptors splits the descriptor part of a symbol node ID. ok is false for anything the
// grammar does not describe; the caller skips that symbol rather than guessing at it.
func parseDescriptors(id string) ([]descriptor, bool) {
	key := strings.TrimPrefix(id, "symbol:")
	// "<manager> <package> <descriptors>": the package never contains a space.
	parts := strings.SplitN(key, " ", 3)
	rest := parts[len(parts)-1]
	var out []descriptor
	for rest != "" {
		var name string
		switch rest[0] {
		case '`':
			j := strings.IndexByte(rest[1:], '`')
			if j < 0 {
				return nil, false
			}
			name, rest = rest[1:1+j], rest[2+j:]
		case '(':
			j := strings.IndexByte(rest, ')')
			if j < 0 {
				return nil, false
			}
			out = append(out, descriptor{Name: rest[1:j], Suffix: 'p'})
			rest = rest[j+1:]
			continue
		case '[':
			j := strings.IndexByte(rest, ']')
			if j < 0 {
				return nil, false
			}
			out = append(out, descriptor{Name: rest[1:j], Suffix: '['})
			rest = rest[j+1:]
			continue
		default:
			j := strings.IndexAny(rest, "/#.:!(")
			if j <= 0 {
				return nil, false
			}
			name, rest = rest[:j], rest[j:]
		}
		if rest == "" {
			return nil, false
		}
		switch rest[0] {
		case '/', '#', '.', ':', '!':
			out = append(out, descriptor{Name: name, Suffix: rest[0]})
			rest = rest[1:]
		case '(':
			j := strings.IndexByte(rest, ')')
			if j < 0 || j+1 >= len(rest) || rest[j+1] != '.' {
				return nil, false
			}
			out = append(out, descriptor{Name: name, Suffix: '('})
			rest = rest[j+2:]
		default:
			return nil, false
		}
	}
	return out, len(out) > 0
}

// afterNamespace returns the descriptors after the last namespace one: the symbol's path
// within its package or file.
func afterNamespace(path []descriptor) []descriptor {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i].Suffix == '/' {
			return path[i+1:]
		}
	}
	return path
}

// neutralKind is the kind the descriptor grammar implies for a path within a namespace.
func neutralKind(path []descriptor) string {
	switch len(path) {
	case 1:
		switch path[0].Suffix {
		case '(':
			return declFunction
		case '#':
			return declType
		case '.':
			return declValue
		}
	case 2:
		if path[0].Suffix != '#' {
			return ""
		}
		switch path[1].Suffix {
		case '(':
			return declMethod
		case '.':
			return declField
		}
	}
	return ""
}

// goShapes reads scip-go's rendered declarations: `func (T).Name(ctx context.Context) error`,
// `type X interface { ... }`, `const X T = ...`, `struct field Name T`.
type goShapes struct{}

func (goShapes) read(s symbolFacts) declShape {
	shape := declShape{Kind: s.Neutral, Visible: goVisible(s.Path)}
	switch s.Kind {
	case "Interface":
		shape.Kind = declInterface
	case "Struct":
		shape.Kind = declStruct
	case "MethodSpecification", "Method":
		shape.Kind = declMethod
	}
	sig := strings.Join(strings.Fields(s.Signature), " ")
	switch shape.Kind {
	case declFunction, declMethod:
		params, results, ok := goFuncParts(sig)
		if !ok {
			return shape
		}
		shape.Params = params
		types := make([]string, len(params))
		for i, p := range params {
			types[i] = goTypeClass(p.Type)
		}
		shape.Key = "func(" + strings.Join(types, ", ") + ") " + goResultClass(results)
		shape.Meaning = "func"
	case declInterface:
		shape.Key, shape.Meaning = "interface", "interface"
	case declStruct:
		shape.Key, shape.Meaning = "struct", "struct"
	case declType:
		rest, ok := strings.CutPrefix(sig, "type ")
		if !ok {
			return shape
		}
		_, under, _ := cutTopLevel(rest, ' ')
		if strings.HasPrefix(under, "= ") {
			shape.Key, shape.Meaning = "alias", "alias"
			return shape
		}
		shape.Meaning = goTypeClass(under)
		shape.Key = "type " + shape.Meaning
	case declValue:
		word, rest, ok := strings.Cut(sig, " ")
		if !ok || (word != "const" && word != "var") {
			return shape
		}
		_, typ, _ := cutTopLevel(rest, ' ')
		typ, _, _ = strings.Cut(typ, " = ")
		typ = strings.TrimPrefix(strings.TrimSpace(typ), "untyped ")
		if strings.HasPrefix(typ, "=") {
			typ = ""
		}
		shape.Meaning = goTypeClass(typ)
		shape.Key = strings.TrimSpace(word + " " + shape.Meaning)
	case declField:
		rest, ok := strings.CutPrefix(sig, "struct field ")
		if !ok {
			return shape
		}
		_, typ, _ := cutTopLevel(rest, ' ')
		shape.Meaning = goTypeClass(typ)
		shape.Key = "field " + shape.Meaning
	}
	return shape
}

// goVisible reports whether every name on the path is exported, so a method on an unexported
// type is not visible even though its own name is capitalized.
func goVisible(path []descriptor) bool {
	if len(path) == 0 {
		return false
	}
	for _, d := range path {
		r, _ := utf8.DecodeRuneInString(d.Name)
		if !unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

// goFuncParts splits a rendered func declaration into its parameters and its result text.
func goFuncParts(sig string) ([]namingParam, string, bool) {
	rest, ok := strings.CutPrefix(sig, "func ")
	if !ok {
		return nil, "", false
	}
	if strings.HasPrefix(rest, "(") {
		// The receiver, then `.Name` (interface and method renderings) or ` Name`.
		end := matchingClose(rest, 0)
		if end < 0 {
			return nil, "", false
		}
		rest = strings.TrimLeft(rest[end+1:], ". ")
	}
	open := strings.IndexAny(rest, "([")
	if open < 0 {
		return nil, "", false
	}
	if rest[open] == '[' {
		end := matchingClose(rest, open)
		if end < 0 || end+1 >= len(rest) || rest[end+1] != '(' {
			return nil, "", false
		}
		open = end + 1
	}
	end := matchingClose(rest, open)
	if end < 0 {
		return nil, "", false
	}
	return goParams(rest[open+1 : end]), strings.TrimSpace(rest[end+1:]), true
}

// goParams reads a Go parameter list, giving each name of a grouped list (`root, rev string`)
// the type that closes the group.
func goParams(list string) []namingParam {
	var parts []string
	for list != "" {
		part, rest, _ := cutTopLevel(list, ',')
		parts = append(parts, strings.TrimSpace(part))
		list = rest
	}
	// Go names every parameter or none, so one named part settles the list.
	named := slices.ContainsFunc(parts, func(p string) bool {
		_, _, ok := goNamed(p)
		return ok
	})
	out := make([]namingParam, len(parts))
	for i, p := range parts {
		if !named {
			out[i] = namingParam{Type: p}
			continue
		}
		name, typ, _ := goNamed(p)
		out[i] = namingParam{Name: name, Type: typ}
	}
	for i := len(out) - 2; i >= 0; i-- {
		if out[i].Type == "" {
			out[i].Type = out[i+1].Type
		}
	}
	return out
}

// goQualifier matches a package qualifier on an exported type name.
var goQualifier = regexp.MustCompile(`\b[a-z][A-Za-z0-9_]*\.([A-Z])`)

// goTypeClass normalizes a type for comparison: the package path goes, a context and a function
// type collapse to one word each, since the name of either says nothing about the shape.
func goTypeClass(typ string) string {
	typ = strings.TrimSpace(typ)
	switch {
	case typ == "":
		return ""
	case typ == "context.Context":
		return "ctx"
	case strings.HasPrefix(typ, "func(") || strings.HasPrefix(typ, "func "), typ == "context.CancelFunc":
		return "func"
	case strings.HasPrefix(typ, "struct{") || strings.HasPrefix(typ, "struct "):
		return "struct"
	case strings.HasPrefix(typ, "interface{") || strings.HasPrefix(typ, "interface "):
		return "interface"
	case !strings.Contains(typ, "."):
		return typ
	}
	return goQualifier.ReplaceAllString(typ, "$1")
}

// goResultClass collapses a result list to the categories a caller's code branches on. The
// value's own type is dropped: `LeaseFromContext` and `RootFromContext` return different types
// and are one idiom.
func goResultClass(results string) string {
	if results == "" {
		return "none"
	}
	if strings.HasPrefix(results, "(") {
		if end := matchingClose(results, 0); end == len(results)-1 {
			results = results[1:end]
		}
	}
	var classes []string
	for results != "" {
		part, rest, _ := cutTopLevel(results, ',')
		part = strings.TrimSpace(part)
		if _, typ, ok := goNamed(part); ok {
			part = typ
		}
		switch c := goTypeClass(part); c {
		case "error", "bool", "ctx", "func":
			classes = append(classes, c)
		default:
			classes = append(classes, "value")
		}
		results = rest
	}
	if len(classes) > 2 {
		return "values"
	}
	return strings.Join(classes, ", ")
}

// goNamed splits a parameter or result declared with a name (`mapped bool`, `ch <-chan int`)
// into its name and type. ok is false for a bare type, including one that holds a top-level
// space itself (`chan<- Event`, `<-chan int`, `chan int`), and name is then the whole part.
func goNamed(part string) (name, typ string, ok bool) {
	before, after, found := cutTopLevel(part, ' ')
	if !found || before == "" || strings.IndexFunc(before, func(r rune) bool { return !isIdentRune(r) || r == '$' }) >= 0 {
		return part, "", false
	}
	switch before {
	case "chan", "func", "map", "struct", "interface":
		return part, "", false
	}
	return before, strings.TrimSpace(after), true
}

// cutTopLevel cuts s around the first sep outside brackets, braces and parentheses.
func cutTopLevel(s string, sep byte) (before, after string, found bool) {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case sep:
			if depth == 0 {
				return s[:i], s[i+1:], true
			}
		}
	}
	return s, "", false
}

// matchingClose returns the index of the bracket closing the one at open, or -1.
func matchingClose(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
