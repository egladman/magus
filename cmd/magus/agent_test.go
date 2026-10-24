package main

import (
	"archive/tar"
	"cmp"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/script"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSkillsAreWellFormed(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	require.Len(t, defs, 16)
	for _, def := range defs {
		skill, err := agentSkills.Render(def, types.SkillVariantFull)
		require.NoError(t, err)
		assert.NotEmpty(t, skill.Name)
		assert.NotEmpty(t, skill.Description)
		assert.NotEmpty(t, skill.Body)
		for _, r := range agentSkills.RenderSkill(skill) {
			require.LessOrEqual(t, r, byte(127), "%s must be plain ASCII", skill.Name)
		}
	}
}

func TestRenderAgentSkill(t *testing.T) {
	got := string(agentSkills.RenderSkill(agent.AgentSkill{Name: "magus-test", Description: "Does one thing.", Body: "# Test\n\nDo it."}))
	want := "---\nname: magus-test\ndescription: \"Does one thing.\"\n---\n\n# Test\n\nDo it.\n"
	assert.Equal(t, want, got)
}

// TestAgentsSectionIsPlainASCII holds the AGENTS.md block to the same message rule.
func TestAgentsSectionIsPlainASCII(t *testing.T) {
	require.NotEmpty(t, agentSkills.Section())
	for _, r := range agentSkills.Section() {
		require.LessOrEqual(t, r, rune(127), "agents-section.md must be plain ASCII")
	}
}

func TestInstallSkillTreeWritesStampedFiles(t *testing.T) {
	dir := t.TempDir()
	written, _, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, types.SkillFormFull)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	const rel = ".claude/skills/magus-query/SKILL.md"
	skillPath := filepath.Join(dir, rel)
	assert.Contains(t, written, rel)

	body, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	skills, err := agentSkills.RenderedSkills(types.SkillFormFull)
	require.NoError(t, err)
	var query agent.AgentSkill
	for _, skill := range skills {
		if skill.Name == "magus-query" {
			query = skill
			break
		}
	}
	require.NotEmpty(t, query.Name)
	assert.Equal(t, string(agentSkills.StampSkill(query.Name, agentSkills.RenderSkill(query), types.SkillVariantFull)), string(body))
}

// TestInstallSkillTreeDestinationsShareBytes proves the host-agnostic promise:
// every destination receives byte-identical files, only the directory differs.
func TestInstallSkillTreeDestinationsShareBytes(t *testing.T) {
	dir := t.TempDir()
	dests := []string{"first-harness/skills", "second-harness/skills"}
	for _, dest := range dests {
		_, _, err := agentSkills.WriteSkillTree(dir, dest, false, types.SkillFormFull)
		require.NoError(t, err)
	}
	first, err := os.ReadFile(filepath.Join(dir, dests[0], "magus-query/SKILL.md"))
	require.NoError(t, err)
	for _, dest := range dests[1:] {
		other, err := os.ReadFile(filepath.Join(dir, dest, "magus-query/SKILL.md"))
		require.NoError(t, err)
		assert.Equal(t, string(first), string(other), "destination %s must receive identical bytes", dest)
	}
}

func TestInstallSkillTreeRefusesThenForces(t *testing.T) {
	dir := t.TempDir()
	_, _, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, types.SkillFormFull)
	require.NoError(t, err)

	_, _, err = agentSkills.WriteSkillTree(dir, ".claude/skills", false, types.SkillFormFull)
	require.Error(t, err, "a second install without --force must refuse")
	assert.Contains(t, err.Error(), "already exists")

	_, _, err = agentSkills.WriteSkillTree(dir, ".claude/skills", true, types.SkillFormFull)
	require.NoError(t, err, "--force overwrites")
}

func TestInstallSkillTreeRefusesAbsoluteDestination(t *testing.T) {
	dir := t.TempDir()
	_, _, err := agentSkills.WriteSkillTree(dir, "/tmp/abs/skills", false, types.SkillFormFull)
	require.Error(t, err, "an absolute destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")

	_, _, err = agentSkills.WriteSkillTree(dir, "~/.config/skills", false, types.SkillFormFull)
	require.Error(t, err, "a tilde-prefixed destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")
}

