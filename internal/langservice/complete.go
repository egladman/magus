package langservice

import (
	"sort"
	"strings"

	"github.com/egladman/gopherbuzz/token"
)

// CompletionKind labels a completion item so the editor can pick an icon.
type CompletionKind string

const (
	KindModule   CompletionKind = "module"
	KindMethod   CompletionKind = "method"
	KindField    CompletionKind = "field"
	KindFunction CompletionKind = "function"
	KindConstant CompletionKind = "constant"
	KindType     CompletionKind = "type"
	KindKeyword  CompletionKind = "keyword"
)

// Completion is one suggestion. Label is what the user sees and, unless Insert is
// set, what is inserted; Detail is a short right-aligned hint (a signature or type);
// Doc is the longer description shown in the item's info panel. Replace is the
// number of characters immediately before the cursor the item replaces (the length
// of the partial word being completed), so the editor can compute the edit range.
type Completion struct {
	Label   string         `json:"label"`
	Kind    CompletionKind `json:"kind"`
	Detail  string         `json:"detail,omitempty"`
	Doc     string         `json:"doc,omitempty"`
	Replace int            `json:"replace"`
}

// builtins are the always-in-scope global functions the checker pre-defines
// (see gopherbuzz checker.registerBuiltins); offered as plain word completions.
var builtins = []string{
	"print", "str", "int", "double", "bool", "len", "keys",
	"values", "append", "range", "error", "assert", "type",
}

// Complete returns the completions for the cursor at offset in src. It classifies
// the cursor context from the raw text - inside an import path, after a `module.`
// member access, or on a bare word - so it stays useful on the half-typed source a
// live editor calls it with. offset is a byte offset; out-of-range offsets are
// clamped. Results are sorted and each carries Replace, the length of the partial
// token it completes.
func Complete(src string, offset int) []Completion {
	if offset < 0 {
		offset = 0
	}
	if offset > len(src) {
		offset = len(src)
	}
	before := src[:offset]

	// Import-path context: the cursor sits inside an `import "..."` string. Offer
	// module paths, not identifiers.
	if partial, ok := importPathContext(before); ok {
		return importCompletions(partial)
	}

	// Member context: `<ident>.<partial>` where ident resolves to a module. Offer
	// that module's methods and fields.
	if base, partial, ok := memberContext(before); ok {
		if mod, ok := resolveModule(base, src); ok {
			return memberCompletions(mod, partial)
		}
		return nil // a `.` after a non-module: no host members to offer
	}

	// Word context: complete a bare identifier prefix against keywords, modules,
	// builtins, and the file's own top-level declarations.
	return wordCompletions(src, before)
}

// importPathContext reports whether before ends inside an unterminated import
// string, returning the path fragment typed so far.
func importPathContext(before string) (partial string, ok bool) {
	line := before[lineStart(before):]
	trimmed := strings.TrimLeft(line, " \t")
	if !strings.HasPrefix(trimmed, "import") {
		return "", false
	}
	rest := trimmed[len("import"):]
	q := strings.IndexByte(rest, '"')
	if q < 0 {
		return "", false
	}
	after := rest[q+1:]
	if strings.Contains(after, `"`) {
		return "", false // the string is already closed before the cursor
	}
	return after, true
}

// memberContext returns the base identifier and partial member when before ends
// with `<ident>.<partial>` (partial may be empty right after the dot). It rejects
// chained access (a.b.c) and numeric/`.`-prefixed forms, which are not module
// member accesses.
func memberContext(before string) (base, partial string, ok bool) {
	i := len(before)
	for i > 0 && isIdentByte(before[i-1]) {
		i--
	}
	partial = before[i:]
	if i == 0 || before[i-1] != '.' {
		return "", "", false
	}
	dot := i - 1
	j := dot
	for j > 0 && isIdentByte(before[j-1]) {
		j--
	}
	base = before[j:dot]
	if base == "" {
		return "", "", false
	}
	// Reject a longer chain (a.b.) or a member off a call result: the char before
	// the base must not itself be an identifier byte or a dot.
	if j > 0 && (isIdentByte(before[j-1]) || before[j-1] == '.') {
		return "", "", false
	}
	return base, partial, true
}

func importCompletions(partial string) []Completion {
	out := make([]Completion, 0, len(modules))
	for _, m := range modules {
		if strings.HasPrefix(m.Name, partial) {
			out = append(out, Completion{
				Label:   m.Name,
				Kind:    KindModule,
				Doc:     m.Doc,
				Replace: len(partial),
			})
		}
	}
	sortByLabel(out)
	return out
}

func memberCompletions(mod Module, partial string) []Completion {
	var out []Completion
	for _, f := range mod.Fields {
		if strings.HasPrefix(f.Name, partial) {
			out = append(out, Completion{
				Label: f.Name, Kind: KindField, Detail: f.Type, Doc: f.Doc, Replace: len(partial),
			})
		}
	}
	for _, m := range mod.Methods {
		if strings.HasPrefix(m.Name, partial) {
			out = append(out, Completion{
				Label: m.Name, Kind: KindMethod, Detail: m.Sig, Doc: m.Doc, Replace: len(partial),
			})
		}
	}
	sortByLabel(out)
	return out
}

// wordCompletions offers keyword, module, builtin, and in-file declaration names
// for the identifier prefix ending at the cursor. An empty prefix returns nothing,
// so an explicit completion request on whitespace does not dump the whole namespace.
func wordCompletions(src, before string) []Completion {
	i := len(before)
	for i > 0 && isIdentByte(before[i-1]) {
		i--
	}
	prefix := before[i:]
	if prefix == "" {
		return nil
	}
	replace := len(prefix)

	seen := map[string]bool{}
	var out []Completion
	add := func(label string, kind CompletionKind, detail, doc string) {
		if label == "" || seen[label] || !strings.HasPrefix(label, prefix) {
			return
		}
		seen[label] = true
		out = append(out, Completion{Label: label, Kind: kind, Detail: detail, Doc: doc, Replace: replace})
	}

	// The file's own top-level declarations first (most relevant), then imported
	// module names, then builtins and keywords.
	for _, s := range scanSymbols(src) {
		add(s.Name, completionKindFor(s.Kind), s.Sig, "")
	}
	for _, imp := range scanImports(src) {
		if m, ok := LookupModule(moduleBase(imp.Path)); ok {
			add(imp.Name, KindModule, "", m.Doc)
		} else {
			add(imp.Name, KindModule, "", "")
		}
	}
	for _, b := range builtins {
		add(b, KindFunction, "", "")
	}
	for _, kw := range token.Keywords() {
		add(kw, KindKeyword, "", "")
	}
	sortByLabel(out)
	return out
}

func completionKindFor(k symbolKind) CompletionKind {
	switch k {
	case symFunction:
		return KindFunction
	case symType:
		return KindType
	default:
		return KindConstant
	}
}

// lineStart returns the index just past the last newline in s (0 if none), i.e.
// the start of the line the end of s sits on.
func lineStart(s string) int {
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

func sortByLabel(c []Completion) {
	sort.Slice(c, func(i, j int) bool { return c[i].Label < c[j].Label })
}
