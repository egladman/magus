package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// listingTree is a workspace whose graph indexes every file but the testdata fixture.
func listingTree(t *testing.T) (string, Dependencies) {
	t.Helper()
	root := writeTree(t, map[string]string{
		"internal/api/handler.go":        "package api\n",
		"internal/api/handler_test.go":   "package api\n",
		"internal/api/gen/x.go":          "package gen\n",
		"internal/api/testdata/fix.json": "{}\n",
		"spells/a/spell.buzz":            "fun a() > void {}\n",
		"spells/b/spell.buzz":            "fun b() > void {}\n",
		"spells/b/.hidden/notes.txt":     "x\n",
		"internal/half/indexed.go":       "package half\n",
		"internal/half/new_unindexed.go": "package half\n",
	})
	return root, Dependencies{
		GraphIDs: graphOf(map[string][]string{types.KindFile: {
			"file:internal/api/handler.go", "file:internal/api/handler_test.go", "file:internal/api/gen/x.go",
			"file:spells/a/spell.buzz", "file:spells/b/spell.buzz", "file:internal/half/indexed.go",
		}}),
		scope:   workspaceScope{root: root},
		callDir: root,
	}
}

// TestListingsTheGraphAnswers pins the file-finding half: a listing whose every entry is a
// node the graph holds is refused with the query and its answer inline, and a listing the
// graph cannot reproduce entry for entry runs.
func TestListingsTheGraphAnswers(t *testing.T) {
	_, deps := listingTree(t)

	for _, tt := range []struct {
		command string
		query   string   // the query served; "" lets the listing run
		answer  []string // ids the deny must carry
	}{
		{`ls internal/api/gen`, `query 'id=~^(?:file|dir):internal/api/gen/[^/]+$' -o name`, []string{"file:internal/api/gen/x.go"}},
		{`ls -1 spells`, `query 'id=~^(?:file|dir):spells/[^/]+$' -o name`, []string{"dir:spells/a", "dir:spells/b"}},
		{`ls -R spells`, `query kind=file 'id=~^file:spells/.*$' -o name`, []string{"file:spells/a/spell.buzz", "file:spells/b/spell.buzz"}},
		{`git ls-files spells`, `query kind=file 'id=~^file:(?:spells/).*$' -o name`, []string{"file:spells/a/spell.buzz"}},
		{`git ls-files 'spells/**/spell.buzz'`, `query kind=file 'id=~^file:(?:spells/)(?:.*/)?spell\.buzz$' -o name`, []string{"file:spells/b/spell.buzz"}},
		{`git ls-files internal/api/gen | grep x`, `query kind=file 'id=~^file:(?:internal/api/gen/).*$' -o name`, []string{"file:internal/api/gen/x.go"}},

		// A fixture directory the graph does not index: the listing prints what no node holds.
		{`ls internal/api`, "", nil},
		{`ls -R internal/api`, "", nil},
		{`git ls-files internal/api`, "", nil},
		// A file on disk the index has not reached.
		{`ls internal/half`, "", nil},
		// Metadata, untracked files, a tracked-file check, and another tree ask other questions.
		{`ls -la internal/api/gen`, "", nil},
		{`ls -lt spells`, "", nil},
		{`git ls-files --others --exclude-standard spells`, "", nil},
		{`git ls-files spells/a/spell.buzz`, "", nil},
		{`ls /tmp`, "", nil},
	} {
		v := Evaluate(deps, tt.command)
		if tt.query == "" {
			assert.Empty(t, v.Deny, tt.command)
			continue
		}
		require.Equal(t, denyRuleSearchTranslation, v.Rule.Name, "%q: %s", tt.command, v.Deny)
		assert.Contains(t, v.Deny, " "+tt.query+"` answers this search exactly.", tt.command)
		for _, id := range tt.answer {
			assert.Contains(t, v.Deny, "  "+id, tt.command)
		}
	}
}

// A graph whose file nodes are not current proves nothing, so the listing runs.
func TestListingsRunWithoutACurrentGraph(t *testing.T) {
	_, deps := listingTree(t)
	deps.GraphIDs = nil
	for _, command := range []string{`ls internal/api/gen`, `ls -R spells`, `git ls-files spells`} {
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
