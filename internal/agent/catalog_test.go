package agent

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"text/template"
	"text/template/parse"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// TestLocalSkillNameIsReserved keeps magus out of the one name a workspace is
// told it owns.
//
// The magus-workspace-rules skill teaches workspaces to put their own rules in a skill
// magus does not ship, and names magus-local-development as the convention. Shipping a
// skill by that name later would not conflict loudly: install --force writes
// the shipped body straight over the workspace's file, on every machine, at
// once. Someone would have to reconstruct the rules from git history, if they
// were committed at all.
func TestLocalSkillNameIsReserved(t *testing.T) {
	for _, source := range skillSources {
		assert.NotEqual(t, LocalSkillName, source.name,
			"%q is reserved for a workspace's own rules; magus shipping a skill by that name would overwrite them on the next install --force", LocalSkillName)
	}
}

// TestFullTwinNamesAreReserved keeps the twin namespace collision-free.
//
// FormBoth writes <name>-full beside every skill, so a shipped skill
// whose own name ends in -full would either collide with another skill's twin
// or be shadowed by its own. The collision is silent: WriteSkillTree writes
// whichever entry comes last, so one of the two skills simply vanishes from
// the installed tree with nothing reporting it. Same hazard as
// LocalSkillName, same fix: assert it rather than remember it.
func TestFullTwinNamesAreReserved(t *testing.T) {
	shipped := make(map[string]bool, len(skillSources))
	for _, source := range skillSources {
		shipped[source.name] = true
	}
	for _, source := range skillSources {
		assert.Falsef(t, IsFullTwinName(source.name),
			"%q ends in %q, which is the reserved suffix for FormBoth's always-full twin; rename the skill",
			source.name, fullTwinSuffix)
		assert.Falsef(t, shipped[FullTwinName(source.name)],
			"%q collides with the twin FormBoth writes for %q; one of the two would silently overwrite the other",
			FullTwinName(source.name), source.name)
	}
}

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	files := make(fstest.MapFS, len(skillSources))
	for _, source := range skillSources {
		files[source.bodyPath] = &fstest.MapFile{Data: []byte("# " + source.name + "\n")}
	}
	return NewCatalog(files, "## Magus\n\nUse the graph.\n", 6)
}

func TestCatalogInstallsAndVerifiesSkillTree(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	writeTestHarness(t, dir)
	written, _, err := catalog.WriteSkillTree(dir, ".agents/skills", false, FormFull)
	require.NoError(t, err)
	require.Len(t, written, len(skillSources), "a full install writes one file per skill and no twins")

	body, err := os.ReadFile(filepath.Join(dir, ".agents/skills", anchorSkillRel))
	require.NoError(t, err)
	assert.Contains(t, string(body), "license: "+skillLicense)
	assert.Contains(t, string(body), "skill-content: "+catalog.SkillDigest("magus-query"))

	statuses := catalog.CheckStatuses(context.Background(), dir, "test-host")
	require.Len(t, statuses, 1)
	assert.Equal(t, ".agents/skills", statuses[0].Location)
	assert.True(t, statuses[0].Installed)
	assert.False(t, statuses[0].Stale)

	stale := strings.Replace(string(body), "skill-content: "+catalog.SkillDigest("magus-query"), "skill-content: 000000000000", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".agents/skills", anchorSkillRel), []byte(stale), 0o644))
	assert.True(t, catalog.CheckStatuses(context.Background(), dir, "test-host")[0].Stale)
}

func TestCheckStatusesDoesNotTreatHandAuthoredSkillDirAsInstall(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	writeTestHarness(t, dir)

	local := filepath.Join(dir, ".agents/skills", LocalSkillName)
	require.NoError(t, os.MkdirAll(local, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("---\nname: "+LocalSkillName+"\n---\nour rules\n"), 0o644))

	assert.Empty(t, catalog.CheckStatuses(context.Background(), dir, "test-host"), "a descriptor path with only hand-authored skills is not an installed generated tree")

	oldInstall := filepath.Join(dir, ".agents/skills", "magus-run")
	require.NoError(t, os.MkdirAll(oldInstall, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(oldInstall, "SKILL.md"), []byte("---\nname: magus-run\n---\nold generated install\n"), 0o644))

	statuses := catalog.CheckStatuses(context.Background(), dir, "test-host")
	require.Len(t, statuses, 1)
	assert.Equal(t, ".agents/skills", statuses[0].Location)
	assert.True(t, statuses[0].Stale)
	assert.Contains(t, statuses[0].Detail, "missing "+anchorSkillRel)
}

// TestCheckStatusesIgnoresASkillMagusDidNotWrite pins the other half of the promise
// LocalSkillName makes. A workspace is told to put its own rules in a skill beside the
// installed ones, and that file carries no stamp, so grading it reported drift on every
// run with a remedy that could not work, since install writes only the names magus ships
// and would never stamp it.
func TestCheckStatusesIgnoresASkillMagusDidNotWrite(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	writeTestHarness(t, dir)
	_, _, err := catalog.WriteSkillTree(dir, ".agents/skills", false, FormFull)
	require.NoError(t, err)
	require.False(t, catalog.CheckStatuses(context.Background(), dir, "test-host")[0].Stale)

	local := filepath.Join(dir, ".agents/skills", LocalSkillName)
	require.NoError(t, os.MkdirAll(local, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(local, "SKILL.md"), []byte("---\nname: "+LocalSkillName+"\n---\nour rules\n"), 0o644))

	statuses := catalog.CheckStatuses(context.Background(), dir, "test-host")
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Stale, "a hand-authored skill has no stamp to grade: %s", statuses[0].Detail)

	// Why a missing stamp cannot be the whole test: an install predating versioning has
	// no footer either, and must still read as stale.
	shippedNoFooter := filepath.Join(dir, ".agents/skills", "magus-query")
	require.NoError(t, os.WriteFile(filepath.Join(shippedNoFooter, "SKILL.md"), []byte("---\nname: magus-query\n---\nold body\n"), 0o644))
	assert.True(t, catalog.CheckStatuses(context.Background(), dir, "test-host")[0].Stale, "a shipped skill with no stamp predates versioning and is stale")
}

