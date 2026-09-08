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

// skillCatalog is the same embedded catalog the generator renders from, so a test asserting what
// pages exist is asserting against the bodies a build actually ships. It replaced a relative path
// into another command's directory, which only worked because the assets were not importable.
func skillCatalog() *agent.Catalog { return agent.Default(0) }

// generate renders the whole reference into a fresh directory. It drives the real
// embedded bodies rather than a fixture on purpose: the pages exist so a skill
// edit cannot leave the documentation describing a version nobody installs, and a
// fixture would give that guarantee about the fixture.
func generate(t *testing.T) string {
	t.Helper()
	out := t.TempDir()
	require.NoError(t, run(out))
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

	defs, err := skillCatalog().EmbeddedSkills()
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
	assert.Equal(t, "internal/agent/skills/magus-query/SKILL.md", fm.GeneratedFrom)
	assert.Equal(t, []string{"agents", "skills", "magus-query"}, fm.Tags)
	assert.True(t, strings.HasSuffix(fm.Description, "."), "the description is trimmed to its opening claim: %q", fm.Description)
	assert.NotContains(t, fm.Description, "Use INSTEAD of Grep", "the trigger text does not belong in a frontmatter description")

	for _, want := range []string{
		"## What an installed copy carries",
		"| `skill-content` | `",
		"| `skill-variant` | `full` |",
		"## The two forms",
		// One tab strip, two panels, and no script: the reader flips between the
		// forms in place rather than scrolling from one to the other.
		`<article class="landing-tabs">`,
		`<input type="radio" name="magus-query-variant" id="magus-query-tab-short" checked>`,
		`<label for="magus-query-tab-short">Short form</label>`,
		`<input type="radio" name="magus-query-variant" id="magus-query-tab-full">`,
		`<label for="magus-query-tab-full">Full form</label>`,
	} {
		assert.Contains(t, body, want)
	}
	assert.Equal(t, 2, strings.Count(body, `<section class="landing-tabpanel">`), "one panel per form")
	assert.NotContains(t, body, "<script", "the toggle is CSS on a checked radio; the site ships no page script")
	// The radio group is named per skill. Two strips sharing a group name would fight
	// the moment anything assembles these pages into one document.
	assert.NotContains(t, body, `name="variant"`)
	// The two byte counts are stated as facts; the SSG turns them into the ratio
	// the page's prose points at.
	assert.Regexp(t, `(?m)^skill_full_bytes: \d+$`, body)
	assert.Regexp(t, `(?m)^skill_short_bytes: \d+$`, body)
}

// TestStampTableStopsAtTheBody guards writeStampTable's scan, which walks the
// stamped frontmatter line by line. The description is the page's subtitle
// already, and the body below it is not frontmatter at all - either leaking into
// the table would publish prose as a stamp field.
func TestStampTableStopsAtTheBody(t *testing.T) {
	body := page(t, generate(t), "magus-query.md")
	table := body[strings.Index(body, "## What an installed copy carries"):]
	table = table[:strings.Index(table, "## The two forms")]

	assert.NotContains(t, table, "| `name` |")
	assert.NotContains(t, table, "| `description` |")
	assert.NotContains(t, table, "| `---` |")
}

// A renamed skill's old page URL is not kept alive. Renaming one breaks that link,
// deliberately: magus is pre-1.0 and a redirect list is a second place the name lives.
func TestARenamedSkillPageCarriesNoRedirect(t *testing.T) {
	out := generate(t)

	assert.NotContains(t, page(t, out, "magus-docs-lookup.md"), "aliases:")
	assert.NotContains(t, page(t, out, "magus-query.md"), "aliases:")
}

// TestIndexCardsEverySkill checks what the index exists for: one card per skill,
// each reaching its page, and a total that covers all of them - the choice between
// the two forms is meant to be made on measured bytes, so a total that misses
// a skill misprices it.
func TestIndexCardsEverySkill(t *testing.T) {
	out := generate(t)
	index := page(t, out, "index.md")

	defs, err := skillCatalog().EmbeddedSkills()
	require.NoError(t, err)
	for _, d := range defs {
		assert.Contains(t, index, `<a class="landing-card" href="`+d.Name+`/">`, "no index card for %s", d.Name)
	}
	assert.Equal(t, len(defs), strings.Count(index, `class="landing-card"`), "one card per skill, no more")
	assert.Contains(t, index, "All "+strconv.Itoa(len(defs))+" together are ")

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

// The digest guard that stood here is GONE, and deliberately: it checked that run refused a -src
// directory holding no agents-section.md, because the section feeds the content fingerprint the
// stamp table publishes and a placeholder would print a digest no install ever writes. The section
// is embedded in internal/agent now and arrives with the catalog, so there is no longer a wrong
// one to pass. The move deleted the failure mode rather than the check for it.

func TestFirstSentence(t *testing.T) {
	assert.Equal(t, "One claim.", firstSentence("One claim. Then the trigger text."))
	assert.Equal(t, "No sentence break", firstSentence("No sentence break"))
	assert.Equal(t, "Ends in a period.", firstSentence("Ends in a period."))
	assert.Equal(t, ". Leading break is not a split", firstSentence(". Leading break is not a split"))
}