// plantSkillDirs stages the three things a skills directory holds beside a shipped
// install: a skill a past magus stamped and this one dropped, a hand-authored skill,
// and an empty directory.
func plantSkillDirs(t *testing.T, dir, dest string) (retired, handAuthored, empty string) {
	t.Helper()
	retired = filepath.Join(dir, dest, "magus-retired")
	require.NoError(t, os.MkdirAll(retired, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(retired, "SKILL.md"),
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), types.SkillVariantShort), 0o644))

	handAuthored = filepath.Join(dir, dest, "magus-local-development")
	require.NoError(t, os.MkdirAll(handAuthored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(handAuthored, "SKILL.md"), []byte("---\nname: magus-local-development\n---\n\nmine\n"), 0o644))

	empty = filepath.Join(dir, dest, "magus-empty")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	return retired, handAuthored, empty
}

func TestInstallSkillTreePrunesOnlyWhatMagusStamped(t *testing.T) {
	dir := t.TempDir()
	const dest = ".claude/skills"
	retired, handAuthored, empty := plantSkillDirs(t, dir, dest)

	written, changed, removed, err := installSkillTree(dir, dest, true, false, types.SkillFormFull)
	require.NoError(t, err)

	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, removed)
	assert.NotEmpty(t, written)
	assert.Equal(t, written, changed, "a fresh tree is all changes")
	assert.NoDirExists(t, retired)
	assert.DirExists(t, handAuthored, "a skill without the stamp is not magus's to delete")
	assert.DirExists(t, empty, "an empty directory proves nothing about who wrote it")
	assert.FileExists(t, filepath.Join(dir, dest, "magus-query", "SKILL.md"))
}

func TestInstallSkillTreeDryRunRemovesNothing(t *testing.T) {
	dir := t.TempDir()
	const dest = ".claude/skills"
	retired, _, _ := plantSkillDirs(t, dir, dest)

	written, changed, removed, err := installSkillTree(dir, dest, true, true, types.SkillFormFull)
	require.NoError(t, err)

	assert.Equal(t, []string{filepath.Join(dest, "magus-retired")}, removed)
	assert.NotEmpty(t, written)
	assert.Empty(t, changed)
	assert.DirExists(t, retired)
	assert.NoDirExists(t, filepath.Join(dir, dest, "magus-query"), "a dry run writes nothing")
}

// TestAgentInstallReportsEachRemovalOnStdout: install prunes without a flag, so the
// report is all that stands between a person and a skill that left unannounced.
func TestAgentInstallReportsEachRemovalOnStdout(t *testing.T) {
	// Hints dedupe process-wide, so an emit here would silence them for every later test.
	interactive.SetHintsEnabled(false)
	t.Cleanup(func() { interactive.SetHintsEnabled(true) })
	dir := t.TempDir()
	const dest = ".claude/skills"
	retired, handAuthored, _ := plantSkillDirs(t, dir, dest)

	out := captureStdout(t, func() {
		require.NoError(t, agentInstallCmd(context.Background(), []string{dest, "--dir", dir, "--skill-form", "full", "--force"}))
	})

	assert.Equal(t, fmt.Sprintf("removed skill this binary no longer ships: %s\n", filepath.Join(dest, "magus-retired")), out)
	assert.NoDirExists(t, retired)
	assert.DirExists(t, handAuthored)
}

func TestSkillTarIsReproducibleAndExtracts(t *testing.T) {
	dir := t.TempDir()
	body, err := agentSkills.SkillTar(".claude/skills", types.SkillFormFull)
	require.NoError(t, err)
	require.NotEmpty(t, body)

	// Piping to `tar -xf - -C <dir>` is the supported install path; simulate it
	// by writing body to a tempfile and extracting with archive/tar.
	tmp := filepath.Join(dir, "skills.tar")
	require.NoError(t, os.WriteFile(tmp, body, 0o644))
	out := filepath.Join(dir, "out")
	require.NoError(t, os.MkdirAll(out, 0o755))
	extractTar(t, tmp, out)

	first, err := os.ReadFile(filepath.Join(out, ".claude/skills", "magus-query", "SKILL.md"))
	require.NoError(t, err)
	assert.Contains(t, string(first), "name: magus-query")

	// Reproducibility: identical bytes on a second call (no timestamps in body).
	body2, err := agentSkills.SkillTar(".claude/skills", types.SkillFormFull)
	require.NoError(t, err)
	assert.Equal(t, body, body2, "SkillTar must be byte-stable across calls")
}

