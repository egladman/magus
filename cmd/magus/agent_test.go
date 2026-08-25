package main

import (
	"archive/tar"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddedSkillsAreWellFormed(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	require.Len(t, defs, 14)
	for _, def := range defs {
		skill, err := agentSkills.Render(def, agent.VariantFull)
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
	written, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	const rel = ".claude/skills/magus-query/SKILL.md"
	skillPath := filepath.Join(dir, rel)
	assert.Contains(t, written, rel)

	body, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	skills, err := agentSkills.RenderedSkills(agent.VariantFull)
	require.NoError(t, err)
	var query agent.AgentSkill
	for _, skill := range skills {
		if skill.Name == "magus-query" {
			query = skill
			break
		}
	}
	require.NotEmpty(t, query.Name)
	assert.Equal(t, string(agentSkills.StampSkill(query.Name, agentSkills.RenderSkill(query), agent.VariantFull)), string(body))
}

// TestInstallSkillTreeDestinationsShareBytes proves the host-agnostic promise:
// every destination receives byte-identical files, only the directory differs.
func TestInstallSkillTreeDestinationsShareBytes(t *testing.T) {
	dir := t.TempDir()
	dests := agent.WellKnownSkillDirs()
	for _, dest := range dests {
		_, err := agentSkills.WriteSkillTree(dir, dest, false, agent.VariantFull)
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
	_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
	require.NoError(t, err)

	_, err = agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
	require.Error(t, err, "a second install without --force must refuse")
	assert.Contains(t, err.Error(), "already exists")

	_, err = agentSkills.WriteSkillTree(dir, ".claude/skills", true, agent.VariantFull)
	assert.NoError(t, err, "--force overwrites")
}

func TestInstallSkillTreeRefusesAbsoluteDestination(t *testing.T) {
	dir := t.TempDir()
	_, err := agentSkills.WriteSkillTree(dir, "/tmp/abs/skills", false, agent.VariantFull)
	require.Error(t, err, "an absolute destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")

	_, err = agentSkills.WriteSkillTree(dir, "~/.config/skills", false, agent.VariantFull)
	require.Error(t, err, "a tilde-prefixed destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")
}

func TestSkillTarIsReproducibleAndExtracts(t *testing.T) {
	dir := t.TempDir()
	body, err := agentSkills.SkillTar(".claude/skills", agent.VariantFull)
	require.NoError(t, err)
	require.NotEmpty(t, body)

	// Piping to tar -xf - -C <dir> is the supported install path; simulate it
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
	body2, err := agentSkills.SkillTar(".claude/skills", agent.VariantFull)
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
	_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
	require.NoError(t, err)

	before := dirSnapshot(t, dir)
	out := captureStderr(t, func() {
		printAgentInstallNextSteps(dir, []string{".claude/skills/magus-query/SKILL.md"}, nil, agent.VariantFull, false)
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
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Theirs\n\n"+agentSkills.AgentsBlock()), 0o644))

	out := captureStderr(t, func() { printAgentsBlockToPaste(dir) })
	assert.Empty(t, out, "a current block is not reprinted")

	// A stale one is, with the replace-in-place instruction rather than the add-it one.
	stale := strings.Replace(agentSkills.AgentsBlock(), "skill-content: ", "skill-content: 0", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Theirs\n\n"+stale), 0o644))
	out = captureStderr(t, func() { printAgentsBlockToPaste(dir) })
	assert.Contains(t, out, "older copy")
	assert.Contains(t, out, "<!-- magus:skills:begin")
}

// TestAgentSamplePrintsAMarkedBlock keeps the two print paths on one set of
// bytes: a paste from `sample` must be gradeable by `magus doctor` exactly as a
// paste from install's offer is.
func TestAgentSamplePrintsAMarkedBlock(t *testing.T) {
	out := captureStdout(t, func() { require.NoError(t, agentSampleCmd()) })
	assert.True(t, strings.HasPrefix(out, "# AGENTS.md\n"))
	assert.Contains(t, out, "## Conventions")
	assert.Contains(t, out, agentSkills.AgentsBlock())

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(out), 0o644))
	statuses := agentSkills.CheckStatuses(dir)
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
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\n---\nbody\n"), agent.VariantFull))
	assert.Equal(t, 1, strings.Count(out, "generated by: magus agent install"))
	assert.True(t, strings.HasSuffix(out, "-->\n"), "footer is the last line")
}

func TestStampSkillInjectsProvenanceInsideFrontmatter(t *testing.T) {
	out := string(agentSkills.StampSkill("x", []byte("---\nname: x\ndescription: y\n---\nbody\n"), agent.VariantFull))
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
	assert.Empty(t, agentSkills.CheckStatuses(t.TempDir()))
}

func TestCheckSkillStatusesCurrent(t *testing.T) {
	dir := t.TempDir()
	_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false, agent.VariantFull)
	require.NoError(t, err)
	// Pasted the way a developer would, since magus no longer writes this file.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# Their notes\n\n"+agentSkills.AgentsBlock()), 0o644))

	statuses := agentSkills.CheckStatuses(dir)
	require.Len(t, statuses, 2, "one status per installed location")
	for _, s := range statuses {
		assert.True(t, s.Installed, "%s installed", s.Location)
		assert.False(t, s.Stale, "a fresh %s install is current", s.Location)
	}
	assert.Equal(t, ".claude/skills", statuses[0].Location)
	assert.Equal(t, "AGENTS.md", statuses[1].Location)
}

func TestCheckSkillStatusesStale(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude/skills/magus-query"), 0o755))
	// A footer stamped with an older skill version is stale.
	stale := "---\nname: x\n---\nbody\n<!-- agent-skill-version: 0; knowledge-schema-version: 1 -->\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude/skills", "magus-query/SKILL.md"), []byte(stale), 0o644))
	statuses := agentSkills.CheckStatuses(dir)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale)
	assert.Contains(t, statuses[0].Detail, "--force")
}

