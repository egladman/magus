package edit

import (
	"cmp"
	"fmt"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/egladman/magus/internal/hint"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	buzztoken "github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/types"
)

// ResolveRenameSymbol picks the symbol a rename acts on from the graph's matches for ref. An
// exact node ID wins. A bare name must be the label of exactly one symbol the workspace
// defines: two would rename whichever the graph ranked first, and a dependency's symbol has
// no definition here to rename.
func ResolveRenameSymbol(ref string, matches []types.KnowledgeMatch, defined func(id string) bool) (string, error) {
	var named []string
	for _, m := range matches {
		if m.Kind != types.KindSymbol {
			continue
		}
		if m.ID == ref {
			if !defined(m.ID) {
				return "", fmt.Errorf("%s is not defined in this workspace, so there is no definition to rename", ref)
			}
			return m.ID, nil
		}
		if m.Label == ref && defined(m.ID) {
			named = append(named, m.ID)
		}
	}
	switch len(named) {
	case 0:
		return "", fmt.Errorf("no symbol defined in this workspace is named %q; `%s` lists the one it resolves to, and its id renames it", ref, hint.Refs.With(ref))
	case 1:
		return named[0], nil
	}
	slices.Sort(named)
	return "", fmt.Errorf("%q names %d symbols defined in this workspace; pass the id of one:\n  %s", ref, len(named), strings.Join(named, "\n  "))
}

// RenameSites derives the sites that rename from to to from a symbol's verified
// occurrences. It refuses rather than derives when the list cannot be trusted to be
// complete and exact: a stale file may be missing sites no check can see, an unverified
// site points at text that moved, and a site holding another spelling (a package's import
// path) is not one a name substitution can rewrite.
func RenameSites(files []types.SymbolOccurrenceFile, from, to string) ([]Site, []types.EditRefusal) {
	refused := identifierRefusals(to, files)
	if to == from {
		refused = append(refused, types.EditRefusal{Reason: fmt.Sprintf("the symbol is already named %q", to)})
	}
	var sites []Site
	for _, f := range files {
		if f.Stale {
			refused = append(refused, types.EditRefusal{Path: f.File, Reason: fmt.Sprintf(
				"changed after it was indexed, so it may hold sites the index never saw; refresh with `%s`", hint.GraphBuild)})
			continue
		}
		for _, occ := range f.Occurrences {
			at := types.EditRefusal{Path: f.File, Line: occ.Line, Column: occ.Column}
			switch {
			case occ.Status != types.SymbolOccurrenceVerified:
				at.Reason = fmt.Sprintf("the site is %s, not verified; refresh with `%s`", occ.Status, hint.GraphBuild)
			case occ.Text != from:
				at.Reason = fmt.Sprintf("the site spells the symbol %q, which renaming %q does not rewrite; edit it by hand", occ.Text, from)
			}
			if at.Reason != "" {
				refused = append(refused, at)
				continue
			}
			sites = append(sites, Site{
				Path:  f.File,
				Start: types.EditPosition{Line: occ.Line, Column: occ.Column},
				End:   types.EditPosition{Line: occ.EndLine, Column: occ.EndColumn},
				Old:   from,
				New:   to,
			})
		}
	}
	if len(sites) == 0 && len(refused) == 0 {
		refused = append(refused, types.EditRefusal{Reason: "the index records no occurrence of the symbol"})
	}
	return sites, refused
}

// RenameCollisions refuses a new name the workspace already defines where the rename writes
// it: in a file a site rewrites or, for Go, in a package one does, where the rename would
// redeclare or shadow that symbol. definedIn lists the files that define a symbol id.
func RenameCollisions(to string, matches []types.KnowledgeMatch, definedIn func(id string) []string, sites []Site) []types.EditRefusal {
	files, goPackages := map[string]bool{}, map[string]bool{}
	for _, s := range sites {
		file := path.Clean(s.Path)
		files[file] = true
		if fileLanguage(file) == langGo {
			goPackages[path.Dir(file)] = true
		}
	}
	var refused []types.EditRefusal
	for _, m := range matches {
		if m.Kind != types.KindSymbol || m.Label != to {
			continue
		}
		for _, file := range definedIn(m.ID) {
			file = path.Clean(file)
			if files[file] || (fileLanguage(file) == langGo && goPackages[path.Dir(file)]) {
				refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("already defines %q (%s), which the new name would collide with", to, m.ID)})
			}
		}
	}
	return refused
}