// TestAgentInstallNeverWritesAgentsMD is the regression test for the rule that
// replaced install-agents-md: AGENTS.md belongs to the developer, so install
// offers the block on stderr and touches nothing. The existing file coming back
// byte-identical is the assertion the old marker-merge could never have made.
func TestAgentInstallNeverWritesAgentsMD(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	const theirs = "# My agents notes\n\nkeep me\n"
	require.NoError(t, os.WriteFile(path, []byte(theirs), 0o644))
	_, _, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, types.SkillFormFull)
	require.NoError(t, err)

	before := dirSnapshot(t, dir)
	out := noticesFrom(t, func() {
		written := []string{".claude/skills/magus-query/SKILL.md"}
		printAgentInstallNextSteps(t.Context(), dir, written, written, types.SkillFormFull, false)
	})

	assert.Contains(t, out, "magus does not write AGENTS.md")
	assert.Contains(t, out, "<!-- magus:skills:begin")
	assert.Contains(t, out, "<!-- magus:skills:end -->")
	assert.Equal(t, before, dirSnapshot(t, dir), "install must not touch AGENTS.md")
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, theirs, string(body), "an existing AGENTS.md is left byte-identical")
}

// TestAgentInstallStaysQuietWhenTheBlockIsCurrent keeps the offer actionable:
// 80 lines of Markdown on every --force reinstall is how a reader learns to
// scroll past this command's output, including the parts that matter.
func TestAgentInstallStaysQuietWhenTheBlockIsCurrent(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Theirs\n\n"+agentSkills.AgentsBlock(false)), 0o644))

	out := noticesFrom(t, func() { printAgentsBlockToPaste(t.Context(), dir) })
	assert.Empty(t, out, "a current block is not reprinted")

	// A stale one is, with the replace-in-place instruction rather than the add-it one.
	stale := strings.Replace(agentSkills.AgentsBlock(false), "skill-content: ", "skill-content: 0", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Theirs\n\n"+stale), 0o644))
	out = noticesFrom(t, func() { printAgentsBlockToPaste(t.Context(), dir) })
	assert.Contains(t, out, "older copy")
	assert.Contains(t, out, "<!-- magus:skills:begin")
}

// TestDeclaresRoutingIndex holds the link line to the workspace's own declaration: only a
// root project that writes MAGUS.md earns it, and a directory that is no workspace root
// never does.
func TestDeclaresRoutingIndex(t *testing.T) {
	const writer = "import \"magus\";\nexport fun index_generate(ctx: magus\\Context, args: [str]) > void !> any {\n    ctx.writesFiles(\"MAGUS.md\");\n}\n"
	const silent = "import \"magus\";\nexport fun noop(ctx: magus\\Context, args: [str]) > void !> any {\n}\n"

	for name, tc := range map[string]struct {
		magusfile string
		want      bool
	}{
		"root declares MAGUS.md":    {writer, true},
		"root declares no MAGUS.md": {silent, false},
	} {
		t.Run(name, func(t *testing.T) {
			w := testkit.NewWorkspace(t)
			w.Write("magus.yaml", "")
			w.Magusfile(tc.magusfile)
			assert.Equal(t, tc.want, declaresRoutingIndex(context.Background(), w.Root()))
		})
	}

	t.Run("not a workspace", func(t *testing.T) {
		assert.False(t, declaresRoutingIndex(context.Background(), t.TempDir()))
	})
	t.Run("below the root", func(t *testing.T) {
		w := testkit.NewWorkspace(t)
		w.Write("magus.yaml", "")
		w.Magusfile(writer)
		sub := filepath.Join(w.Root(), "sub")
		require.NoError(t, os.Mkdir(sub, 0o755))
		assert.False(t, declaresRoutingIndex(context.Background(), sub))
	})
}

// TestAgentStarterPrintsAMarkedBlock keeps the two print paths on one set of
// bytes: a paste from `sample` must be gradeable by `magus doctor` exactly as a
// paste from install's offer is.
func TestAgentStarterPrintsAMarkedBlock(t *testing.T) {
	out := captureStdout(t, func() { require.NoError(t, agentStarterCmd(context.Background(), "")) })
	assert.True(t, strings.HasPrefix(out, "# AGENTS.md\n"))
	assert.Contains(t, out, "## Conventions")
	assert.Contains(t, out, agentSkills.AgentsBlock(false))

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(out), 0o644))
	statuses := agentSkills.CheckStatuses(context.Background(), dir)
	require.Len(t, statuses, 1)
	assert.False(t, statuses[0].Stale, statuses[0].Detail)
}

