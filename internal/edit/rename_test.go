package edit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestResolveRenameSymbol(t *testing.T) {
	t.Parallel()

	const (
		mine    = "symbol:gomod example.com/a `example.com/a`/parse()."
		other   = "symbol:gomod example.com/a `example.com/a/b`/parse()."
		dep     = "symbol:gomod example.com/dep `example.com/dep`/parse()."
		similar = "symbol:gomod example.com/a `example.com/a`/parseAll()."
	)
	defined := func(id string) bool { return id != dep }
	match := func(id, label string) types.KnowledgeMatch {
		return types.KnowledgeMatch{ID: id, Kind: types.KindSymbol, Label: label}
	}

	for _, tc := range []struct {
		name    string
		ref     string
		matches []types.KnowledgeMatch
		want    string
		err     string
	}{
		{
			name:    "a bare name one workspace symbol carries",
			ref:     "parse",
			matches: []types.KnowledgeMatch{match(similar, "parseAll"), match(dep, "parse"), match(mine, "parse")},
			want:    mine,
		},
		{
			name:    "an exact id beats a same-named sibling",
			ref:     other,
			matches: []types.KnowledgeMatch{match(mine, "parse"), match(other, "parse")},
			want:    other,
		},
		{
			name:    "a bare name two workspace symbols carry",
			ref:     "parse",
			matches: []types.KnowledgeMatch{match(other, "parse"), match(mine, "parse")},
			err:     "\"parse\" names 2 symbols defined in this workspace; pass the id of one:\n  " + other + "\n  " + mine,
		},
		{
			name:    "only a fuzzy match",
			ref:     "pars",
			matches: []types.KnowledgeMatch{match(mine, "parse"), {ID: "target:.:parse", Kind: types.KindTarget, Label: "pars"}},
			err:     "no symbol defined in this workspace is named \"pars\"; `magus refs pars` lists the one it resolves to, and its id renames it",
		},
		{
			name:    "a dependency's symbol by id",
			ref:     dep,
			matches: []types.KnowledgeMatch{match(dep, "parse")},
			err:     dep + " is not defined in this workspace, so there is no definition to rename",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ResolveRenameSymbol(tc.ref, tc.matches, defined)
			if tc.err != "" {
				assert.EqualError(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func occurrence(line, col int, text string, status types.SymbolOccurrenceStatus) types.SymbolOccurrence {
	return types.SymbolOccurrence{Line: line, Column: col, EndLine: line, EndColumn: col + len(text), Text: text, Status: status}
}

func TestRenameAppliesEveryVerifiedSite(t *testing.T) {
	t.Parallel()

	root := writeFiles(t, map[string]string{
		"a.go":   "package a\n\nfunc parse() {}\n\nfunc parseAll() { parse(); parse() }\n",
		"b/b.go": "package b\n\nvar _ = a.parse\n",
	})
	verified := types.SymbolOccurrenceVerified
	files := []types.SymbolOccurrenceFile{
		{File: "a.go", Occurrences: []types.SymbolOccurrence{
			occurrence(3, 6, "parse", verified), occurrence(5, 19, "parse", verified), occurrence(5, 28, "parse", verified),
		}},
		{File: "b/b.go", Occurrences: []types.SymbolOccurrence{occurrence(3, 11, "parse", verified)}},
	}

	sites, refused := RenameSites(files, "parse", "decode")
	require.Empty(t, refused)
	require.NoError(t, Resolve(root, sites).Apply())

	assert.Equal(t, "package a\n\nfunc decode() {}\n\nfunc parseAll() { decode(); decode() }\n", readFile(t, root, "a.go"))
	assert.Equal(t, "package b\n\nvar _ = a.decode\n", readFile(t, root, "b/b.go"))
}

func TestRenameSitesRefusals(t *testing.T) {
	t.Parallel()

	verified := types.SymbolOccurrenceVerified
	good := types.SymbolOccurrenceFile{File: "a.go", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}
	ts := types.SymbolOccurrenceFile{File: "web/a.ts", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}
	py := types.SymbolOccurrenceFile{File: "tools/a.py", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}
	rs := types.SymbolOccurrenceFile{File: "src/a.rs", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}
	bz := types.SymbolOccurrenceFile{File: "hack/a.buzz", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}
	other := types.SymbolOccurrenceFile{File: "a.kt", Occurrences: []types.SymbolOccurrence{occurrence(1, 1, "parse", verified)}}

	for _, tc := range []struct {
		name  string
		files []types.SymbolOccurrenceFile
		to    string
		want  []types.EditRefusal
	}{
		{"not an identifier", []types.SymbolOccurrenceFile{good}, "de code", []types.EditRefusal{{Reason: `"de code" is not a Go identifier`}}},
		{"syntax in the name", []types.SymbolOccurrenceFile{good}, "x()", []types.EditRefusal{{Reason: `"x()" is not a Go identifier`}}},
		{"a Go keyword", []types.SymbolOccurrenceFile{good}, "func", []types.EditRefusal{{Reason: `"func" is a Go keyword`}}},
		{"a dollar in Go", []types.SymbolOccurrenceFile{good}, "$parse", []types.EditRefusal{{Reason: `"$parse" is not a Go identifier`}}},
		{"the blank identifier", []types.SymbolOccurrenceFile{good}, "_", []types.EditRefusal{{Reason: `"_" is not a Go identifier`}}},
		{"a dollar in TypeScript", []types.SymbolOccurrenceFile{ts}, "$parse", nil},
		{"a keyword of one language the rename writes", []types.SymbolOccurrenceFile{ts, py}, "class", []types.EditRefusal{
			{Reason: `"class" is a Python keyword`}, {Reason: `"class" is a TypeScript keyword`},
		}},
		{"a keyword of the other only", []types.SymbolOccurrenceFile{py}, "type", nil},
		{"a Rust keyword", []types.SymbolOccurrenceFile{rs}, "fn", []types.EditRefusal{{Reason: `"fn" is a Rust keyword`}}},
		{"a Buzz keyword", []types.SymbolOccurrenceFile{bz}, "fun", []types.EditRefusal{{Reason: `"fun" is a Buzz keyword`}}},
		{"another Buzz keyword", []types.SymbolOccurrenceFile{bz}, "final", []types.EditRefusal{{Reason: `"final" is a Buzz keyword`}}},
		{"a Buzz reserved identifier", []types.SymbolOccurrenceFile{bz}, "str", []types.EditRefusal{{Reason: `"str" is reserved in Buzz`}}},
		{"a dollar in Buzz", []types.SymbolOccurrenceFile{bz}, "$parse", []types.EditRefusal{{Reason: `"$parse" is not a Buzz identifier`}}},
		{"a raw Buzz identifier", []types.SymbolOccurrenceFile{bz}, `@"parse"`, []types.EditRefusal{{Reason: `"@\"parse\"" is not a Buzz identifier`}}},
		{"a name gopherbuzz lexes as a letter", []types.SymbolOccurrenceFile{bz}, "größe", nil},
		{"a non-ASCII digit after a letter", []types.SymbolOccurrenceFile{bz}, "x٣", nil},
		{"a non-ASCII symbol", []types.SymbolOccurrenceFile{bz}, "x·y", []types.EditRefusal{{Reason: `"x·y" is not a Buzz identifier`}}},
		{"Buzz admits test as a name", []types.SymbolOccurrenceFile{bz}, "test", nil},
		{"an unknown language takes Buzz keywords too", []types.SymbolOccurrenceFile{other}, "fun", []types.EditRefusal{{Reason: `"fun" is a Buzz keyword`}}},
		{"an unknown language takes the shared shape", []types.SymbolOccurrenceFile{other}, "$parse", []types.EditRefusal{{Reason: `"$parse" is not an identifier`}}},
		{"an unknown language takes every keyword", []types.SymbolOccurrenceFile{other}, "def", []types.EditRefusal{{Reason: `"def" is a Python keyword`}}},
		{"the same name", []types.SymbolOccurrenceFile{good}, "parse", []types.EditRefusal{{Reason: `the symbol is already named "parse"`}}},
		{"no occurrences", nil, "decode", []types.EditRefusal{{Reason: "the index records no occurrence of the symbol"}}},
		{
			"a stale file",
			[]types.SymbolOccurrenceFile{good, {File: "b.go", Stale: true, Occurrences: []types.SymbolOccurrence{occurrence(2, 3, "parse", verified)}}},
			"decode",
			[]types.EditRefusal{{Path: "b.go", Reason: "changed after it was indexed, so it may hold sites the index never saw; refresh with `magus graph build`"}},
		},
		{
			"an unverified site",
			[]types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{occurrence(4, 2, "other", types.SymbolOccurrenceMismatch)}}},
			"decode",
			[]types.EditRefusal{{Path: "a.go", Line: 4, Column: 2, Reason: "the site is mismatch, not verified; refresh with `magus graph build`"}},
		},
		{
			"another spelling",
			[]types.SymbolOccurrenceFile{good, {File: "c.go", Occurrences: []types.SymbolOccurrence{occurrence(3, 8, "example.com/parse", verified)}}},
			"decode",
			[]types.EditRefusal{{Path: "c.go", Line: 3, Column: 8, Reason: `the site spells the symbol "example.com/parse", which renaming "parse" does not rewrite; edit it by hand`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refused := RenameSites(tc.files, "parse", tc.to)
			assert.Equal(t, tc.want, refused)
		})
	}
}

func TestRenameCollisions(t *testing.T) {
	t.Parallel()

	const (
		sameFile    = "symbol:gomod example.com/a `example.com/a`/decode()."
		samePackage = "symbol:gomod example.com/a `example.com/a`/decodeAll()."
		elsewhere   = "symbol:gomod example.com/a `example.com/a/c`/decode()."
		pySibling   = "symbol:python tools/b.py decode()."
	)
	defs := map[string][]string{
		sameFile:    {"a.go"},
		samePackage: {"a_other.go"},
		elsewhere:   {"c/c.go"},
		pySibling:   {"tools/b.py"},
	}
	definedIn := func(id string) []string { return defs[id] }
	match := func(id, label string) types.KnowledgeMatch {
		return types.KnowledgeMatch{ID: id, Kind: types.KindSymbol, Label: label}
	}
	sites := []Site{site("a.go", 1, 1, "parse", "decode"), site("./tools/a.py", 1, 1, "parse", "decode")}

	got := RenameCollisions("decode", []types.KnowledgeMatch{
		match(sameFile, "decode"),
		match(samePackage, "decode"),
		match(elsewhere, "decode"),
		match(pySibling, "decode"),
		match("symbol:gomod example.com/a `example.com/a`/decoder().", "decoder"),
		{ID: "target:.:decode", Kind: types.KindTarget, Label: "decode"},
	}, definedIn, sites)

	assert.Equal(t, []types.EditRefusal{
		{Path: "a.go", Reason: `already defines "decode" (` + sameFile + `), which the new name would collide with`},
		{Path: "a_other.go", Reason: `already defines "decode" (` + samePackage + `), which the new name would collide with`},
	}, got, "a Go package is one scope; another directory or a Python sibling module is not")
}

// The Buzz index records no locals, so a parameter or local already named the new name is
// invisible to RenameCollisions, and the renamed call would bind to it. A file that
// imports one the rename writes is checked too, since a flat import merges the new name
// into it; a file outside both is not read.
func TestBuzzNameCaptures(t *testing.T) {
	t.Parallel()

	sources := map[string]string{
		"lib/util.buzz":  "export fun foo() > int { return 1; }\n",
		"main.buzz":      "import \"lib/util\";\nfun g(x: int) > int { return foo(); }\n",
		"importer.buzz":  "import \"lib/util\";\nfun h() > str { return \"{x}\"; }\n",
		"unrelated.buzz": "fun x() > void {}\n",
		"broken.buzz":    "fun \"\n",
	}
	read := func(file string) ([]byte, error) { return []byte(sources[file]), nil }
	importers := func(file string) []string {
		if file == "lib/util.buzz" {
			return []string{"importer.buzz", "lib/util.go"}
		}
		return nil
	}
	sites := []Site{site("lib/util.buzz", 1, 12, "foo", "x"), site("./main.buzz", 2, 30, "foo", "x"), site("a.go", 1, 1, "foo", "x")}

	assert.Equal(t, []types.EditRefusal{
		{Path: "importer.buzz", Reason: `already uses "x" as an identifier, which the symbol index does not record, so a renamed site could bind to it`},
		{Path: "main.buzz", Reason: `already uses "x" as an identifier, which the symbol index does not record, so a renamed site could bind to it`},
	}, BuzzNameCaptures("x", sites, importers, read), "the parameter in main.buzz, the interpolation in importer.buzz")

	assert.Empty(t, BuzzNameCaptures("y", sites, importers, read), "a name no checked file uses")

	got := BuzzNameCaptures("x", []Site{site("broken.buzz", 1, 1, "foo", "x")}, importers, read)
	require.Len(t, got, 1)
	assert.Equal(t, "broken.buzz", got[0].Path)
	assert.Contains(t, got[0].Reason, "does not lex as Buzz")
}

// The file lexes, but an interpolation's text does not lex on its own: the check falls back
// to the name as a whole word there rather than refusing the file.
func TestHasIdentifierReadsAnInterpolationThatDoesNotLex(t *testing.T) {
	t.Parallel()

	// A JSON fixture in a backtick string, as hack/ci/pull-requests.buzz's tests hold.
	uses, err := hasIdentifier("final s = `{\"body\": \"<!-- {not x -->\", \"url\": \"u\"}`;\n", "x")
	require.NoError(t, err)
	assert.True(t, uses, "the name as a word in the part")

	uses, err = hasIdentifier("final s = `{\"body\": \"<!-- {not xy -->\", \"url\": \"u\"}`;\n", "x")
	require.NoError(t, err)
	assert.False(t, uses, "a longer identifier is not the name")
}

// scip-buzz records no occurrence where a value is also its label, `f(a, w)` for
// `f(a, w: w)` or `Rect{ w }` for `Rect{ w = w }`, so a rename of w would leave those
// uses naming nothing. The defining file, a file a site rewrites and an importer of the
// defining file are each checked; a first argument, a labeled one and a field set from
// another name are fine.
func TestBuzzLabelUses(t *testing.T) {
	t.Parallel()

	sources := map[string]string{
		"lib/util.buzz": "export final w = 1;\nobject Rect { w: int }\nfun f(a: int, w: int) > int { return a; }\nexport fun g() > Rect { return Rect{ w }; }\n",
		"main.buzz":     "import \"lib/util\" as u;\nfun h() > int { return u\\f(w, w: u\\w); }\n",
		"alias.buzz":    "import \"lib/util\" as _;\nfun k() > int { return f(1, w); }\n",
		"other.buzz":    "fun m() > int { return f(1, w); }\n",
		"broken.buzz":   "fun (\n",
	}
	read := func(file string) ([]byte, error) { return []byte(sources[file]), nil }
	importers := func(file string) []string {
		if file == "lib/util.buzz" {
			return []string{"main.buzz", "alias.buzz"}
		}
		return nil
	}
	reason := "uses \"w\" as a label as well as a value, which the symbol index records no site for; write the label out (`w: w`, `w = w`) first"

	got := BuzzLabelUses("w", []string{"lib/util.buzz"}, []Site{site("main.buzz", 2, 26, "w", "width")}, importers, read)
	assert.Equal(t, []types.EditRefusal{
		{Path: "alias.buzz", Line: 2, Column: 29, Reason: reason},
		{Path: "lib/util.buzz", Line: 4, Column: 38, Reason: reason},
	}, got, "other.buzz neither defines, imports nor holds a site; main.buzz labels its w")

	assert.Empty(t, BuzzLabelUses("a", []string{"lib/util.buzz"}, nil, importers, read), "a first argument is never a label")

	got = BuzzLabelUses("w", []string{"broken.buzz"}, nil, importers, read)
	require.Len(t, got, 1)
	assert.Contains(t, got[0].Reason, "does not parse as Buzz")
}