// TestStaleSkillDirsReportsAndPruneRemovesOnlyWhatMagusWrote pins the deletion half of the
// install contract, its opt-in, and the limit on it.
//
// Renaming a skill used to leave the old directory installed forever (still
// stamped, still loaded by the host, still teaching whatever it said the day it
// was orphaned) because install owned its writes and nothing owned its
// deletions. Pruning closes that. The stamp is what keeps it safe: a
// hand-authored skill sits in the same folder (magus-skill-authoring here, a
// workspace's own magus-local-development in a consuming repo) and is not
// magus's to delete no matter how absent it is from the catalog.
func TestStaleSkillDirsReportsAndPruneRemovesOnlyWhatMagusWrote(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	dest := ".agents/skills"
	_, _, err := catalog.WriteSkillTree(dir, dest, false, FormBoth)
	require.NoError(t, err)

	// An orphan from an earlier release: magus wrote it, so magus may remove it.
	orphan := filepath.Join(dir, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), VariantShort), 0o644))

	// A hand-authored skill beside it, and a directory that is not a skill at all.
	handAuthored := filepath.Join(dir, dest, "magus-local-development")
	require.NoError(t, os.MkdirAll(handAuthored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(handAuthored, "SKILL.md"),
		[]byte("---\nname: magus-local-development\n---\n\n# local rules\n"), 0o644))
	notASkill := filepath.Join(dir, dest, "notes")
	require.NoError(t, os.MkdirAll(notASkill, 0o755))

	// Detection first, and it must not delete: an install that did not ask for a
	// prune still reports what is stale, so the orphan stays visible.
	stale, err := catalog.StaleSkillDirs(dir, dest, FormBoth)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, stale)
	assert.DirExists(t, orphan, "detecting a stale skill must not remove it")

	removed, err := catalog.PruneSkillTree(dir, dest, FormBoth)
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, removed)

	assert.NoDirExists(t, orphan)
	assert.DirExists(t, handAuthored, "an unstamped skill is not magus's to delete")
	assert.DirExists(t, notASkill, "a directory with no SKILL.md is not a skill")
	assert.FileExists(t, filepath.Join(dir, dest, anchorSkillRel), "a shipped skill survives its own prune")

	// FormBoth writes both names, so pruning one does not eat the twins it just wrote.
	twin := filepath.Join(dir, dest, FullTwinName(skillSources[0].name))
	assert.DirExists(t, twin)

	// ... and a form that writes NO twin reports them, which is the whole point of the
	// form reaching this far. Left as a report: pruning them is the caller's ask.
	stale, err = catalog.StaleSkillDirs(dir, dest, FormShort)
	require.NoError(t, err)
	assert.Contains(t, stale, filepath.Join(dest, FullTwinName(skillSources[0].name)),
		"a twin no longer written by the selected form is an orphan")
	assert.DirExists(t, twin, "reporting still does not delete")

	// Nothing installed at all is not an error: install prunes unconditionally, and
	// a first install has nothing to prune.
	removed, err = catalog.PruneSkillTree(dir, "never/installed", FormBoth)
	require.NoError(t, err)
	assert.Empty(t, removed)
}

// TestWriteSkillTreeRejectsPathEscape guards S-3: the guard rejected an
// absolute dest or a leading "~" but not "..", so dest="../../outside" walked
// filepath.Join right out of dir. The doc comment on WriteSkillTree claims
// magus never silently writes outside the working tree; this is what makes
// that claim true rather than aspirational.
func TestWriteSkillTreeRejectsPathEscape(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()

	_, _, err := catalog.WriteSkillTree(dir, "../../outside", false, FormFull)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes the working tree")

	// An ordinary nested destination is unaffected.
	written, _, err := catalog.WriteSkillTree(dir, "nested/skills", false, FormFull)
	require.NoError(t, err)
	require.Len(t, written, len(skillSources))
}

// TestCatalogAgentsBlockIsSelfDelimitedAndStable pins what a developer pastes:
// exactly one marker pair, a stamp CheckStatuses can grade, and the same bytes
// on every call so re-running to refresh a stale block produces a clean diff.
func TestCatalogAgentsBlockIsSelfDelimitedAndStable(t *testing.T) {
	catalog := testCatalog(t)
	block := catalog.AgentsBlock()

	assert.True(t, strings.HasPrefix(block, "<!-- magus:skills:begin "))
	assert.True(t, strings.HasSuffix(block, "<!-- magus:skills:end -->\n"))
	assert.Equal(t, 1, strings.Count(block, "magus:skills:begin"))
	assert.Contains(t, block, "skill-content: "+catalog.contentDigest)
	assert.Equal(t, block, catalog.AgentsBlock(), "AgentsBlock must be byte-stable across calls")

	// The block a developer pastes is the block CheckStatuses grades: round-trip
	// it through a hand-owned AGENTS.md and it must read as current, not stale.
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Local rules\n\nkeep this\n\n"+block), 0o644))
	statuses := catalog.CheckStatuses(context.Background(), dir)
	require.Len(t, statuses, 1)
	assert.Equal(t, "AGENTS.md", statuses[0].Location)
	assert.False(t, statuses[0].Stale, statuses[0].Detail)
}