// dirSnapshot maps every path under dir to its contents, so a test can assert a
// command wrote nothing at all rather than only that one known file survived.
func dirSnapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	}))
	return out
}

func TestStampSkillAppendsExactlyOneFooter(t *testing.T) {
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\n---\nbody\n"), types.SkillVariantFull))
	assert.Equal(t, 1, strings.Count(out, "generated by: magus agent install"))
	assert.True(t, strings.HasSuffix(out, "-->\n"), "footer is the last line")
}

func TestStampSkillInjectsProvenanceInsideFrontmatter(t *testing.T) {
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\ndescription: y\n---\nbody\n"), types.SkillVariantFull))
	// Provenance lands inside the frontmatter (before the closing ---), leaving the
	// source name/description ahead of it byte-for-byte.
	assert.Contains(t, out, "---\nname: x\ndescription: y\nlicense: GPL-3.0-or-later\n")
	assert.Contains(t, out, "compatibility: any-agent\n")
	assert.Contains(t, out, "\n---\nbody\n", "closing --- and body follow the provenance")
	fmStart := strings.Index(out, "---")
	fmEnd := strings.Index(out[fmStart+3:], "\n---")
	assert.Contains(t, out[:fmStart+3+fmEnd], "agent-skill-version:", "version metadata is within the frontmatter")
}

func TestCheckSkillStatusesNothingInstalled(t *testing.T) {
	assert.Empty(t, agentSkills.CheckStatuses(context.Background(), t.TempDir()))
}

func TestCheckSkillStatusesCurrent(t *testing.T) {
	dir := t.TempDir()
	const skillsDir = "harness-skills"
	writeStatusHarness(t, skillsDir)
	_, _, err := agentSkills.WriteSkillTree(dir, skillsDir, false, types.SkillFormFull)
	require.NoError(t, err)
	// Pasted the way a developer would, since magus no longer writes this file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Their notes\n\n"+agentSkills.AgentsBlock(false)), 0o644))

	statuses := agentSkills.CheckStatuses(context.Background(), dir, "status-test")
	require.Len(t, statuses, 2, "one status per installed location")
	for _, s := range statuses {
		assert.True(t, s.Installed, "%s installed", s.Location)
		assert.False(t, s.Stale, "a fresh %s install is current", s.Location)
	}
	assert.Equal(t, "AGENTS.md", statuses[0].Location)
	assert.Equal(t, skillsDir, statuses[1].Location)
}

func TestCheckSkillStatusesStale(t *testing.T) {
	dir := t.TempDir()
	const skillsDir = "harness-skills"
	writeStatusHarness(t, skillsDir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, skillsDir, "magus-query"), 0o755))
	// A footer stamped with an older skill version is stale.
	stale := "---\nname: x\n---\nbody\n<!-- agent-skill-version: 0; knowledge-schema-version: 1 -->\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, skillsDir, "magus-query/SKILL.md"), []byte(stale), 0o644))
	statuses := agentSkills.CheckStatuses(context.Background(), dir, "status-test")
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale)
	assert.Contains(t, statuses[0].Detail, "agent harness install")
}

func TestCheckSkillStatusesNoFooter(t *testing.T) {
	dir := t.TempDir()
	const skillsDir = "harness-skills"
	writeStatusHarness(t, skillsDir)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, skillsDir, "magus-query"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, skillsDir, "magus-query/SKILL.md"), []byte("---\nname: x\n---\nno footer\n"), 0o644))
	statuses := agentSkills.CheckStatuses(context.Background(), dir, "status-test")
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale, "a stamp-less install reads as stale (predates versioning)")
}

// writeStatusHarness registers "status-test" as a fake harness spell (there is
// no harnesses/*.json compat directory left to write it into), so
// CheckStatuses(ctx, dir, "status-test") resolves skillsDir the way a wired host
// spell would.
func writeStatusHarness(t *testing.T, skillsDir string) {
	t.Helper()
	fixture.WireHarness(t, agent.HarnessDescriptor{
		SchemaVersion: 2,
		ID:            "status-test",
		Display:       agent.HarnessDisplay{Name: "Status test"},
		Skills:        types.HarnessSkills{Paths: []string{skillsDir}, Form: types.SkillFormFull},
		Config:        types.HarnessConfig{Path: ".status-test/config.json"},
	})
}

