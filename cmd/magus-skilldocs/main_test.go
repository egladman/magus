package main

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/docs"
)

// skillSrc is the directory holding the skills/ tree, relative to this package.
// The tests run in the command's own directory; the embedded bodies live beside
// the CLI that embeds them.
const skillSrc = "../magus"

// generate renders the whole reference into a fresh directory. It drives the real
// embedded bodies rather than a fixture on purpose: the pages exist so a skill
// edit cannot leave the documentation describing a version nobody installs, and a
// fixture would give that guarantee about the fixture.
func generate(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	require.NoError(t, run(out, skillSrc))
	return out
}

func page(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	require.NoError(t, err)
	return string(b)
}

func TestWriteFencedCountsIndentedClosingFence(t *testing.T) {
	var got strings.Builder
	writeFenced(&got, "- example:\n\n  ```sh\n  magus affected ci\n  ```")

	assert.True(t, strings.HasPrefix(got.String(), "````markdown\n"))
	assert.True(t, strings.HasSuffix(got.String(), "\n````\n\n"))
}

// TestWriteFencedUsesThreeBackticksForPlainText keeps the fence from growing
// without cause: a body with no fences of its own reads better in the source at
// the conventional three.
func TestWriteFencedUsesThreeBackticksForPlainText(t *testing.T) {
	var got strings.Builder
	writeFenced(&got, "plain prose with an `inline` span")

	assert.Equal(t, "```markdown\nplain prose with an `inline` span\n```\n\n", got.String())
}

// TestRunWritesOnePagePerShippedSkill pins the file set the site publishes. A
// page for a skill this binary does not ship is a page nobody can install from,
// and a missing one is a skill with no documentation at all.
func TestRunWritesOnePagePerShippedSkill(t *testing.T) {
	out := generate(t)

	entries, err := os.ReadDir(out)
	require.NoError(t, err)
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}

	defs, err := agent.NewCatalog(os.DirFS(skillSrc), "", 0).EmbeddedSkills()
	require.NoError(t, err)
	want := []string{"index.md"}
	for _, d := range defs {
		want = append(want, d.Name+".md")
	}
	sort.Strings(want)
	sort.Strings(got)
	assert.Equal(t, want, got)
}

// TestSkillPageShowsTheStampAndBothForms is the page's whole argument: it tells
// the reader to install rather than copy, so it has to SHOW what an installed
// copy carries and what was dropped from the short form. An assertion the page
// makes without evidence is the thing this test refuses.
func TestSkillPageShowsTheStampAndBothForms(t *testing.T) {
	body := page(t, generate(t), "magus-query.md")

	fm, ok := docs.ParseFrontmatter(body)
	require.True(t, ok, "the page has no parsable frontmatter")
	assert.Equal(t, "magus-query", fm.Title)
	assert.Equal(t, "cmd/magus/skills/magus-query/SKILL.md", fm.GeneratedFrom)
	assert.Equal(t, []string{"agents", "skills", "magus-query"}, fm.Tags)
	assert.True(t, strings.HasSuffix(fm.Description, "."), "the description is trimmed to its opening claim: %q", fm.Description)
	assert.NotContains(t, fm.Description, "Use INSTEAD of Grep", "the trigger text does not belong in a frontmatter description")

	for _, want := range []string{
		"## What an installed copy carries",
		"| `skill-content` | `",
		"| `skill-variant` | `full` |",
		"## Full form",
		"## Short form",
		"<details>\n<summary>Show the short form</summary>",
	} {
		assert.Contains(t, body, want)
	}
	// The two byte counts are stated as facts; the SSG turns them into the ratio
	// the page's prose points at.
	assert.Regexp(t, `(?m)^skill_full_bytes: \d+$`, body)
	assert.Regexp(t, `(?m)^skill_simple_bytes: \d+$`, body)
}