// TestFormBothWritesTwinsStampedFull pins the mixed-batch property: one FormBoth
// install produces entries of BOTH variants, so the stamp has to follow each
// entry's own Variant rather than the form that was requested. Keying off the
// request instead would stamp every twin "short", mislabelling the copy a
// delegated model was handed precisely because it needed full.
func TestFormBothWritesTwinsStampedFull(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	written, _, err := catalog.WriteSkillTree(dir, ".claude/skills", false, FormBoth)
	require.NoError(t, err)
	require.Len(t, written, 2*len(skillSources), "FormBoth writes one primary plus one twin per skill")

	primary, err := os.ReadFile(filepath.Join(dir, ".claude/skills", anchorSkillRel))
	require.NoError(t, err)
	assert.Contains(t, string(primary), "skill-variant: short")

	base := strings.TrimSuffix(anchorSkillRel, "/SKILL.md")
	twin, err := os.ReadFile(filepath.Join(dir, ".claude/skills", FullTwinName(base), "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(twin), "skill-variant: full",
		"a twin inside a FormBoth install must stamp itself full")
	// One source body, so both still report the same digest and go stale together.
	assert.Contains(t, string(twin), "skill-content: "+catalog.SkillDigest("magus-query"))
}

func TestCatalogSkillTarIsByteStable(t *testing.T) {
	catalog := testCatalog(t)
	a, err := catalog.SkillTar(".claude/skills", FormFull)
	require.NoError(t, err)
	b, err := catalog.SkillTar(".claude/skills", FormFull)
	require.NoError(t, err)
	assert.Equal(t, a, b, "SkillTar must be reproducible; no embedded timestamps in the body")
	assert.NotEmpty(t, a)
}

func TestCatalogSkillBytesByName(t *testing.T) {
	catalog := testCatalog(t)
	body, err := catalog.SkillBytes("magus-architecture-review", FormFull)
	require.NoError(t, err)
	assert.Contains(t, string(body), "name: magus-architecture-review")
	assert.Contains(t, string(body), "skill-content: "+catalog.SkillDigest("magus-architecture-review"))

	ultra, err := catalog.SkillBytes("magus-multi-agent", FormFull)
	require.NoError(t, err)
	assert.Contains(t, string(ultra), "name: magus-multi-agent")

	_, err = catalog.SkillBytes("does-not-exist", FormFull)
	assert.ErrorContains(t, err, "unknown skill")
}

func TestTestDesignFullVariantAddsDelegatedWorkflow(t *testing.T) {
	catalog := Default(7)
	short, err := catalog.SkillBytes("magus-test-design", FormShort)
	require.NoError(t, err)
	full, err := catalog.SkillBytes("magus-test-design", FormFull)
	require.NoError(t, err)

	assert.Greater(t, len(full), len(short))
	assert.NotContains(t, string(short), "recommendation is **provisional**")
	assert.Contains(t, string(full), "recommendation is **provisional**")
}

// TestMustSkillRefusesWhatMagusDoesNotShip is what makes a SkillRef worth more than a string. A
// prompt naming a skill nobody can load still renders perfectly, so the only place to catch it is
// where the reference is made.
func TestMustSkillRefusesWhatMagusDoesNotShip(t *testing.T) {
	assert.Equal(t, SkillRef("magus-query"), MustSkill("magus-query"))
	assert.Panics(t, func() { MustSkill("magus-not-a-real-skill") })
	// A name magus USED to ship is refused like any other unknown: it still resolves for an
	// already-installed copy, which is precisely why a stale one would go unnoticed here.
	assert.Panics(t, func() { MustSkill("magus-architecture") })
}

func TestMultiAgentVariantsKeepTheSameSafetyContract(t *testing.T) {
	catalog := Default(7)
	full, err := catalog.SkillBytes("magus-multi-agent", FormFull)
	require.NoError(t, err)
	short, err := catalog.SkillBytes("magus-multi-agent", FormShort)
	require.NoError(t, err)

	for _, body := range [][]byte{full, short} {
		text := string(body)
		assert.Contains(t, text, "acceptance criteria")
		assert.Contains(t, text, "magus affected <target> --plan")
		assert.Contains(t, text, "magus status --watch=15s")
		assert.Contains(t, text, "Nesting is allowed when the host supports it")
		assert.NotContains(t, text, "{{")
	}
	assert.Contains(t, string(full), "natural evolution of loop engineering")
	assert.NotContains(t, string(short), "natural evolution of loop engineering")
	assert.LessOrEqual(t, len(short)*5, len(full)*4,
		"the short form must remove at least one fifth of the full skill while preserving its safety contract")
}

// TestSkillDigestIsPerSkill pins the granularity, which is the whole point of the
// digest: a catalog-wide one restamped all 26 installed files and all 16 reference
// pages whenever any skill changed, so a diff could not show which skill moved.
// TestPlanSkillTreeMatchesTheWriter keeps --dry-run honest: a plan that named
// different paths than the run would be worse than no plan, because it would be
// believed.
func TestPlanSkillTreeMatchesTheWriter(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()

	planned, err := catalog.PlanSkillTree(dir, ".claude/skills", FormBoth)
	require.NoError(t, err)
	require.NotEmpty(t, planned)

	written, _, err := catalog.WriteSkillTree(dir, ".claude/skills", false, FormBoth)
	require.NoError(t, err)

	assert.Equal(t, written, planned, "the plan must name exactly what the writer writes")
}

// TestPlanSkillTreeWritesNothing is the property the flag exists for.
func TestPlanSkillTreeWritesNothing(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()

	_, err := catalog.PlanSkillTree(dir, ".claude/skills", FormShort)
	require.NoError(t, err)

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "a plan must not create the destination it describes")
}

func TestCheckDestinationRefusesEscapes(t *testing.T) {
	dir := t.TempDir()

	assert.NoError(t, checkDestination(dir, ".claude/skills"))
	assert.NoError(t, checkDestination(dir, "nested/deeper/skills"))

	assert.Error(t, checkDestination(dir, "/etc/skills"), "an absolute path is outside the tree")
	assert.Error(t, checkDestination(dir, "~/skills"), "a home-relative path is outside the tree")
	// The one IsAbs and the ~ check both miss: it is relative and does not start
	// with ~, and only cleaning the joined path reveals where it lands.
	assert.Error(t, checkDestination(dir, "../../outside"), "a traversal escapes the tree")
}