// TestCheckSkillStatusesIgnoresForeignAgentsMD proves an AGENTS.md without our
// managed section is not claimed as a magus install.
func TestCheckSkillStatusesIgnoresForeignAgentsMD(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# their file\n"), 0o644))
	assert.Empty(t, agentSkills.CheckStatuses(context.Background(), dir))
}

// TestEvaluateBashGuard pins the guard's decision table: destructive whole-tree
// git operations deny, staging and raw language tools get a context reminder,
// and everything else passes silently.
// TestEveryEmbeddedSkillHasBothForms is the completeness gate: the short form is
// advertised for the whole installable set, so a skill with no marked rationale
// would quietly install identical bytes and the flag would be a lie for that one.
func TestEveryEmbeddedSkillHasBothForms(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)

	for _, def := range defs {
		f, err := agentSkills.Render(def, types.SkillVariantFull)
		require.NoError(t, err)
		s, err := agentSkills.Render(def, types.SkillVariantShort)
		require.NoError(t, err)
		require.Equal(t, f.Name, s.Name)
		assert.Less(t, len(s.Body), len(f.Body),
			"%s marks no rationale, so the short form installs the same bytes; curate it or drop the claim", f.Name)
	}
}

// Anthropic's skill guidance caps a SKILL.md body at 500 lines, and gives a reference file
// over 100 lines a contents list. The full form is that reference copy, so it carries one;
// the short form is read whole and carries none.
func TestEverySkillFitsTheSkillGuidance(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)

	for _, def := range defs {
		for _, v := range []types.SkillVariant{types.SkillVariantShort, types.SkillVariantFull} {
			r, err := agentSkills.Render(def, v)
			require.NoError(t, err)
			lines := strings.Count(r.Body, "\n") + 1
			assert.LessOrEqual(t, lines, 500, "%s (%s) is %d lines; move enumeration and examples into its full-only arms, or split it", def.Name, v, lines)
			assert.Equal(t, v == types.SkillVariantFull, strings.Contains(r.Body, "\n## Contents\n"), "%s (%s): only the full form carries a contents list", def.Name, v)
			if r.Reference == "" {
				continue
			}
			refLines := strings.Count(r.Reference, "\n") + 1
			assert.LessOrEqual(t, refLines, 500, "%s reference.md is %d lines", def.Name, refLines)
			assert.Contains(t, r.Reference, "\n## Contents\n", "%s: a reference file is read in part, so it carries a contents list", def.Name)
			assert.Contains(t, r.Body, "](reference.md)", "%s: SKILL.md links its reference file, or nothing points a reader at it", def.Name)
		}
	}
}

// A skill names a command through {{cmd}}, which resolves it against the registry, so a
// renamed command fails the install instead of shipping prose that names a verb that is gone.
// Fenced blocks are exempt: they show a whole command line as typed.
func TestSkillSourcesNameCommandsThroughTheTemplate(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	literal := regexp.MustCompile("`magus [a-z][a-z-]*")
	for _, def := range defs {
		for file, src := range map[string]string{"SKILL.md": def.Body, "reference.md": def.Reference} {
			fenced := false
			for i, line := range strings.Split(src, "\n") {
				if strings.HasPrefix(strings.TrimSpace(line), "```") {
					fenced = !fenced
					continue
				}
				if m := literal.FindString(line); !fenced && m != "" {
					t.Errorf("%s/%s:%d types %s literally; write {{cmd \"<path>\"}} instead", def.Name, file, i+1, m)
				}
			}
		}
	}
}

