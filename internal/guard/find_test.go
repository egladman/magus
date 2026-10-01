package guard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// listingTree is a workspace whose graph indexes every file but a testdata fixture, a file
// the index has not reached, and twenty-one notes; every file but the hidden one is tracked.
func listingTree(t *testing.T) (string, Dependencies) {
	t.Helper()
	files := map[string]string{
		"internal/api/handler.go":        "package api\n",
		"internal/api/handler_test.go":   "package api\n",
		"internal/api/gen/x.go":          "package gen\n",
		"internal/api/testdata/fix.json": "{}\n",
		"spells/a/spell.buzz":            "fun a() > void {}\n",
		"spells/b/spell.buzz":            "fun b() > void {}\n",
		"spells/b/.hidden/notes.txt":     "x\n",
		"internal/half/indexed.go":       "package half\n",
		"internal/half/new_unindexed.go": "package half\n",
		"docs/site.go":                   "package docs\n",
	}
	for i := 1; i <= 21; i++ {
		files[fmt.Sprintf("docs/n%02d.md", i)] = "# note\n"
	}
	root := writeTree(t, files)
	var tracked []string
	for rel := range files {
		if !strings.Contains(rel, "/.hidden/") {
			tracked = append(tracked, rel)
		}
	}
	return root, Dependencies{
		IndexedIDs: graphOf(map[string][]string{types.KindFile: {
			"file:internal/api/handler.go", "file:internal/api/handler_test.go", "file:internal/api/gen/x.go",
			"file:spells/a/spell.buzz", "file:spells/b/spell.buzz", "file:internal/half/indexed.go", "file:docs/site.go",
		}}),
		TrackedFiles: func(context.Context, string) ([]string, bool) { return tracked, true },
		scope:        workspaceScope{root: root},
		callDir:      root,
	}
}

// TestListingsTheGraphAnswers pins the file-finding half: a listing whose every file is a
// node the graph holds is refused with a query selecting exactly those files and its answer
// inline, and a listing the graph cannot reproduce runs.
func TestListingsTheGraphAnswers(t *testing.T) {
	_, deps := listingTree(t)

	for _, tt := range []struct {
		command string
		query   string   // the query served; "" lets the listing run
		answer  []string // ids the deny must carry
	}{
		{`ls internal/api/gen`, `query 'id=~^(?:file|dir):internal/api/gen/[^/]+$' -o name`, []string{"file:internal/api/gen/x.go"}},
		{`ls -1 spells`, `query 'id=~^(?:file|dir):spells/[^/]+$' -o name`, []string{"dir:spells/a", "dir:spells/b"}},
		{`ls -R spells`, `query kind=file 'id=~^file:spells/(?:.*/)?[^/]+$' -o name`, []string{"file:spells/a/spell.buzz", "file:spells/b/spell.buzz"}},
		{`git ls-files spells`, `query kind=file 'id=~^file:(?:spells/.*)$' -o name`, []string{"file:spells/a/spell.buzz"}},
		{`git ls-files 'spells/**/spell.buzz'`, `query kind=file 'id=~^file:(?:spells/(?:.*/)?spell\.buzz)$' -o name`, []string{"file:spells/b/spell.buzz"}},

		// find by path as well as name, and with a negation, is enumerated: no one pattern
		// over ids says both.
		{`find . -name '*.buzz' -path '*/b/*'`, `query kind=file 'id=~^file:(?:spells/b/spell\.buzz)$' -o name`, []string{"file:spells/b/spell.buzz"}},
		{`find spells -name '*.buzz' -not -path '*/a/*'`, `query kind=file 'id=~^file:(?:spells/b/spell\.buzz)$' -o name`, []string{"file:spells/b/spell.buzz"}},
		{`find spells -type f -name '*.buzz'`, `query kind=file 'id=~^file:spells/(?:.*/)?[^/]*\.buzz$' -o name`, []string{"file:spells/a/spell.buzz"}},

		// fd and rg --files skip hidden entries, as their walks do.
		{`fd -t f spell spells`, `query kind=file 'id=~^file:spells/(?:.*/)?(?i:[^/]*(?:spell)[^/]*)$' -o name`, []string{"file:spells/b/spell.buzz"}},
		{`fd -e buzz . spells`, `query kind=file 'id=~^file:(?:spells/a/spell\.buzz|spells/b/spell\.buzz)$' -o name`, []string{"file:spells/a/spell.buzz"}},
		{`rg --files spells`, `query kind=file 'id=~^file:spells/(?:.*/)?[^/]+$' -o name`, []string{"file:spells/b/spell.buzz"}},
		{`rg --files -g '*.go' internal/api`, `query kind=file 'id=~^file:internal/api/(?:.*/)?[^/]*\.go$' -o name`, []string{"file:internal/api/gen/x.go"}},

		// The graph answers a tracked listing piped into a search of its paths.
		{`git ls-files internal/api/gen | grep x`, `query kind=file 'id=~^file:internal/api/gen/.*(?:x).*$' -o name`, []string{"file:internal/api/gen/x.go"}},
		{`git ls-files | grep spell`, `query kind=file 'id=~^file:.*(?:spell).*$' -o name`, []string{"file:spells/a/spell.buzz", "file:spells/b/spell.buzz"}},

		// A fixture directory, or a file the index has not reached, is printed by the
		// listing and held by no node.
		{`ls internal/api`, "", nil},
		{`ls -R internal/api`, "", nil},
		{`ls internal/half`, "", nil},
		{`find internal -name '*.go'`, "", nil},
		{`fd x internal/api`, "", nil},
		{`rg --files internal/api`, "", nil},
		// Too many tracked files the graph does not index to name.
		{`git ls-files | grep docs/`, "", nil},
		// Metadata, untracked files, a tracked-file check, an inverted or counted filter,
		// and another tree ask other questions.
		{`ls -la internal/api/gen`, "", nil},
		{`ls -lt spells`, "", nil},
		{`git ls-files --others --exclude-standard spells`, "", nil},
		{`git ls-files spells/a/spell.buzz`, "", nil},
		{`git ls-files | grep -v spell`, "", nil},
		{`git ls-files | grep -c spell`, "", nil},
		{`fd -H spell spells`, "", nil},
		{`ls /tmp`, "", nil},
	} {
		v := Evaluate(deps, tt.command)
		if tt.query == "" {
			assert.Empty(t, v.Deny, tt.command)
			continue
		}
		require.Equal(t, denyRuleSearchTranslation, v.Rule.Name, "%q: %s", tt.command, v.Deny)
		assert.Contains(t, v.Deny, " "+tt.query+"` answers this search exactly.", tt.command)
		assert.NotContains(t, v.Deny, "The pipe after the search is not reproduced", "%q: the filter is part of the answer", tt.command)
		for _, id := range tt.answer {
			assert.Contains(t, v.Deny, "  "+id, tt.command)
		}
	}
}