// TestSkillTreeRefusesASymlinkedDestination pins the delete side of the guard.
// Cleaning a path is lexical and sees no symlink, so a destination whose parent
// is a link cleaned fine and landed a write, and then PruneSkillTree's RemoveAll,
// wherever the link pointed.
func TestSkillTreeRefusesASymlinkedDestination(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	outside := t.TempDir()

	// A stamped skill outside the tree: magus wrote it, so a prune reaching it
	// would delete it.
	victim := filepath.Join(outside, "skills", "magus-retired")
	require.NoError(t, os.MkdirAll(victim, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(victim, "SKILL.md"),
		catalog.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), VariantShort), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, ".agents")))

	dest := filepath.Join(".agents", "skills")
	err := checkDestination(dir, dest)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")

	_, _, err = catalog.WriteSkillTree(dir, dest, true, FormFull)
	require.Error(t, err, "a write through a symlinked component lands outside the tree")
	_, err = catalog.PlanSkillTree(dir, dest, FormFull)
	require.Error(t, err, "a plan must refuse what the writer refuses")

	// An install writes before it prunes, so the refused write is what keeps the
	// delete from running.
	assert.DirExists(t, victim, "nothing outside the tree may be removed")

	// A destination with no symlink in it still resolves.
	assert.NoError(t, checkDestination(dir, ".claude/skills"))
}

func TestInstalledSkillNamesListsOnlyMagusDirs(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	for _, name := range []string{"magus-run", "magus-query", "magus-query-full", "notes", "README"} {
		require.NoError(t, os.MkdirAll(filepath.Join(dir, name), 0o755))
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus-not-a-dir"), []byte("x"), 0o644))

	// Sorted, so a verify run reports the same order every time; a plain file is
	// not a skill however it is named.
	assert.Equal(t, []string{"magus-query", "magus-query-full", "magus-run"}, catalog.installedSkillNames(dir))
	assert.Nil(t, catalog.installedSkillNames(filepath.Join(dir, "does-not-exist")))
}

func TestBaseSkillNameResolvesTwins(t *testing.T) {
	assert.Equal(t, "magus-run", baseSkillName("magus-run"))
	assert.Equal(t, "magus-run", baseSkillName(FullTwinName("magus-run")))
}

func TestSkillDigestIsPerSkill(t *testing.T) {
	catalog := testCatalog(t)

	first := catalog.SkillDigest("magus-query")
	second := catalog.SkillDigest("magus-run")
	assert.NotEmpty(t, first)
	assert.NotEqual(t, first, second, "two skills must not share a digest, or one edit restamps both")

	// A twin is rendered from its primary's body, so the two report the same
	// value and go stale together: the invariant StampSkill's comment names.
	assert.Equal(t, first, catalog.SkillDigest(FullTwinName("magus-query")))

	assert.Equal(t, "unreadable", catalog.SkillDigest("magus-not-a-skill"))
}

func TestCatalogRenderAndStamp(t *testing.T) {
	catalog := testCatalog(t)
	rendered := catalog.RenderSkill(AgentSkill{Name: "magus-test", Description: "Does one thing.", Body: "# Test"})
	assert.Equal(t, "---\nname: magus-test\ndescription: \"Does one thing.\"\n---\n\n# Test\n", string(rendered))
	stamped := string(catalog.StampSkill("magus-test", rendered, VariantFull))
	assert.Contains(t, stamped, "metadata:\n  source: magus\n")
	assert.Equal(t, 1, strings.Count(stamped, "generated by: magus agent install"))
}

// TestApplyVariantKeepsBothPermutationsWellFormed pins the branching contract.
// TestApplyVariantResolvesCommandsThroughTheRegistry pins the reason skill prose
// calls {{cmd}} instead of typing a command path: a verb that moves must break an
// install rather than ship a sentence naming a command nobody has.
func TestApplyVariantResolvesCommandsThroughTheRegistry(t *testing.T) {
	got, err := applyVariant("s", `run {{cmd "agent harness verify"}} first`, VariantShort)
	require.NoError(t, err)
	assert.Equal(t, "run magus agent harness verify first", got)

	// The failure this exists for, demonstrated against a command that WAS real: `agent
	// improve` was retired, and every skill naming it failed the install until it was
	// repointed, which is the whole reason prose resolves instead of retyping.
	_, err = applyVariant("s", `run {{cmd "agent improve"}} first`, VariantShort)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `no command "agent improve"`)
}

// TestApplyVariantRendersCommandsForTheReadersPath keeps an installed skill free of
// this process's argv0. A skill is read in another checkout, where ./magus is a path
// to something else or to nothing.
func TestApplyVariantRendersCommandsForTheReadersPath(t *testing.T) {
	hint.ResolveBinaryNameFrom("./magus")
	t.Cleanup(func() { hint.ResolveBinaryNameFrom(hint.DefaultBinaryName) })
	require.Equal(t, "./magus", hint.BinaryName(), "the test needs the argv0 spelling actually set")

	got, err := applyVariant("s", `{{cmd "doctor"}}`, VariantShort)
	require.NoError(t, err)
	assert.Equal(t, "magus doctor", got)
}

func TestApplyVariantKeepsBothPermutationsWellFormed(t *testing.T) {
	body := "Do the thing{{if .Full}} - because the alternative silently corrupts output{{end}}.\n" +
		"\n{{if .Full}}A whole paragraph of rationale.{{end}}\n\nNext step."

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Do the thing - because the alternative silently corrupts output.\n\nA whole paragraph of rationale.\n\nNext step.", full)
	assert.NotContains(t, full, "{{", "no template action may reach an installed file")

	short, err := applyVariant("s", body, VariantShort)
	require.NoError(t, err)
	assert.Equal(t, "Do the thing.\n\nNext step.", short,
		"the full-only branches go, the sentence still ends in a period, and the emptied paragraph leaves no blank-line run")
}

