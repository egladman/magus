package edit

import (
	"fmt"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
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
		if languageOf(file) == langGo {
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
			if files[file] || (languageOf(file) == langGo && goPackages[path.Dir(file)]) {
				refused = append(refused, types.EditRefusal{Path: file, Reason: fmt.Sprintf("already defines %q (%s), which the new name would collide with", to, m.ID)})
			}
		}
	}
	return refused
}

// The languages the symbol indexes cover, by the name a refusal prints.
const (
	langGo         = "Go"
	langPython     = "Python"
	langRust       = "Rust"
	langTypeScript = "TypeScript"
)

func languageOf(file string) string {
	switch path.Ext(file) {
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
	// identifierRe is the identifier shape all four languages accept; TypeScript also
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
		langs[languageOf(f.File)] = true
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
	case "":
		if !identifierRe.MatchString(name) || name == "_" {
			return fmt.Sprintf("%q is not an identifier", name)
		}
		if token.IsKeyword(name) {
			return fmt.Sprintf("%q is a Go keyword", name)
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