// TestShortInstallShipsAFullTwinForEverySkill pins FormBoth: the short body is a
// bet the INSTALLING reader can re-derive what it drops, and a
// delegated or smaller model that inherits it never made that bet. Every skill
// therefore ships a <name>-full twin whose body is byte-identical to what a
// plain full install writes under the base name, so pointing a sub-agent at the
// twin is exactly as good as having installed full.
func TestShortInstallShipsAFullTwinForEverySkill(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	installed, err := agentSkills.RenderedSkills(types.SkillFormBoth)
	require.NoError(t, err)

	byName := make(map[string]agent.AgentSkill, len(installed))
	for _, s := range installed {
		byName[s.Name] = s
	}
	require.Len(t, installed, 2*len(defs), "FormBoth writes one primary plus one twin per skill")

	for _, def := range defs {
		twin, ok := byName[agent.FullTwinName(def.Name)]
		require.Truef(t, ok, "FormBoth ships no %s twin", agent.FullTwinName(def.Name))

		wantFull, err := agentSkills.Render(def, types.SkillVariantFull)
		require.NoError(t, err)
		assert.Equal(t, wantFull.Body, twin.Body,
			"%s must carry the same body a full install writes for %s", twin.Name, def.Name)
		// The stamp is keyed off the entry's own Variant, so a twin inside a
		// FormBoth install must still stamp itself full: otherwise a reader
		// grading provenance sees "short" on the copy it was handed BECAUSE
		// it needed full.
		assert.Contains(t, string(agentSkills.StampSkill(twin.Name, agentSkills.RenderSkill(twin), twin.Variant)),
			"skill-variant: full", "%s must stamp itself full", twin.Name)

		// The twins point AT EACH OTHER, and neither restates the other.
		//
		// This reverses what the pair used to assert, and the measurement is why. The twin's
		// description was this one's verbatim plus a sentence asking the model to decide
		// whether it is "smaller" (not a fact a model has), and the primary named no twin at
		// all. So nothing routed: 12 of 15 twins were never loaded once across 2,147 sessions,
		// while the restatement spent 10,105 of the 19,113 description bytes a host loads into
		// every prompt.
		//
		// Now the twin is standalone and the primary carries one clause naming it. That is the
		// only path by which a reader holding the short copy can learn the fuller one exists.
		assert.NotContains(t, twin.Description, def.Description,
			"%s must stand alone, not restate %s: the restatement was half of every session's skill-description budget", twin.Name, def.Name)
		assert.Contains(t, twin.Description, def.Name,
			"%s must name the skill it is a fuller copy of", twin.Name)
		primary, ok := byName[def.Name]
		require.True(t, ok)
		assert.Contains(t, primary.Description, agent.FullTwinName(def.Name),
			"%s must name its twin, or a reader holding the short copy can never discover it", def.Name)
	}
}

// TestInstallTargetHonorsGlobal pins the fix for a flag that never worked:
// --global documents "allow absolute destination paths", the command let one
// past its own guard, and Catalog.WriteSkillTree then refused it
// unconditionally, so the error told the caller to pass the flag they had just
// passed.
//
// The split is what makes it work without weakening the catalog: an absolute
// destination becomes the directory it names plus the leaf inside it, so the
// catalog's containment check still has something real to check.
func TestInstallTargetHonorsGlobal(t *testing.T) {
	base, leaf := installTarget(".", "/tmp/skills", true)
	assert.Equal(t, filepath.Dir("/tmp/skills"), base, "an absolute dest supplies its own base")
	assert.Equal(t, "skills", leaf)
	assert.Equal(t, filepath.Clean("/tmp/skills"), filepath.Join(base, leaf),
		"the split must rejoin to the path the caller asked for")
}

// TestInstallTargetLeavesRelativeAlone: without --global, and for a relative
// destination with it, the pair is unchanged: the catalog keeps enforcing
// containment against the repo dir.
func TestInstallTargetLeavesRelativeAlone(t *testing.T) {
	base, leaf := installTarget("repo", ".claude/skills", false)
	assert.Equal(t, "repo", base)
	assert.Equal(t, ".claude/skills", leaf)

	base, leaf = installTarget("repo", ".claude/skills", true)
	assert.Equal(t, "repo", base, "--global does not relocate a relative destination")
	assert.Equal(t, ".claude/skills", leaf)

	// A traversal is left intact so the catalog still refuses it; --global must
	// not become a way to escape the tree.
	base, leaf = installTarget("repo", "../../outside", true)
	assert.Equal(t, "repo", base)
	assert.Equal(t, "../../outside", leaf)
}

// extractTar reads a tar archive at src and writes its entries under dst,
// creating parent directories. Mirrors the user-facing `tar -xf - -C <dst>`
// pipe that the tar output mode is designed for.
func extractTar(t *testing.T, src, dst string) {
	t.Helper()
	f, err := os.Open(src)
	require.NoError(t, err)
	defer f.Close()
	tr := tar.NewReader(f)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return
		}
		require.NoError(t, err)
		target := filepath.Join(dst, hdr.Name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			require.NoError(t, os.MkdirAll(target, 0o755))
		case tar.TypeReg:
			require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
			body, err := io.ReadAll(tr)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(target, body, 0o644))
		default:
			t.Fatalf("unexpected tar entry type %v for %s", hdr.Typeflag, hdr.Name)
		}
	}
}