// TestApplyVariantDoesNotTouchUnelidedContent ensures rendering only evaluates
// template actions and never rewrites ordinary text.
func TestApplyVariantDoesNotTouchUnelidedContent(t *testing.T) {
	body := "Never run `git checkout .` or `git clean`{{if .Full}} - it destroys untracked work{{end}}.\n" +
		"\nKeep ( these ) spaces and this : colon."

	for _, v := range []Variant{VariantFull, VariantShort} {
		got, err := applyVariant("s", body, v)
		require.NoError(t, err)
		assert.Contains(t, got, "`git checkout .`", "%s must not rewrite an unelided command", v)
		assert.Contains(t, got, "( these ) spaces and this : colon.", "%s must not retouch unelided punctuation", v)
	}
}

// TestApplyVariantSwapsTwoWordings pins the else idiom that lets both
// forms express one instruction at different lengths.
func TestApplyVariantSwapsTwoWordings(t *testing.T) {
	body := "Scope explicitly{{if .Full}}, because a bare command acts on whichever project holds " +
		"your current directory and therefore means something different depending on where you " +
		"happen to be standing{{else}} (magus is CWD-relative){{end}}."

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Scope explicitly, because a bare command acts on whichever project holds your "+
		"current directory and therefore means something different depending on where you happen "+
		"to be standing.", full)

	short, err := applyVariant("s", body, VariantShort)
	require.NoError(t, err)
	assert.Equal(t, "Scope explicitly (magus is CWD-relative).", short)

	for _, got := range []string{full, short} {
		assert.NotContains(t, got, "{{", "no template action may reach an installed file")
	}
}

// TestApplyVariantTerseAloneIsShortOnly covers a short-only branch.
func TestApplyVariantTerseAloneIsShortOnly(t *testing.T) {
	body := "Step one.{{if .Short}} See the docs for why.{{end}}"

	full, err := applyVariant("s", body, VariantFull)
	require.NoError(t, err)
	assert.Equal(t, "Step one.", full)

	short, err := applyVariant("s", body, VariantShort)
	require.NoError(t, err)
	assert.Equal(t, "Step one. See the docs for why.", short)
}

// TestApplyVariantRefusesMalformedTemplate keeps a malformed body from installing.
func TestApplyVariantRefusesMalformedTemplate(t *testing.T) {
	for name, body := range map[string]string{
		"missing end":   "Do it{{if .Full}} because.",
		"unknown field": "Do it {{.Unknown}}.",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := applyVariant("magus-x", body, VariantShort)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "magus-x", "the error must name the skill that is malformed")
		})
	}
}

// TestSkillTemplatesUseOnlyBranching keeps the forms from diverging
// structurally. The old marker scheme could only swap spans, so "both
// forms describe the same behaviour" was true by construction; with a
// general template engine it has to be asserted.
func TestSkillTemplatesUseOnlyBranching(t *testing.T) {
	for _, source := range skillSources {
		t.Run(source.name, func(t *testing.T) {
			// Read through the embedded FS rather than off disk: the assets live in this
			// package now, so the test reads exactly what a build ships.
			body, err := skillFS.ReadFile(source.bodyPath)
			require.NoError(t, err)
			tmpl, err := template.New(source.name).Funcs(skillFuncs).Parse(string(body))
			require.NoError(t, err)
			require.NoError(t, validateTemplateNode(tmpl.Root))
		})
	}
}

func validateTemplateNode(node parse.Node) error {
	switch node := node.(type) {
	case *parse.ListNode:
		for _, child := range node.Nodes {
			if err := validateTemplateNode(child); err != nil {
				return err
			}
		}
		return nil
	case *parse.TextNode:
		return nil
	case *parse.IfNode:
		if err := validateBranchPipe(node.Pipe); err != nil {
			return err
		}
		if err := validateTemplateNode(node.List); err != nil {
			return err
		}
		if node.ElseList != nil {
			return validateTemplateNode(node.ElseList)
		}
		return nil
	case *parse.ActionNode:
		return validateActionPipe(node.Pipe)
	default:
		return fmt.Errorf("template node %T is not permitted", node)
	}
}

func validateBranchPipe(pipe *parse.PipeNode) error {
	if len(pipe.Cmds) != 1 {
		return fmt.Errorf("branch pipeline has %d commands", len(pipe.Cmds))
	}
	args := pipe.Cmds[0].Args
	if len(args) == 1 {
		field, ok := args[0].(*parse.FieldNode)
		if ok && len(field.Ident) == 1 && (field.Ident[0] == "Full" || field.Ident[0] == "Short") {
			return nil
		}
	}
	if len(args) == 2 {
		field, fieldOK := args[0].(*parse.FieldNode)
		_, stringOK := args[1].(*parse.StringNode)
		if fieldOK && stringOK && len(field.Ident) == 1 && field.Ident[0] == "Is" {
			return nil
		}
	}
	return fmt.Errorf("branch must be .Full, .Short, or .Is \"name\"")
}

func validateActionPipe(pipe *parse.PipeNode) error {
	if len(pipe.Cmds) != 1 {
		return fmt.Errorf("template action must be one bare field, a string, or a registered lookup")
	}
	args := pipe.Cmds[0].Args
	// A registered lookup: {{cmd "agent improve"}}. Permitted because it resolves an
	// identifier from a registry and renders the SAME text in both forms, so it cannot
	// make the two diverge, which is the property this file exists to assert. A function
	// added to skillFuncs inherits that obligation: render variant-independently, or the
	// forms stop describing one behaviour and nothing here would catch it.
	if len(args) == 2 {
		ident, identOK := args[0].(*parse.IdentifierNode)
		_, stringOK := args[1].(*parse.StringNode)
		if identOK && stringOK {
			if _, ok := skillFuncs[ident.Ident]; ok {
				return nil
			}
			return fmt.Errorf("template calls %q, which is not a registered skill function", ident.Ident)
		}
	}
	if len(args) == 1 {
		switch node := args[0].(type) {
		case *parse.StringNode:
			return nil
		case *parse.FieldNode:
			if len(node.Ident) == 1 {
				return nil
			}
		}
	}
	return fmt.Errorf("template action must be one bare field, a string, or a registered lookup")
}