// The languages the symbol indexes cover, by the name a refusal prints.
const (
	langBuzz       = "Buzz"
	langGo         = "Go"
	langPython     = "Python"
	langRust       = "Rust"
	langTypeScript = "TypeScript"
)

func fileLanguage(file string) string {
	switch path.Ext(file) {
	case ".buzz":
		return langBuzz
	case ".go":
		return langGo
	case ".py", ".pyi":
		return langPython
	case ".rs":
		return langRust
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return langTypeScript
	}
	return ""
}

var (
	// identifierRe is the identifier shape every language here accepts; TypeScript also
	// admits `$`. Decimal digits only, since Go rejects the other numeric categories.
	identifierRe   = regexp.MustCompile(`^[\p{L}_][\p{L}\p{Nd}_]*$`)
	tsIdentifierRe = regexp.MustCompile(`^[\p{L}_$][\p{L}\p{Nd}_$]*$`)

	// keywords are the reserved words a declaration cannot take. Go's come from go/token.
	keywords = map[string][]string{
		langPython: {"False", "None", "True", "and", "as", "assert", "async", "await", "break", "class", "continue",
			"def", "del", "elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in", "is",
			"lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while", "with", "yield"},
		langRust: {"Self", "abstract", "as", "async", "await", "become", "box", "break", "const", "continue", "crate",
			"do", "dyn", "else", "enum", "extern", "false", "final", "fn", "for", "gen", "if", "impl", "in", "let",
			"loop", "macro", "match", "mod", "move", "mut", "override", "priv", "pub", "ref", "return", "self",
			"static", "struct", "super", "trait", "true", "try", "type", "typeof", "unsafe", "unsized", "use",
			"virtual", "where", "while", "yield"},
		langTypeScript: {"await", "break", "case", "catch", "class", "const", "continue", "debugger", "default",
			"delete", "do", "else", "enum", "export", "extends", "false", "finally", "for", "function", "if",
			"implements", "import", "in", "instanceof", "interface", "let", "new", "null", "package", "private",
			"protected", "public", "return", "static", "super", "switch", "this", "throw", "true", "try", "typeof",
			"var", "void", "while", "with", "yield"},
	}
)

// identifierRefusals checks to against the language of every file the rename writes, since
// a name valid in one (`$x` in TypeScript) is a syntax error in another. A file of no
// known language is held to the shape every language shares and none of their keywords.
func identifierRefusals(to string, files []types.SymbolOccurrenceFile) []types.EditRefusal {
	langs := map[string]bool{}
	for _, f := range files {
		langs[fileLanguage(f.File)] = true
	}
	if len(langs) == 0 {
		langs[""] = true
	}
	var refused []types.EditRefusal
	for _, lang := range slices.Sorted(maps.Keys(langs)) {
		if reason := notIdentifier(lang, to); reason != "" {
			refused = append(refused, types.EditRefusal{Reason: reason})
		}
	}
	return refused
}