// TestStampTableStopsAtTheBody guards writeStampTable's scan, which walks the
// stamped frontmatter line by line. The description is the page's subtitle
// already, and the body below it is not frontmatter at all - either leaking into
// the table would publish prose as a stamp field.
func TestStampTableStopsAtTheBody(t *testing.T) {
	body := page(t, generate(t), "magus-query.md")
	table := body[strings.Index(body, "## What an installed copy carries"):]
	table = table[:strings.Index(table, "## Full form")]

	assert.NotContains(t, table, "| `name` |")
	assert.NotContains(t, table, "| `description` |")
	assert.NotContains(t, table, "| `---` |")
}

// TestRenamedSkillPageCarriesItsOldURL pins the compat that the rename row
// exists for: a published doc URL is a link someone else wrote, and nothing on
// their end re-runs when the skill is renamed.
func TestRenamedSkillPageCarriesItsOldURL(t *testing.T) {
	out := generate(t)

	require.NotEmpty(t, agent.FormerNames("magus-docs-lookup"), "this test is pinned to a skill that has been renamed")
	assert.Contains(t, page(t, out, "magus-docs-lookup.md"), "aliases:\n  - reference/skills/magus-docs\n")
	assert.NotContains(t, page(t, out, "magus-query.md"), "aliases:", "a skill that was never renamed carries no redirect")
}

// TestIndexTotalsEverySkill checks the numbers the index exists for: the choice
// between the two permutations is meant to be made on measured bytes, so a
// totals row that does not cover every skill misprices it.
func TestIndexTotalsEverySkill(t *testing.T) {
	out := generate(t)
	index := page(t, out, "index.md")

	defs, err := agent.NewCatalog(os.DirFS(skillSrc), "", 0).EmbeddedSkills()
	require.NoError(t, err)
	for _, d := range defs {
		assert.Contains(t, index, "| ["+d.Name+"]("+d.Name+".md) |", "no index row for %s", d.Name)
	}
	assert.Contains(t, index, "| **all "+strconv.Itoa(len(defs))+"** | **")

	fm, ok := docs.ParseFrontmatter(index)
	require.True(t, ok)
	assert.Equal(t, "overview", fm.PageType)
}

// TestPruneDeletesOnlyTheGeneratorsOwnOrphans is the fix for a rename that wrote
// the new page and left the old one published, linked from nothing, describing a
// skill nobody can install. Scoped to the pages this generator emits, so a
// hand-written page dropped in the directory is not collateral.
func TestPruneDeletesOnlyTheGeneratorsOwnOrphans(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"magus-query.md", "magus-renamed-away.md", "index.md", "notes.md", "magus-query.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}
	require.NoError(t, os.Mkdir(filepath.Join(dir, "magus-a-directory.md"), 0o755))

	require.NoError(t, prune(dir, []agent.AgentSkill{{Name: "magus-query"}}))

	for _, kept := range []string{"magus-query.md", "index.md", "notes.md", "magus-query.txt", "magus-a-directory.md"} {
		_, err := os.Stat(filepath.Join(dir, kept))
		assert.NoError(t, err, "%s was removed", kept)
	}
	_, err := os.Stat(filepath.Join(dir, "magus-renamed-away.md"))
	assert.True(t, os.IsNotExist(err), "the orphaned page survived pruning")
}

func TestPruneReportsAnUnreadableDirectory(t *testing.T) {
	assert.Error(t, prune(filepath.Join(t.TempDir(), "absent"), nil))
}

// TestRunNeedsTheRealAgentsSection guards the digest: the section is part of the
// content fingerprint the stamp table publishes, so rendering with a placeholder
// would print a digest no install ever writes.
func TestRunNeedsTheRealAgentsSection(t *testing.T) {
	err := run(t.TempDir(), t.TempDir())
	assert.ErrorContains(t, err, "agents-section.md")
}

func TestFirstSentence(t *testing.T) {
	assert.Equal(t, "One claim.", firstSentence("One claim. Then the trigger text."))
	assert.Equal(t, "No sentence break", firstSentence("No sentence break"))
	assert.Equal(t, "Ends in a period.", firstSentence("Ends in a period."))
	assert.Equal(t, ". Leading break is not a split", firstSentence(". Leading break is not a split"))
}