func TestCheckSkillStatusesNoFooter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude/skills/magus-query"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude/skills", "magus-query/SKILL.md"), []byte("---\nname: x\n---\nno footer\n"), 0o644))
	statuses := agentSkills.CheckStatuses(dir)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale, "a stamp-less install reads as stale (predates versioning)")
}

// TestCheckSkillStatusesIgnoresForeignAgentsMD proves an AGENTS.md without our
// managed section is not claimed as a magus install.
func TestCheckSkillStatusesIgnoresForeignAgentsMD(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# their file\n"), 0o644))
	assert.Empty(t, agentSkills.CheckStatuses(dir))
}

// TestEvaluateBashGuard pins the guard's decision table: destructive whole-tree
// git operations deny, staging and raw language tools get a context reminder,
// and everything else passes silently.
// TestEveryEmbeddedSkillHasBothPermutations is the completeness gate: --simple is
// advertised for the whole installable set, so a skill with no marked rationale
// would quietly install identical bytes and the flag would be a lie for that one.
func TestEveryEmbeddedSkillHasBothPermutations(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)

	for _, def := range defs {
		f, err := agentSkills.Render(def, agent.VariantFull)
		require.NoError(t, err)
		s, err := agentSkills.Render(def, agent.VariantSimple)
		require.NoError(t, err)
		require.Equal(t, f.Name, s.Name)
		assert.Less(t, len(s.Body), len(f.Body),
			"%s marks no rationale, so --simple installs the same bytes; curate it or drop the claim", f.Name)
	}
}

// TestSimpleInstallShipsAFullTwinForEverySkill pins the dual install: a simple
// install is a bet the INSTALLING reader can re-derive what simple drops, and a
// delegated or smaller model that inherits it never made that bet. Every skill
// therefore ships a <name>-full twin whose body is byte-identical to what a
// plain full install writes under the base name, so pointing a sub-agent at the
// twin is exactly as good as having installed full.
func TestSimpleInstallShipsAFullTwinForEverySkill(t *testing.T) {
	defs, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	installed, err := agentSkills.RenderedSkills(agent.VariantSimple)
	require.NoError(t, err)

	byName := make(map[string]agent.AgentSkill, len(installed))
	for _, s := range installed {
		byName[s.Name] = s
	}
	require.Len(t, installed, 2*len(defs), "a simple install writes one primary plus one twin per skill")

	for _, def := range defs {
		twin, ok := byName[agent.FullTwinName(def.Name)]
		require.Truef(t, ok, "simple install ships no %s twin", agent.FullTwinName(def.Name))

		wantFull, err := agentSkills.Render(def, agent.VariantFull)
		require.NoError(t, err)
		assert.Equal(t, wantFull.Body, twin.Body,
			"%s must carry the same body a full install writes for %s", twin.Name, def.Name)
		// The stamp is keyed off the entry's own Variant, so a twin inside a
		// simple install must still stamp itself full - otherwise a reader
		// grading provenance sees "simple" on the copy it was handed BECAUSE
		// it needed full.
		assert.Contains(t, string(agentSkills.StampSkill(twin.Name, agentSkills.RenderSkill(twin), twin.Variant)),
			"skill-variant: full", "%s must stamp itself full", twin.Name)

		// The twin announces itself; the primary does not carry a pointer to it.
		// simple exists to spend less context, so the discoverability cost is
		// paid once on the twin's own listing entry rather than on every skill.
		assert.Contains(t, twin.Description, "delegated",
			"%s must tell a delegated model to prefer it, or nothing routes to it", twin.Name)
		primary, ok := byName[def.Name]
		require.True(t, ok)
		assert.NotContains(t, primary.Description, agent.FullTwinName(def.Name),
			"%s must not spend simple's context pointing at its twin; the twin's own entry does that", def.Name)
	}
}

// TestInstallTargetHonorsGlobal pins the fix for a flag that never worked:
// --global documents "allow absolute destination paths", the command let one
// past its own guard, and Catalog.WriteSkillTree then refused it
// unconditionally - so the error told the caller to pass the flag they had just
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
// destination with it, the pair is unchanged - the catalog keeps enforcing
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

func TestAgentSampleDocPlainASCIISelfContained(t *testing.T) {
	doc := agentSampleDoc()
	assert.Contains(t, doc, "# AGENTS.md")
	assert.Contains(t, doc, "## Project")  // a project placeholder to fill in
	assert.Contains(t, doc, "## magus")    // the reproduced magus block
	assert.Contains(t, doc, vcsSafetyRule) // the shared safety rule
	for _, r := range doc {
		require.Less(t, r, rune(128), "sample AGENTS.md must be plain ASCII")
	}
}