// TestStampNamesTheVariantButSharesTheDigest pins the versioning property the
// single-source design exists for: both forms come from one body, so they
// must report the same content digest and go stale together. A per-variant digest
// would let a short install look current against a source its sibling outgrew.
func TestStampNamesTheVariantButSharesTheDigest(t *testing.T) {
	catalog := testCatalog(t)
	full, err := catalog.SkillBytes("magus-vcs-hygiene", FormFull)
	require.NoError(t, err)
	short, err := catalog.SkillBytes("magus-vcs-hygiene", FormShort)
	require.NoError(t, err)

	assert.Contains(t, string(full), "skill-variant: full")
	assert.Contains(t, string(short), "skill-variant: short")

	digest := footerDigestRe.FindStringSubmatch(string(full))
	require.Len(t, digest, 2)
	assert.Contains(t, string(short), "skill-content: "+digest[1],
		"one source body, one digest - the forms version together")
}

// TestGradeDestReportsAnUnreadableSkill: a skill magus cannot read is one it cannot
// vouch for, and the loop used to skip it and grade the location up to date. chmod 000
// any installed SKILL.md and doctor said everything was current, which is the one answer
// that stops a reader looking.
func TestGradeDestReportsAnUnreadableSkill(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	dest := ".claude/skills"
	_, _, err := catalog.WriteSkillTree(dir, dest, false, FormBoth)
	require.NoError(t, err)

	blocked := filepath.Join(dir, dest, "magus-run", "SKILL.md")
	require.NoError(t, os.Chmod(blocked, 0o000))
	t.Cleanup(func() { _ = os.Chmod(blocked, 0o644) })

	got := catalog.gradeDest(dir, HarnessSkillLocation{ID: "test", Path: dest, Form: FormBoth})

	assert.True(t, got.Stale, "an unreadable installed skill graded as current")
	assert.Contains(t, got.Detail, "magus-run")
	assert.Contains(t, got.Detail, "cannot read it")
}

// TestGradeDestReportsEveryReasonNotJustTheFirst pins the fix for gradeDest
// returning on the first offender. An orphaned directory that sorts before
// magus-query alphabetically used to short-circuit the loop and hide the
// version/schema mismatch on magus-query entirely: the reason an operator
// actually needs, since "not part of the form" tells them nothing about how
// stale the binary is.
func TestGradeDestReportsEveryReasonNotJustTheFirst(t *testing.T) {
	catalog := testCatalog(t)
	dir := t.TempDir()
	dest := ".claude/skills"
	_, _, err := catalog.WriteSkillTree(dir, dest, false, FormFull)
	require.NoError(t, err)

	// Sorts before every shipped name, so the old first-offender return hit this
	// one and never reached magus-query.
	orphan := filepath.Join(dir, dest, "magus-aaa-orphan")
	require.NoError(t, os.MkdirAll(orphan, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(orphan, "SKILL.md"),
		catalog.StampSkill("magus-aaa-orphan", []byte("---\nname: magus-aaa-orphan\n---\n\n# gone\n"), VariantShort), 0o644))

	stale := "---\nname: magus-query\n---\nbody\n<!-- generated by: magus agent install; agent-skill-version: 23; knowledge-schema-version: 7; skill-content: deadbeefcafe; skill-variant: full -->\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, dest, "magus-query", "SKILL.md"), []byte(stale), 0o644))

	got := catalog.gradeDest(dir, HarnessSkillLocation{ID: "test", Path: dest, Form: FormFull})

	require.True(t, got.Stale)
	assert.Contains(t, got.Detail, "magus-aaa-orphan", "the orphan finding must still be reported")
	wantVersion := fmt.Sprintf("stale (skill v23/schema v7; binary v%d/schema v%d)", SkillVersion, catalog.schemaVersion)
	assert.Contains(t, got.Detail, wantVersion,
		"the version/schema mismatch must never be masked by an earlier, alphabetically-sorted finding")
	assert.Less(t, strings.Index(got.Detail, "v23/schema v7"), strings.Index(got.Detail, "magus-aaa-orphan"),
		"a version/schema mismatch sorts ahead of a lesser finding")
}

// TestGradeStampNeverMatchesTwoUnreadableDigests pins defect 3: unreadableDigest
// is what SkillDigest and computeContentDigest report when THIS binary could not
// hash its own embedded source. It is a failure marker, not a content value, so
// it must never satisfy gradeStamp's equality check. Before this fix, an
// installed file stamped by an equally broken magus (skill-content: unreadable)
// compared equal to a currently-broken binary's own unreadable digest and graded
// the pair up to date: two catalogs that both failed to hash their content,
// each vouching for the other.
func TestGradeStampNeverMatchesTwoUnreadableDigests(t *testing.T) {
	catalog := testCatalog(t)
	name := skillSources[0].name
	// Simulate THIS binary also failing to hash its own source for this skill.
	catalog.skillDigests[name] = unreadableDigest

	body := fmt.Sprintf("---\nname: %s\n---\nbody\n<!-- generated by: magus agent install; agent-skill-version: %d; knowledge-schema-version: %d; skill-content: %s; skill-variant: short -->\n",
		name, SkillVersion, catalog.schemaVersion, unreadableDigest)

	got := catalog.gradeStamp(".claude/skills", "reinstall-cmd", body, catalog.SkillDigest(name))

	assert.True(t, got.Stale, "two catalogs that both failed to hash content must never grade as matching")
	assert.Contains(t, got.Detail, "unreadable")
}

// offeredWorkspace installs the full form into the test harness's skill path and adds
// one hand-authored skill beside it.
func offeredWorkspace(t *testing.T) (*Catalog, string) {
	t.Helper()
	catalog := testCatalog(t)
	dir := t.TempDir()
	writeTestHarness(t, dir)
	_, _, err := catalog.WriteSkillTree(dir, ".agents/skills", false, FormFull)
	require.NoError(t, err)
	writeLocalSkill(t, dir, "acme-rules", "---\nname: acme-rules\ndescription: \"Our house rules.\"\n---\n\n# Acme\n\nShip on Fridays.\n")
	return catalog, dir
}

func writeLocalSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, ".agents/skills", name, "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
}

func TestOfferedListsBothShippedFormsAndLocalSkills(t *testing.T) {
	catalog, dir := offeredWorkspace(t)

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{})
	require.NoError(t, err)
	require.Len(t, got, 2*len(skillSources)+1)

	var names []string
	for _, s := range got {
		names = append(names, s.Name+"/"+s.Form)
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		assert.True(t, prev.Name < cur.Name || (prev.Name == cur.Name && prev.Form == "short" && cur.Form == "full"),
			"out of order at %d: %v", i, names)
	}

	idx := slices.IndexFunc(got, func(s types.Skill) bool { return s.Name == "acme-rules" })
	require.GreaterOrEqual(t, idx, 0)
	assert.Equal(t, types.Skill{
		Name:        "acme-rules",
		Description: "Our house rules.",
		Source:      "local",
		Form:        "full",
		Body:        "# Acme\n\nShip on Fridays.\n",
		Current:     true,
	}, got[idx])

	idx = slices.IndexFunc(got, func(s types.Skill) bool { return s.Name == "magus-query" })
	require.GreaterOrEqual(t, idx, 0)
	description := skillSources[slices.IndexFunc(skillSources, func(s skillSource) bool { return s.name == "magus-query" })].description
	assert.Equal(t, []types.Skill{
		// The install carries only the full form, so no copy of the short one exists to be current.
		{Name: "magus-query", Description: description, Source: "shipped", Form: "short", Body: "# magus-query\n" + catalog.footer("magus-query", VariantShort)},
		{Name: "magus-query", Description: description, Source: "shipped", Form: "full", Body: "# magus-query\n" + catalog.footer("magus-query", VariantFull), Current: true},
	}, got[idx:idx+2])
}

func TestOfferedBodyIsTheInstalledFileWithoutFrontmatter(t *testing.T) {
	catalog, dir := offeredWorkspace(t)
	installed, err := os.ReadFile(filepath.Join(dir, ".agents/skills", anchorSkillRel))
	require.NoError(t, err)

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Name: "magus-query", Form: FormFull})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.True(t, strings.HasSuffix(string(installed), "\n\n"+got[0].Body), "body must be the installed file after its frontmatter")
}

func TestOfferedIsNotCurrentOnceAnInstalledCopyDrifts(t *testing.T) {
	catalog, dir := offeredWorkspace(t)
	path := filepath.Join(dir, ".agents/skills", anchorSkillRel)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, append(body, "hand edit\n"...), 0o644))

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Name: "magus-query", Form: FormFull})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.False(t, got[0].Current)
}

func TestOfferedFormNarrowsShippedSkillsOnly(t *testing.T) {
	catalog, dir := offeredWorkspace(t)

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Form: FormShort})
	require.NoError(t, err)
	require.Len(t, got, len(skillSources)+1)
	for _, s := range got {
		if s.Source == "shipped" {
			assert.Equal(t, "short", s.Form, s.Name)
		} else {
			assert.Equal(t, "acme-rules", s.Name)
		}
	}

	_, err = catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Form: "medium"})
	assert.ErrorContains(t, err, `unknown skill form "medium"`)
}

func TestOfferedNameSelectsOneSkillOrNamesNearMatches(t *testing.T) {
	catalog, dir := offeredWorkspace(t)

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Name: "acme-rules"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "local", got[0].Source)

	_, err = catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Name: "magus-qery"})
	assert.EqualError(t, err, `agent: no skill named "magus-qery"; near matches: magus-query, magus-memory`)

	_, err = catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{Name: "zzzzzzzzzzzzzzzzzzzz"})
	assert.ErrorContains(t, err, "none of the")
}

func TestOfferedLocalSkillsSkipStampedLitterAndRequireADescription(t *testing.T) {
	catalog, dir := offeredWorkspace(t)
	// An orphaned install carries the stamp: StaleSkillDirs reports it, and it is not
	// the workspace's own skill.
	writeLocalSkill(t, dir, "magus-retired", "---\nname: magus-retired\ndescription: old\n---\n\nold\n<!-- "+generatedSkillMarker+" -->\n")

	got, err := catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{})
	require.NoError(t, err)
	assert.False(t, slices.ContainsFunc(got, func(s types.Skill) bool { return s.Name == "magus-retired" }))

	writeLocalSkill(t, dir, "bare", "# no frontmatter\n")
	_, err = catalog.Offered(context.Background(), dir, []string{"test-host"}, SkillQuery{})
	assert.EqualError(t, err, "agent: "+filepath.Join(".agents/skills", "bare", "SKILL.md")+" has no frontmatter description, so no host can list it")
}

// guardAdviceSkillCoverage maps each advisory the guard can emit to a token that
// must appear in some installed skill.
//
// The token is a magus surface name, not a phrase: the skill teaches the same
// thing in its own words and rewording it should not fail a gate, but dropping
// the CAPABILITY should.
var guardAdviceSkillCoverage = map[string]string{
	"update":     ":update",
	"checkpoint": "magus vcs checkpoint",
	"search":     "magus refs",
	"cwd":        "magus where",
}