func TestAgentStarterDocPlainASCIISelfContained(t *testing.T) {
	doc := agentStarterDoc(false)
	assert.Contains(t, doc, "# AGENTS.md")
	assert.Contains(t, doc, "## Project")  // a project placeholder to fill in
	assert.Contains(t, doc, "## magus")    // the reproduced magus block
	assert.Contains(t, doc, vcsSafetyRule) // the shared safety rule
	for _, r := range doc {
		require.Less(t, r, rune(128), "sample AGENTS.md must be plain ASCII")
	}
}

// agentEvalEnv is read at package initialization because TestMain replaces the
// environment with an isolated one before any test runs. The claude credentials and
// CLAUDE_CODE_EXECPATH are the only variables the agent gets from the caller's
// environment.
var agentEvalEnv = func() map[string]string {
	env := map[string]string{}
	for _, name := range []string{
		"MAGUS_AGENT_EVAL",
		"MAGUS_AGENT_EVAL_VARIANTS",
		"MAGUS_AGENT_EVAL_REPS",
		"MAGUS_AGENT_EVAL_RESULTS",
		"CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_API_KEY",
		"CLAUDE_CODE_EXECPATH",
	} {
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
	}
	return env
}()

// TestAgentEval runs every testdata/agenteval task with a real claude, once per skill
// variant and repetition, and appends one row per run to MAGUS_AGENT_EVAL_RESULTS, an
// absolute path (default magus-agenteval.jsonl in the temp dir). It spends tokens, so it
// skips unless MAGUS_AGENT_EVAL=1.
//
// MAGUS_AGENT_EVAL_VARIANTS is a comma-separated list of skill forms or absolute skills
// directories (default full,short); MAGUS_AGENT_EVAL_REPS is the runs per variant
// (default 1). The test runs in cmd/magus, so it refuses a relative path rather than
// resolve it there.
func TestAgentEval(t *testing.T) {
	if agentEvalEnv["MAGUS_AGENT_EVAL"] != "1" {
		t.Skip("MAGUS_AGENT_EVAL=1 runs the agent eval, which spends tokens")
	}
	var variants []string
	for _, v := range strings.Split(cmp.Or(agentEvalEnv["MAGUS_AGENT_EVAL_VARIANTS"], "full,short"), ",") {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if strings.ContainsRune(v, filepath.Separator) && !filepath.IsAbs(v) {
			t.Fatalf("MAGUS_AGENT_EVAL_VARIANTS: %q: a skills directory must be an absolute path", v)
		}
		variants = append(variants, v)
	}
	require.NotEmpty(t, variants, "MAGUS_AGENT_EVAL_VARIANTS names no variant")
	reps, err := strconv.Atoi(cmp.Or(agentEvalEnv["MAGUS_AGENT_EVAL_REPS"], "1"))
	require.NoError(t, err, "MAGUS_AGENT_EVAL_REPS")
	if reps <= 0 {
		t.Fatalf("MAGUS_AGENT_EVAL_REPS: %d: want at least 1", reps)
	}
	results := cmp.Or(agentEvalEnv["MAGUS_AGENT_EVAL_RESULTS"], filepath.Join(os.TempDir(), "magus-agenteval.jsonl"))
	if !filepath.IsAbs(results) {
		t.Fatalf("MAGUS_AGENT_EVAL_RESULTS: %q: want an absolute path", results)
	}
	t.Logf("results: %s", results)

	passEnv := script.WithSetup(func(e *script.Env) error {
		for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_EXECPATH"} {
			if v := agentEvalEnv[name]; v != "" {
				e.Setenv(name, v)
			}
		}
		return nil
	})
	for _, variant := range variants {
		for rep := 1; rep <= reps; rep++ {
			t.Run(fmt.Sprintf("%s/%d", filepath.Base(variant), rep), func(t *testing.T) {
				script.Run(t, filepath.Join("testdata", "agenteval"),
					passEnv,
					script.WithSetup(script.Skills(variant)),
					script.WithAgent(script.Recorder{Variant: variant, Repetition: rep, File: results}))
			})
		}
	}
}