// notIdentifier says why name cannot be declared in lang, or "". A lone `_` discards in Go
// and Rust, so every site renamed to it stops naming anything.
func notIdentifier(lang, name string) string {
	switch lang {
	case langGo:
		switch {
		case token.IsKeyword(name):
			return fmt.Sprintf("%q is a Go keyword", name)
		case !token.IsIdentifier(name) || name == "_":
			return fmt.Sprintf("%q is not a Go identifier", name)
		}
		return ""
	case langBuzz:
		// Lexed rather than matched, so the shape is the one gopherbuzz reads: a name that
		// lexes as one ordinary identifier, or as one keyword, which the reason below names.
		toks, err := buzztoken.Tokenize(name)
		if err != nil || len(toks) != 2 || toks[0].Val != name || toks[0].Raw ||
			(toks[0].Kind != buzztoken.Ident && !buzztoken.IsKeyword(name)) {
			return fmt.Sprintf("%q is not a Buzz identifier", name)
		}
		return buzzReservedReason(name)
	case "":
		if !identifierRe.MatchString(name) || name == "_" {
			return fmt.Sprintf("%q is not an identifier", name)
		}
		if token.IsKeyword(name) {
			return fmt.Sprintf("%q is a Go keyword", name)
		}
		if reason := buzzReservedReason(name); reason != "" {
			return reason
		}
		for _, l := range []string{langPython, langRust, langTypeScript} {
			if slices.Contains(keywords[l], name) {
				return fmt.Sprintf("%q is a %s keyword", name, l)
			}
		}
		return ""
	}
	shape := identifierRe
	if lang == langTypeScript {
		shape = tsIdentifierRe
	}
	switch {
	case !shape.MatchString(name) || (lang == langRust && name == "_"):
		return fmt.Sprintf("%q is not a %s identifier", name, lang)
	case slices.Contains(keywords[lang], name):
		return fmt.Sprintf("%q is a %s keyword", name, lang)
	}
	return ""
}

// buzzReservedReason says why Buzz will not bind name, or "". It asks gopherbuzz rather
// than keeping a list here, so a word the parser starts reserving is refused without a
// second edit. A reserved identifier (`str`, `type`) lexes as a name but upstream Buzz
// will not bind it.
func buzzReservedReason(name string) string {
	switch {
	case buzztoken.IsKeyword(name):
		return fmt.Sprintf("%q is a Buzz keyword", name)
	case buzz.IsReservedIdent(name):
		return fmt.Sprintf("%q is reserved in Buzz", name)
	}
	return ""
}

// BuzzNameCaptures refuses to when a Buzz file the rename writes, or a file that imports
// one, already spells it as an identifier. The Buzz index records no locals, so
// RenameCollisions cannot see a parameter or local named to, and a call renamed to it would
// bind to that local instead; a flat import merges the renamed name into its importer,
// where a name of the importer's own would capture it the same way. The check is by token,
// not by scope, so it also refuses a use that would not collide: a refusal costs a second
// name, a missed capture a silent rebinding.
//
// importers lists the workspace-relative files that import file, and read returns a file's
// source. A file that cannot be read or lexed is refused, since its uses are unknown.
func BuzzNameCaptures(to string, sites []Site, importers func(file string) []string, read func(file string) ([]byte, error)) []types.EditRefusal {
	files := map[string]bool{}
	for _, s := range sites {
		file := path.Clean(s.Path)
		if fileLanguage(file) != langBuzz {
			continue
		}
		files[file] = true
		for _, imp := range importers(file) {
			if imp = path.Clean(imp); fileLanguage(imp) == langBuzz {
				files[imp] = true
			}
		}
	}
	var refused []types.EditRefusal
	for _, file := range slices.Sorted(maps.Keys(files)) {
		src, err := read(file)
		if err != nil {
			refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("cannot be read to check it does not already use %q: %v", to, err)})
			continue
		}
		uses, err := hasIdentifier(string(src), to)
		switch {
		case err != nil:
			refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("does not lex as Buzz, so whether it already uses %q is unknown: %v", to, err)})
		case uses:
			refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf(
				"already uses %q as an identifier, which the symbol index does not record, so a renamed site could bind to it", to)})
		}
	}
	return refused
}

