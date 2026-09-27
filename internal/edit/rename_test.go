package edit

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestRenameTarget(t *testing.T) {
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
			got, err := RenameTarget(tc.ref, tc.matches, defined)
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

	for _, tc := range []struct {
		name  string
		files []types.SymbolOccurrenceFile
		to    string
		want  []types.EditRefusal
	}{
		{"not an identifier", []types.SymbolOccurrenceFile{good}, "de code", []types.EditRefusal{{Reason: `"de code" is not an identifier`}}},
		{"syntax in the name", []types.SymbolOccurrenceFile{good}, "x()", []types.EditRefusal{{Reason: `"x()" is not an identifier`}}},
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
			[]types.EditRefusal{{Path: "a.go", Line: 4, Col: 2, Reason: "the site is mismatch, not verified; refresh with `magus graph build`"}},
		},
		{
			"another spelling",
			[]types.SymbolOccurrenceFile{good, {File: "c.go", Occurrences: []types.SymbolOccurrence{occurrence(3, 8, "example.com/parse", verified)}}},
			"decode",
			[]types.EditRefusal{{Path: "c.go", Line: 3, Col: 8, Reason: `the site spells the symbol "example.com/parse", which renaming "parse" does not rewrite; edit it by hand`}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refused := RenameSites(tc.files, "parse", tc.to)
			assert.Equal(t, tc.want, refused)
		})
	}
}