// A tracked listing whose matches include files the graph does not index is answered for
// the ones it does, and names the rest, so the deny loses nothing.
func TestTrackedListingNamesWhatTheGraphLeavesOut(t *testing.T) {
	_, deps := listingTree(t)
	for command, left := range map[string]string{
		`git ls-files internal/api`: "internal/api/testdata/fix.json",
		`git ls-files | grep half`:  "internal/half/new_unindexed.go",
	} {
		v := Evaluate(deps, command)
		require.Equal(t, denyRuleSearchTranslation, v.Rule.Name, "%q: %s", command, v.Deny)
		assert.Contains(t, v.Deny, "answers this search for every matching file the graph indexes.", command)
		assert.Contains(t, v.Deny, "Version control also tracks 1 matching file the graph does not index, which the query leaves out: "+left+".", command)
	}

	// An indexed file version control does not track would make the graph answer more.
	deps.TrackedFiles = func(context.Context, string) ([]string, bool) { return []string{"spells/a/spell.buzz"}, true }
	assert.Empty(t, Evaluate(deps, `git ls-files spells`).Deny)

	// With no answer from version control a named directory is walked instead, and a piped
	// filter or the whole checkout is not attempted.
	deps.TrackedFiles = nil
	assert.Equal(t, denyRuleSearchTranslation, Evaluate(deps, `git ls-files spells`).Rule.Name)
	assert.Empty(t, Evaluate(deps, `git ls-files | grep spell`).Deny)
}

// The proof walks the disk, so the last build's ids serve whether or not the index is
// current; only with no index at all does the listing run.
func TestListingsUseTheLastBuildsIDs(t *testing.T) {
	_, deps := listingTree(t)
	assert.Equal(t, denyRuleSearchTranslation, Evaluate(deps, `ls internal/api/gen`).Rule.Name)

	deps.IndexedIDs = func(context.Context, string) ([]string, bool) { return nil, false }
	for _, command := range []string{`ls internal/api/gen`, `ls -R spells`, `git ls-files spells`, `fd -e buzz . spells`} {
		assert.Empty(t, Evaluate(deps, command).Deny, command)
	}
}

func TestGitGlobRegexp(t *testing.T) {
	for glob, want := range map[string]string{
		"*.go":             `.*\.go`,
		"**/spell.buzz":    `(?:.*/)?spell\.buzz`,
		"a/**/b.go":        `a/(?:.*/)?b\.go`,
		"cmd/magus-utils*": `cmd/magus-utils.*`,
		"x?.ts":            `x[^/]\.ts`,
	} {
		assert.Equal(t, want, gitGlobRegexp(glob), glob)
	}
}
