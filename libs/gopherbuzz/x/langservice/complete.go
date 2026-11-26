package langservice

import (
	"slices"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/token"
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

// Completion is one suggestion. Label is both shown and inserted. Detail is a
// short hint such as a signature, and Doc the longer description. Replace is how
// many bytes before the cursor the item replaces: the partial word typed so far.
type Completion struct {
	Label   string         `json:"label"`
	Kind    CompletionKind `json:"kind"`
	Detail  string         `json:"detail,omitempty"`
	Doc     string         `json:"doc,omitempty"`
	Replace int            `json:"replace"`
}

// builtins are the global functions checker.registerBuiltins defines; a test
// type-checks each one so the list cannot drift from it.
var builtins = []string{"print", "str", "int", "double", "bool", "assert", "type", "zdef"}

// Complete returns the completions for the cursor at byte offset in src, sorted
// by label, or nil when there is nothing to offer. It reads the cursor's context
// from the raw text: an import path, a `module\` member, or a bare word. An
// offset outside src is clamped. A module whose source does not parse offers no
// members.
func (s *Service) Complete(src string, offset int) []Completion {
	before := src[:clampOffset(src, offset)]

	if partial, ok := importPathContext(before); ok {
		return s.importCompletions(partial)
	}
	if base, partial, ok := memberContext(before); ok {
		if name, ok := s.resolveModule(base, src); ok {
			return s.memberCompletions(name, partial)
		}
		return nil
	}
	if afterDot(before) {
		return nil // a member of a value: no host members to offer
	}
	return s.wordCompletions(src, before)
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
// with `<ident>\<partial>` (partial may be empty right after the backslash). It
// rejects chained access (a\b\c, a.b\c), which is not a module member access.
func memberContext(before string) (base, partial string, ok bool) {
	i := len(before)
	for i > 0 && isIdentByte(before[i-1]) {
		i--
	}
	partial = before[i:]
	if i == 0 || before[i-1] != '\\' {
		return "", "", false
	}
	sep := i - 1
	j := sep
	for j > 0 && isIdentByte(before[j-1]) {
		j--
	}
	base = before[j:sep]
	if base == "" {
		return "", "", false
	}
	// Reject a longer chain (a\b\) and a member off a call result. The byte
	// before the base must be neither an identifier byte nor a separator.
	if j > 0 && (isIdentByte(before[j-1]) || before[j-1] == '.' || before[j-1] == '\\') {
		return "", "", false
	}
	return base, partial, true
}

// afterDot reports whether the identifier prefix ending before follows a dot.
func afterDot(before string) bool {
	i := len(before)
	for i > 0 && isIdentByte(before[i-1]) {
		i--
	}
	return i > 0 && before[i-1] == '.'
}

func (s *Service) importCompletions(partial string) []Completion {
	var out []Completion
	for _, name := range s.names() {
		if !strings.HasPrefix(name, partial) {
			continue
		}
		mod, _ := s.module(name)
		out = append(out, Completion{Label: name, Kind: KindModule, Doc: mod.Doc, Replace: len(partial)})
	}
	sortByLabel(out)
	return out
}

func (s *Service) memberCompletions(module, partial string) []Completion {
	members, err := s.members(module)
	if err != nil {
		return nil
	}
	var out []Completion
	for _, m := range members {
		if strings.HasPrefix(m.Name, partial) {
			out = append(out, Completion{
				Label:   m.Name,
				Kind:    memberCompletionKind(m.Kind),
				Detail:  qualified(module, m),
				Doc:     m.Doc,
				Replace: len(partial),
			})
		}
	}
	sortByLabel(out)
	return out
}

func memberCompletionKind(k MemberKind) CompletionKind {
	if k == MemberValue {
		return KindField
	}
	return KindMethod
}

// qualified spells a member's signature the way a call site names it.
func qualified(module string, m Member) string {
	return moduleBase(module) + `\` + m.Signature
}

// wordCompletions offers in-file declarations, imports, builtins and keywords
// for the identifier prefix ending at the cursor. An empty prefix offers
// nothing, so a request on whitespace does not dump the whole namespace.
func (s *Service) wordCompletions(src, before string) []Completion {
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

	for _, sym := range scanSymbols(src) {
		add(sym.Name, completionKind(sym.Kind), sym.Sig, "")
	}
	for _, imp := range scanImports(src) {
		doc := ""
		if name, ok := s.lookupPath(imp.Path); ok {
			mod, _ := s.module(name)
			doc = mod.Doc
		}
		add(imp.Name, KindModule, "", doc)
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

func completionKind(k symbolKind) CompletionKind {
	switch k {
	case symFunction:
		return KindFunction
	case symType:
		return KindType
	case symConstant:
		return KindConstant
	}
	// Unreachable: scanSymbols only produces the three kinds above. A new
	// symbolKind should be classified explicitly, not silently labeled here.
	return KindConstant
}

func sortByLabel(c []Completion) {
	slices.SortFunc(c, func(a, b Completion) int { return strings.Compare(a.Label, b.Label) })
}