// TestGuardAdviceHasSkillCoverage keeps the guard's advisories reachable on every
// host, not just the one that can inject them.
//
// An advise reaches the MODEL on Claude Code alone. Codex rejects the
// additionalContext key outright, Cursor's command surface has no channel for a
// non-denial, and OpenCode can only log one for the person. That is those hosts'
// contract and magus cannot widen it, so the guard advisory is a timelier
// delivery of guidance, never its only copy.
//
// The installed skills ARE the common channel: plain files every host reads,
// carrying no host conditionals. So anything the guard would advise has to be in
// one, or three hosts out of four never learn it. That was not true when this was
// written: the charm advisory existed with the charm appearing in no skill at all,
// while the OpenCode plugin's own comment claimed "the same guidance ships in the
// installed skills, which is why the skills and the guard say the same things".
//
// Sources rather than installed copies, because the source is what a contributor
// edits and what `magus agent install` regenerates from.
func TestGuardAdviceHasSkillCoverage(t *testing.T) {
	const embeddedSkillDir = "skills"
	entries, err := fs.ReadDir(skillFS, embeddedSkillDir)
	require.NoError(t, err, "read %s", embeddedSkillDir)

	var allBodies strings.Builder
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		body, err := fs.ReadFile(skillFS, path.Join(embeddedSkillDir, entry.Name(), "SKILL.md"))
		require.NoError(t, err, "read skill %s", entry.Name())
		allBodies.Write(body)
	}
	all := allBodies.String()

	for advisory, token := range guardAdviceSkillCoverage {
		assert.Contains(t, all, token,
			"the %s advisory teaches %q, and no skill under %s mentions it.\n"+
				"An advise reaches the model on Claude Code only, so a skill is where the other three\n"+
				"hosts learn this. Add it to the skill that owns the topic, or drop the advisory.",
			advisory, token, embeddedSkillDir)
	}
}

// TestTargetIsTheTaughtNoun pins the worst vocabulary-drift regression: the
// printed target definition and the magus-run skill's opening both teaching
// "target" as the unit of work, rather than sliding back to "operation" or
// "task" (docs/concepts/targets.md bans both as a Target substitute). This is
// deliberately narrow (a broad synonym grep false-positives too easily), so it
// only pins these two known-worst sites.
func TestTargetIsTheTaughtNoun(t *testing.T) {
	lower := strings.ToLower(types.TargetDefinition)
	assert.Contains(t, lower, "target", "TargetDefinition must teach target as the unit of work")
	assert.NotContains(t, lower, "operation", "TargetDefinition must not substitute operation for target")
	assert.NotContains(t, lower, "task", "TargetDefinition must not substitute task for target")

	data, err := fs.ReadFile(skillFS, "skills/magus-run/SKILL.md")
	require.NoError(t, err, "read magus-run SKILL.md")
	paragraphs := strings.SplitN(string(data), "\n\n", 3)
	require.GreaterOrEqual(t, len(paragraphs), 2, "SKILL.md must have an opening paragraph after its heading")
	opening := strings.ToLower(paragraphs[1])

	assert.Contains(t, opening, "target", "the magus-run skill opening must teach target as the unit of work")
	assert.NotContains(t, opening, "operation", "the magus-run skill opening must not substitute operation for target")
	// "task orchestrator" is the blessed product-positioning phrase (README.md uses it);
	// strip it before checking, so this pins task NOT being used as the taught noun
	// without banning the positioning phrase itself.
	withoutBlessedPhrase := strings.ReplaceAll(opening, "task orchestrator", "")
	assert.NotContains(t, withoutBlessedPhrase, "task",
		"the magus-run skill opening must not teach task as the unit of work (task orchestrator excepted)")
}

// vcsDriverSpellings maps each driver's Name() to how a human-facing surface may spell
// it. Unmapped names FAIL rather than pass, so adding a fifth backend forces a decision
// here instead of shipping a surface that silently covers four of five.
var vcsDriverSpellings = map[string][]string{
	"git": {"git"},
	"hg":  {"Mercurial", "hg"},
	"sl":  {"Sapling", "sl"},
	"jj":  {"Jujutsu", "jj"},
}

// TestAgentSurfaceNamesEveryVCSDriver keeps the agent surface at parity with the drivers.
//
// vcs/parity_test.go already pins parity for nineteen DRIVER METHODS across all four
// backends, so the repo has decided this matters. That enforcement stopped at the driver
// and never reached the surfaces a reader meets, and the gap was not theoretical: the
// magus-vcs-hygiene DESCRIPTION named git and only git, and a description is what a host
// matches on to decide whether to load a skill at all. An agent in a Mercurial repo about
// to run `hg purge` would never have loaded the skill that exists to stop it.
//
// The driver list is READ FROM THE SOURCE rather than restated, so this cannot drift from
// what magus actually drives.
func TestAgentSurfaceNamesEveryVCSDriver(t *testing.T) {
	names := vcsDriverNames(t)
	require.NotEmpty(t, names, "found no VCS drivers; the Name() scan below stopped matching")

	catalog, err := os.ReadFile("catalog.go")
	require.NoError(t, err)
	desc := vcsHygieneDescription(t, string(catalog))

	for _, name := range names {
		spellings, ok := vcsDriverSpellings[name]
		require.Truef(t, ok, "vcs driver %q has no entry in vcsDriverSpellings; decide how the agent surface should spell it", name)
		assert.Truef(t, containsAny(desc, spellings),
			"the magus-vcs-hygiene description never names the %q backend (any of %v), so a host will not load it for that repository", name, spellings)
	}
}

// vcsDriverNames reads each driver's Name() return straight out of vcs/*.go.
func vcsDriverNames(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(repoRoot, "vcs", "*.go"))
	require.NoError(t, err)
	re := regexp.MustCompile(`func \(v \w+VCS\) Name\(\) string\s*{\s*return "([^"]+)"`)
	var names []string
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		body, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(body), -1) {
			names = append(names, m[1])
		}
	}
	sort.Strings(names)
	return names
}

// vcsHygieneDescription pulls the one description line the skill catalog registers.
func vcsHygieneDescription(t *testing.T, catalog string) string {
	t.Helper()
	for line := range strings.SplitSeq(catalog, "\n") {
		if strings.Contains(line, `name: "magus-vcs-hygiene"`) {
			return line
		}
	}
	t.Fatal("magus-vcs-hygiene is not registered in internal/agent/catalog.go")
	return ""
}

func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(haystack, n) {
			return true
		}
	}
	return false
}