// BuzzLabelUses refuses a rename of from wherever a Buzz file spells it as a value that is
// also a label: an unlabeled bare identifier after a call's first argument, which Buzz
// reads as `f(a, b: b)`, or a punned object field, `Rect{ w }` for `Rect{ w = w }`. The
// index records no occurrence there, since rewriting it would rename the label or field
// too, so the rename would leave the use behind. The files checked are those that define
// the symbol (defs), those a site rewrites, and every file importing a defining one,
// however it imports it. A file that cannot be read or parsed is refused, since its uses
// are unknown. An explicit `b: b` or `w = w` is refused as well: the AST does not tell it
// from the implicit form.
func BuzzLabelUses(from string, defs []string, sites []Site, importers func(file string) []string, read func(file string) ([]byte, error)) []types.EditRefusal {
	files := map[string]bool{}
	for _, def := range defs {
		def = path.Clean(def)
		if fileLanguage(def) != langBuzz {
			continue
		}
		files[def] = true
		for _, imp := range importers(def) {
			if imp = path.Clean(imp); fileLanguage(imp) == langBuzz {
				files[imp] = true
			}
		}
	}
	for _, s := range sites {
		if file := path.Clean(s.Path); fileLanguage(file) == langBuzz {
			files[file] = true
		}
	}
	var refused []types.EditRefusal
	for _, file := range slices.Sorted(maps.Keys(files)) {
		src, err := read(file)
		if err != nil {
			refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("cannot be read to check how it uses %q: %v", from, err)})
			continue
		}
		prog, err := buzz.ParseEmbedded(string(src))
		if err != nil {
			refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("does not parse as Buzz, so whether it uses %q as a label is unknown: %v", from, err)})
			continue
		}
		for _, at := range labelUses(prog, from) {
			refused = append(refused, types.EditRefusal{Path: file, Line: at.Line, Column: at.Col, Reason: fmt.Sprintf(
				"uses %q as a label as well as a value, which the symbol index records no site for; write the label out (`%s: %s`, `%s = %s`) first", from, from, from, from, from)})
		}
	}
	return refused
}

// labelUses is where prog spells name as an implicit call label or a punned field, in
// source order.
func labelUses(prog *ast.Program, name string) []ast.Pos {
	var out []ast.Pos
	visit := func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			for i, arg := range n.Args {
				id, ok := arg.(*ast.IdentExpr)
				if !ok || i == 0 || id.Name != name {
					continue
				}
				if i >= len(n.ArgNames) || n.ArgNames[i] == "" || n.ArgNames[i] == name {
					out = append(out, id.Pos)
				}
			}
		case *ast.ObjectLit:
			for i, v := range n.Values {
				if id, ok := v.(*ast.IdentExpr); ok && id.Name == name && i < len(n.Keys) && n.Keys[i] == name {
					out = append(out, id.Pos)
				}
			}
		}
		return true
	}
	for _, stmt := range prog.Stmts {
		ast.Inspect(stmt, visit)
	}
	slices.SortFunc(out, func(a, b ast.Pos) int { return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Col, b.Col)) })
	return out
}

// hasIdentifier reports whether the Buzz source src holds name as an identifier token,
// inside an interpolated string's expressions too.
func hasIdentifier(src, name string) (bool, error) {
	toks, err := buzztoken.Tokenize(src)
	if err != nil {
		return false, err
	}
	for _, tok := range toks {
		if tok.Kind == buzztoken.Ident && tok.Val == name {
			return true, nil
		}
		for _, part := range tok.Parts {
			if !part.IsExpr {
				continue
			}
			uses, err := hasIdentifier(part.Text, name)
			if err != nil {
				// A backtick string's braces need not hold Buzz: a JSON fixture lexes as part of
				// its file but not alone. The name as a whole word there may refuse more than it
				// must, never less.
				uses = hasWord(part.Text, name)
			}
			if uses {
				return true, nil
			}
		}
	}
	return false, nil
}

// hasWord reports whether name appears in text with no identifier character on either side.
func hasWord(text, name string) bool {
	for i := 0; ; {
		at := strings.Index(text[i:], name)
		if at < 0 {
			return false
		}
		start, end := i+at, i+at+len(name)
		if !identRune(text, start-1, true) && !identRune(text, end, false) {
			return true
		}
		i = start + 1
	}
}

// identRune reports whether the rune ending before i (before) or starting at i (after) is an
// identifier character; outside text it is not.
func identRune(text string, i int, before bool) bool {
	if i < 0 || i >= len(text) {
		return false
	}
	var r rune
	if before {
		r, _ = utf8.DecodeLastRuneInString(text[:i+1])
	} else {
		r, _ = utf8.DecodeRuneInString(text[i:])
	}
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
