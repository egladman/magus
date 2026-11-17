package main

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSkillsAreWellFormed(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	require.Len(t, defs, 17)
	for _, def := range defs {
		skill, err := agentSkills.Render(def, tier.SkillVariantFull)
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
	assert.Equal(t, string(agentSkills.StampSkill(query.Name, agentSkills.RenderSkill(query), tier.SkillVariantFull)), string(body))
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
		agentSkills.StampSkill("magus-retired", []byte("---\nname: magus-retired\n---\n\n# gone\n"), tier.SkillVariantShort), 0o644))

	handAuthored = filepath.Join(dir, dest, "magus-local-development")
	require.NoError(t, os.MkdirAll(handAuthored, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(handAuthored, "SKILL.md"), []byte("---\nname: magus-local-development\n---\n\nmine\n"), 0o644))

	empty = filepath.Join(dir, dest, "magus-empty")
	require.NoError(t, os.MkdirAll(empty, 0o755))
	return retired, handAuthored, empty
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
		require.NoError(t, agentInstallCommand(context.Background(), []string{dest, "--dir", dir, "--skill-form", "full", "--force"}))
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
	out := notices(t, func() {
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

	out := notices(t, func() { printAgentsBlockToPaste(t.Context(), dir) })
	assert.Empty(t, out, "a current block is not reprinted")

	// A stale one is, with the replace-in-place instruction rather than the add-it one.
	stale := strings.Replace(agentSkills.AgentsBlock(false), "skill-content: ", "skill-content: 0", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Theirs\n\n"+stale), 0o644))
	out = notices(t, func() { printAgentsBlockToPaste(t.Context(), dir) })
	assert.Contains(t, out, "older copy")
	assert.Contains(t, out, "<!-- magus:skills:begin")
}

// TestDeclaresRoutingIndex holds the link line to the workspace's own declaration: only a
// root project that writes MAGUS.md earns it, and a directory that is no workspace root
// never does.
func TestDeclaresRoutingIndex(t *testing.T) {
	const writer = "import \"magus\";\nexport fun index_generate(ctx: magus\\Context, args: [str]) > void !> any {\n    ctx.writesFiles([\"MAGUS.md\"]);\n}\n"
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
	out := captureStdout(t, func() { require.NoError(t, agentStarterCommand(context.Background(), "")) })
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
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\n---\nbody\n"), tier.SkillVariantFull))
	assert.Equal(t, 1, strings.Count(out, "generated by: magus agent install"))
	assert.True(t, strings.HasSuffix(out, "-->\n"), "footer is the last line")
}

func TestStampSkillInjectsProvenanceInsideFrontmatter(t *testing.T) {
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\ndescription: y\n---\nbody\n"), tier.SkillVariantFull))
	// Provenance lands inside the frontmatter (before the closing ---), leaving the
	// source name/description ahead of it byte-for-byte.
	assert.Contains(t, out, "---\nname: x\ndescription: y\nlicense: GPL-3.0-or-later\n")
	assert.Contains(t, out, "compatibility: any-agent\n")
	assert.Contains(t, out, "\n---\nbody\n", "closing --- and body follow the provenance")
	fmStart := strings.Index(out, "---")
	fmEnd := strings.Index(out[fmStart+3:], "\n---")
	assert.Contains(t, out[:fmStart+3+fmEnd], "skill-stamp-format:", "version metadata is within the frontmatter")
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
		APIVersion: agent.HarnessAPIVersion,
		ID:         "status-test",
		Display:    agent.HarnessDisplay{Name: "Status test"},
		Skills:     types.HarnessSkills{Paths: []string{skillsDir}, Form: types.SkillFormFull},
		Config:     types.HarnessConfig{Path: ".status-test/config.json"},
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
		f, err := agentSkills.Render(def, tier.SkillVariantFull)
		require.NoError(t, err)
		s, err := agentSkills.Render(def, tier.SkillVariantShort)
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
		for _, v := range []tier.SkillVariant{tier.SkillVariantShort, tier.SkillVariantFull} {
			r, err := agentSkills.Render(def, v)
			require.NoError(t, err)
			lines := strings.Count(r.Body, "\n") + 1
			assert.LessOrEqual(t, lines, 500, "%s (%s) is %d lines; move enumeration and examples into its full-only arms, or split it", def.Name, v, lines)
			assert.Equal(t, v == tier.SkillVariantFull, strings.Contains(r.Body, "\n## Contents\n"), "%s (%s): only the full form carries a contents list", def.Name, v)
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

		wantFull, err := agentSkills.Render(def, tier.SkillVariantFull)
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
		// Now the twin is standalone and names its primary. The primary carries no pointer
		// to the twin: a description cannot route, so the sentence it used to carry was dropped.
		assert.NotContains(t, twin.Description, def.Description,
			"%s must stand alone, not restate %s: the restatement was half of every session's skill-description budget", twin.Name, def.Name)
		assert.Contains(t, twin.Description, def.Name,
			"%s must name the skill it is a fuller copy of", twin.Name)
		primary, ok := byName[def.Name]
		require.True(t, ok)
		assert.NotContains(t, primary.Description, agent.FullTwinName(def.Name),
			"%s must not carry the twin pointer sentence any more", def.Name)
	}
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

// TestHarnessInstallRefusesABoundJobFromEitherSource pins that a harness skill install is
// refused under the checkout's binding as well as under the claim the process was launched
// with. Only the claim used to count, so a worker `magus job exec` bound, with no BAGGAGE,
// could rewrite the skills that steer it.
func TestHarnessInstallRefusesABoundJobFromEitherSource(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	t.Setenv(trail.EnvBaggage, "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("version: 1\n"), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)

	acting := func(ctx context.Context) string {
		lease, _, err := guard.CheckoutLease(ctx, root, jobPlace)
		require.NoError(t, err)
		return lease
	}
	assert.Empty(t, acting(context.Background()), "an unbound caller installs its own skills")

	claimed := jobtypes.WithLease(context.Background(), "fleet/claimed")
	assert.Equal(t, "fleet/claimed", acting(claimed), "the claim")

	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/env")
	assert.Equal(t, "fleet/env", acting(context.Background()), "BAGGAGE, with no forwarded lease")
	assert.Equal(t, "fleet/claimed", acting(claimed), "a forwarded lease over BAGGAGE")
	t.Setenv(trail.EnvBaggage, "")

	bindCheckout(t, cacheDir, "fleet/bound")
	assert.Equal(t, "fleet/bound", acting(context.Background()), "the binding, with no claim")
	assert.Equal(t, "fleet/bound", acting(claimed), "the binding over a different claim")

	err = agentHarnessInstallCommand(context.Background(), root, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bound job "fleet/bound"`)
}

// TestDescribeHarnessMergeCommandLeavesTheFileCurrent runs the printed merge command with
// this test binary as `magus`, so the `magus buzz` it pipes into is built from this source
// and never a stale binary found on disk. The command is copied from the text output the
// way a person copies it and must match want-merge.sh byte for byte, so an escaped > fails
// here. Afterwards nothing is left to merge, the person's own entries and an integer past
// 2^53 survive, a retired entry is gone, and a second run on the current plan changes nothing.
func TestDescribeHarnessMergeCommandLeavesTheFileCurrent(t *testing.T) {
	script := filepath.Join(t.TempDir(), "describe_harness_merge.txtar")
	require.NoError(t, os.WriteFile(script, []byte(describeHarnessMergeTxtar), 0o644))
	testscript.Run(t, testscript.Params{
		Files: []string{script},
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_SERVER_ENABLED", "false")
			e.Setenv("MAGUS_BROKER", "off")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			// The merge's fs writes are judged, which opens the workspace.
			e.Setenv("XDG_CACHE_HOME", filepath.Join(e.WorkDir, ".cache"))
			return nil
		},
	})
}

const describeHarnessMergeTxtar = `exec sh -c 'magus describe harness script-host | sed -n "s/^  \(magus describe harness .*\)/\1/p" > printed.sh'
cmp printed.sh want-merge.sh
exec sh printed.sh
cmp hooks.json want.json
exec sh printed.sh
cmp hooks.json want.json
exec magus describe harness script-host
stdout '^script-host harness: current$'
stdout '^wired in hooks.json hooks.before:$'
stdout '^  run: magus buzz -s magus-command.buzz$'
exec magus describe harness script-host -o 'template={{range .wired}}{{.file}} {{.key}}{{range .entries}} {{.match}}{{end}}{{end}}'
stdout '^hooks.json hooks.before run$'

-- magusfile.buzz --
import "magus";
import "spells/script-host" as scripthost;
magus\harness.provider(scripthost);
-- spells/script-host/spell.buzz --
import "magus/spell";

export fun mgs_getName() > str { return "script-host"; }

export fun mgs_getHarness() > Harness {
    return Harness{config = HarnessConfig{path = "hooks.json"}, guard = "hooks"};
}

export fun mgs_getSkills() > HarnessSkills {
    return HarnessSkills{form = SkillForm.short};
}

export fun mgs_listEntries() > [ManagedEntries] {
    return [ManagedEntries{path = ["hooks", "before"], entries = [{"match": "run", "commands": [{"type": "command", "command": "magus buzz -s magus-command.buzz"}]}]}];
}

export fun mgs_renderPrompts(humanOnly: {str: str}) > [Prompt] { return []; }
-- hooks.json --
{
  "large": 9007199254740993,
  "hooks": {
    "before": [
      {"match": "run", "commands": [{"type": "command", "command": "magus buzz -s old/magus-command.buzz"}]},
      {"match": "run", "commands": [{"type": "command", "command": "my-own-hook"}]}
    ],
    "after": [{"command": "theirs"}]
  }
}
-- want-merge.sh --
magus describe harness script-host -o json | magus buzz -e 'import "encoding/json"; import "fs"; import "io"; import "merge"; fun main(args: [str]) > void !> str { final plan = json\parse(io\stdin.readAll()); if (plan["files"] == null) { return; } foreach (path, file in plan["files"]) { final dir = fs\dirname(path); if (dir != "" and dir != ".") { fs\mkdirAll(dir); } if (file["content"] != null) { fs\writeFileAtomic(path, content: file["content"]); } else if (file["exists"]) { fs\writeFileAtomic(path, content: merge\json(fs\readFile(path), overlay: json\stringify(file["fragment"]))); } else { fs\writeFileAtomic(path, content: json\stringify(file["fragment"], indent: "  ") + "\n"); } } }'
-- want.json --
{
  "hooks": {
    "after": [
      {
        "command": "theirs"
      }
    ],
    "before": [
      {
        "commands": [
          {
            "command": "my-own-hook",
            "type": "command"
          }
        ],
        "match": "run"
      },
      {
        "commands": [
          {
            "command": "magus buzz -s magus-command.buzz",
            "type": "command"
          }
        ],
        "match": "run"
      }
    ]
  },
  "large": 9007199254740993
}
`

// TestShippedHarnessEntriesRetireWhenRewritten holds every entry a shipped harness spell
// renders to being one a later merge recognizes as magus's. It merges each host's config,
// rewrites every command the way a new spell version would, and merges again: an entry the
// planner did not recognize stays beside its replacement, which is how Claude Code's session
// PATH entry came to be wired twice while describe called the file current. Each config starts
// with a hand-written reply check that no merge may add a second copy of, retire or rewrite.
func TestShippedHarnessEntriesRetireWhenRewritten(t *testing.T) {
	script := filepath.Join(t.TempDir(), "shipped_harness_retire.txtar")
	require.NoError(t, os.WriteFile(script, []byte(shippedHarnessRetireTxtar), 0o644))
	testscript.Run(t, testscript.Params{
		Files: []string{script},
		Setup: func(e *testscript.Env) error {
			e.Setenv("MAGUS_SERVER_ENABLED", "false")
			e.Setenv("MAGUS_BROKER", "off")
			e.Setenv("MAGUS_HINTS_ENABLED", "false")
			// The merge's fs writes are judged, which opens the workspace.
			e.Setenv("XDG_CACHE_HOME", filepath.Join(e.WorkDir, ".cache"))
			for _, id := range []string{"claude-code", "codex", "cursor"} {
				spell := filepath.Join("spells", "harness", id, "spell.buzz")
				body, err := os.ReadFile(filepath.Join("..", "..", spell))
				if err != nil {
					return err
				}
				if err := os.MkdirAll(filepath.Join(e.WorkDir, filepath.Dir(spell)), 0o755); err != nil {
					return err
				}
				if err := os.WriteFile(filepath.Join(e.WorkDir, spell), body, 0o644); err != nil {
					return err
				}
			}
			return nil
		},
	})
}

const shippedHarnessRetireTxtar = `exec sh merge.sh
grep 'magus-command.buzz' .claude/settings.json
grep 'CLAUDE_ENV_FILE' .claude/settings.json
grep 'magus-command.buzz' .codex/hooks.json
grep 'cursor-hook.buzz' .cursor/hooks.json
grep '"matcher": "spawn_agent\|collaborationspawn_agent\|Agent"' .codex/hooks.json
grep '"PostToolUse"' .codex/hooks.json
grep '"SubagentStart"' .codex/hooks.json
grep '"SubagentStop"' .codex/hooks.json
! grep '"matcher": "Read"' .codex/hooks.json
grep '"UserPromptSubmit"' .codex/hooks.json
grep '"beforeSubmitPrompt"' .cursor/hooks.json
! grep 'sandbox_mode' .codex/agents/magus-scout.toml
grep -count=1 'proofread-reply.buzz' .claude/settings.json
grep -count=1 'proofread-reply.buzz' .codex/hooks.json
grep -count=1 'proofread-reply.buzz' .cursor/hooks.json
exec sh rewrite.sh
grep 'stale-rendering' .claude/settings.json
exec sh merge.sh
! grep 'stale-rendering' .claude/settings.json
! grep 'stale-rendering' .codex/hooks.json
! grep 'stale-rendering' .cursor/hooks.json
grep -count=1 'proofread-reply.buzz' .claude/settings.json
grep -count=1 'proofread-reply.buzz' .codex/hooks.json
grep -count=1 'proofread-reply.buzz' .cursor/hooks.json
grep -count=1 'collaborationspawn_agent' .codex/hooks.json
grep -count=3 'magus-rehydrate.buzz' .codex/hooks.json
exec magus describe harness claude-code
stdout '^claude-code harness: current$'
exec magus describe harness codex
stdout '^codex harness: current$'
exec magus describe harness cursor
stdout '^cursor harness: current$'

-- magusfile.buzz --
import "magus";
import "spells/harness/claude-code" as claude;
import "spells/harness/codex";
import "spells/harness/cursor";
magus\harness.provider(claude);
magus\harness.provider(codex);
magus\harness.provider(cursor);
-- .claude/settings.json --
{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "magus buzz -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name claude-code"}]}]}}
-- .codex/hooks.json --
{"hooks": {"Stop": [{"hooks": [{"type": "command", "command": "magus buzz -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name codex"}]}]}}
-- .cursor/hooks.json --
{"version": 1, "hooks": {"stop": [{"command": "magus buzz -s docs/guides/integrations/agents/proofread-reply.buzz -- --agent-name cursor", "loop_limit": 1}]}}
-- merge.sh --
set -e
for id in claude-code codex cursor; do
  magus describe harness "$id" -o 'template={{.merge}}' > "merge-$id.sh"
  sh "merge-$id.sh"
done
-- rewrite.sh --
set -e
for f in .claude/settings.json .codex/hooks.json .cursor/hooks.json; do
  sed '/proofread-reply/!s/"command": "/"command": ": stale-rendering; /g' "$f" > rewritten
  mv rewritten "$f"
done
`

// The checkpoint's spawn ancestry is read from the environment, so its lease must be the
// environment's claim too: a hook process forwards none, and guard.CheckoutLease has to agree with
// job.ActingLease over trail.SpawnFromEnv for every source.
func TestCheckoutLeaseAnswersWhatTheCheckpointClaimedFromTheEnvironment(t *testing.T) {
	for name, tc := range map[string]struct{ claim, bound string }{
		"nothing":                 {},
		"the claim alone":         {claim: "fleet/f3"},
		"the binding alone":       {bound: "fleet/bound"},
		"the binding and a claim": {claim: "fleet/f3", bound: "fleet/bound"},
		"an invalid claim":        {claim: "not a lease id"},
	} {
		t.Run(name, func(t *testing.T) {
			root := attentionTestRoot(t)
			cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
			require.NoError(t, err)
			if tc.bound != "" {
				bindCheckout(t, cacheDir, tc.bound)
			}
			t.Setenv(trail.EnvBaggage, "")
			if tc.claim != "" {
				t.Setenv(trail.EnvBaggage, fmt.Sprintf("%s=%s", trail.BaggageLease, tc.claim))
			}

			wantLease, wantFrom := job.ActingLease(job.Location{CacheDir: cacheDir, StateBase: fixture.StateBase(t)}, trail.SpawnFromEnv().Lease)
			lease, from, err := guard.CheckoutLease(context.Background(), root, jobPlace)
			require.NoError(t, err)
			assert.Equal(t, wantLease, lease)
			assert.Equal(t, wantFrom, from)
		})
	}
}

func TestHarnessSourceLinksRefuseBoundCommandBeforeReadingStdin(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	t.Setenv(trail.EnvBaggage, "")
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("version: 1\n"), 0o644))
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	bindCheckout(t, cacheDir, "fleet/bound")

	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	defer reader.Close()
	defer writer.Close()
	previous := os.Stdin
	os.Stdin = reader
	t.Cleanup(func() { os.Stdin = previous })
	err = agentHarnessLinksCommand(context.Background(), root, []string{"--stdin"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `bound job "fleet/bound"`)
	_, err = writer.Write([]byte(`{"links":{}}`))
	assert.NoError(t, err, "stdin remains unread while the checkout is bound")
}

// TestAgentAdoptionCmdCommandsFile exercises the command end to end through the
// generated --commands binder: a file of shell commands becomes the input, and
// the whole rendered report is compared so input that arrived truncated or
// empty cannot pass.
func TestAgentAdoptionCmdCommandsFile(t *testing.T) {
	for _, tt := range []struct {
		name     string
		commands string
		want     string
	}{
		{
			name:     "commands",
			commands: "grep -rn HandleFoo internal/\ncat foo.go\nmagus query kind=spell\n",
			want: `agent adoption over 3 commands:

  graph verbs (query/refs/explain/path/graph)          1
  text searches (grep/rg/ag)                           1
  graph : search ratio                             1 : 1

  repo-wide search over source (refs/query)            1
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             1

top repo-wide patterns that look like symbols, with the graph query to try:
       1  HandleFoo  ->  magus refs HandleFoo
`,
		},
		{
			name:     "empty file",
			commands: "",
			want: `agent adoption over 0 commands:

  graph verbs (query/refs/explain/path/graph)          0
  text searches (grep/rg/ag)                           0
  graph : search ratio                               n/a

  repo-wide search over source (refs/query)            0
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             0
`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// bindDisplayFlags seeds -o from the ambient global, so a format left
			// behind by another test would decide this one's rendering.
			prev := global.output
			t.Cleanup(func() { global.output = prev })
			global.output = ""

			path := filepath.Join(t.TempDir(), "commands.txt")
			require.NoError(t, os.WriteFile(path, []byte(tt.commands), 0o644))

			var err error
			out := captureStdout(t, func() { err = agentAdoptionCommand([]string{"--commands", path}) })
			require.NoError(t, err)
			require.Equal(t, tt.want, out)
		})
	}
}

func TestAgentAdoptionCmdMissingCommandsFile(t *testing.T) {
	err := agentAdoptionCommand([]string{"--commands", filepath.Join(t.TempDir(), "absent.txt")})
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), "agent adoption: ")
}
