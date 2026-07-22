package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/docs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEmbeddedSkillsAreWellFormed guards that every shipped SKILL.md carries the
// Agent Skills frontmatter (name + description) an agent host requires - a broken
// skill would install silently and never trigger.
func TestEmbeddedSkillsAreWellFormed(t *testing.T) {
	var checked int
	err := fs.WalkDir(skillFS, "skills", func(p string, d fs.DirEntry, err error) error {
		require.NoError(t, err)
		if d.IsDir() || filepath.Base(p) != "SKILL.md" {
			return nil
		}
		checked++
		b, err := skillFS.ReadFile(p)
		require.NoError(t, err)
		fm, ok := docs.ParseFrontmatter(string(b))
		require.True(t, ok, "%s must open with YAML frontmatter", p)
		assert.NotEmpty(t, fm.Description, "%s needs a description", p)
		// name: is required by the Agent Skills spec but is not a docs-frontmatter field,
		// so check the block carries it directly.
		assert.Contains(t, string(b), "\nname: ", "%s needs a name", p)
		// User-facing skill text follows the plain-ASCII message rule.
		for _, r := range string(b) {
			require.LessOrEqual(t, r, rune(127), "%s must be plain ASCII", p)
		}
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, 6, checked, "expected the magus-query, magus-run, magus-vcs, magus-architecture, magus-memory, and magus-docs skills")
}

// TestAgentsSectionIsPlainASCII holds the AGENTS.md block to the same message rule.
func TestAgentsSectionIsPlainASCII(t *testing.T) {
	require.NotEmpty(t, agentsSection)
	for _, r := range agentsSection {
		require.LessOrEqual(t, r, rune(127), "agents-section.md must be plain ASCII")
	}
}

func TestInstallSkillTreeWritesStampedFiles(t *testing.T) {
	dir := t.TempDir()
	written, err := installSkillTree(dir, skillDirPlatforms["claude"], false)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	skillPath := filepath.Join(dir, ".claude/skills/magus-query/SKILL.md")
	assert.Contains(t, written, ".claude/skills/magus-query/SKILL.md")

	body, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), "---\n"), "frontmatter still leads the file")
	assert.Contains(t, string(body), "agent-skill-version:", "install stamps the drift footer")
	assert.Contains(t, string(body), "knowledge-schema-version:")
}

// TestInstallSkillTreePlatformsShareBytes proves the multi-platform promise: every
// skill-dir platform receives byte-identical files, only the destination differs.
func TestInstallSkillTreePlatformsShareBytes(t *testing.T) {
	dir := t.TempDir()
	for _, dest := range skillDirPlatforms {
		_, err := installSkillTree(dir, dest, false)
		require.NoError(t, err)
	}
	claude, err := os.ReadFile(filepath.Join(dir, ".claude/skills", anchorSkillRel))
	require.NoError(t, err)
	for platform, dest := range skillDirPlatforms {
		other, err := os.ReadFile(filepath.Join(dir, dest, anchorSkillRel))
		require.NoError(t, err)
		assert.Equal(t, string(claude), string(other), "platform %s must receive identical bytes", platform)
	}
}

func TestInstallSkillTreeRefusesThenForces(t *testing.T) {
	dir := t.TempDir()
	_, err := installSkillTree(dir, skillDirPlatforms["claude"], false)
	require.NoError(t, err)

	_, err = installSkillTree(dir, skillDirPlatforms["claude"], false)
	require.Error(t, err, "a second install without --force must refuse")
	assert.Contains(t, err.Error(), "already exists")

	_, err = installSkillTree(dir, skillDirPlatforms["claude"], true)
	assert.NoError(t, err, "--force overwrites")
}

func TestInstallAgentsSectionCreatesReplacesPreserves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	// No AGENTS.md: created holding just the managed section.
	written, err := installAgentsSection(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"AGENTS.md"}, written)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus:skills:begin")
	assert.Contains(t, string(body), "agent-skill-version:")

	// Existing AGENTS.md with other content: section appended, content preserved.
	require.NoError(t, os.WriteFile(path, []byte("# My agents notes\n\nkeep me\n"), 0o644))
	_, err = installAgentsSection(dir)
	require.NoError(t, err)
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "keep me")
	assert.Contains(t, string(body), "magus:skills:begin")

	// Re-install: the section is replaced in place, not duplicated.
	_, err = installAgentsSection(dir)
	require.NoError(t, err)
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(body), "magus:skills:begin"), "re-install must not duplicate the section")
	assert.Equal(t, 1, strings.Count(string(body), "keep me"))
}

func TestStampSkillAppendsExactlyOneFooter(t *testing.T) {
	out := string(stampSkill([]byte("---\nname: x\n---\nbody\n")))
	assert.Equal(t, 1, strings.Count(out, "generated by: magus agent install"))
	assert.True(t, strings.HasSuffix(out, "-->\n"), "footer is the last line")
}

func TestStampSkillInjectsProvenanceInsideFrontmatter(t *testing.T) {
	out := string(stampSkill([]byte("---\nname: x\ndescription: y\n---\nbody\n")))
	// Provenance lands inside the frontmatter (before the closing ---), leaving the
	// source name/description ahead of it byte-for-byte.
	assert.Contains(t, out, "---\nname: x\ndescription: y\nlicense: "+skillLicense+"\n")
	assert.Contains(t, out, "compatibility: any-agent\n")
	assert.Contains(t, out, "\n---\nbody\n", "closing --- and body follow the provenance")
	fmStart := strings.Index(out, "---")
	fmEnd := strings.Index(out[fmStart+3:], "\n---")
	assert.Contains(t, out[:fmStart+3+fmEnd], "agent-skill-version:", "version metadata is within the frontmatter")
}

func TestInjectSkillProvenanceLeavesFrontmatterlessBodyAlone(t *testing.T) {
	body := []byte("no frontmatter here\n")
	assert.Equal(t, body, injectSkillProvenance(body))
}

func TestCheckSkillStatusesNothingInstalled(t *testing.T) {
	assert.Empty(t, checkSkillStatuses(t.TempDir()))
}

func TestCheckSkillStatusesCurrent(t *testing.T) {
	dir := t.TempDir()
	_, err := installSkillTree(dir, skillDirPlatforms["claude"], false)
	require.NoError(t, err)
	_, err = installAgentsSection(dir)
	require.NoError(t, err)

	statuses := checkSkillStatuses(dir)
	require.Len(t, statuses, 2, "one status per installed platform")
	for _, s := range statuses {
		assert.True(t, s.Installed, "%s installed", s.Platform)
		assert.False(t, s.Stale, "a fresh %s install is current", s.Platform)
	}
	assert.Equal(t, "claude", statuses[0].Platform)
	assert.Equal(t, agentsMDPlatform, statuses[1].Platform)
}

func TestCheckSkillStatusesStale(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude/skills/magus-query"), 0o755))
	// A footer stamped with an older skill version is stale.
	stale := "---\nname: x\n---\nbody\n<!-- agent-skill-version: 0; knowledge-schema-version: 1 -->\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude/skills", anchorSkillRel), []byte(stale), 0o644))
	statuses := checkSkillStatuses(dir)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale)
	assert.Contains(t, statuses[0].Detail, "--force")
}

func TestCheckSkillStatusesNoFooter(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".claude/skills/magus-query"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".claude/skills", anchorSkillRel), []byte("---\nname: x\n---\nno footer\n"), 0o644))
	statuses := checkSkillStatuses(dir)
	require.Len(t, statuses, 1)
	assert.True(t, statuses[0].Stale, "a stamp-less install reads as stale (predates versioning)")
}

// TestCheckSkillStatusesIgnoresForeignAgentsMD proves an AGENTS.md without our
// managed section is not claimed as a magus install.
func TestCheckSkillStatusesIgnoresForeignAgentsMD(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("# their file\n"), 0o644))
	assert.Empty(t, checkSkillStatuses(dir))
}

func TestAgentSampleDocPlainASCIISelfContained(t *testing.T) {
	doc := agentSampleDoc()
	assert.Contains(t, doc, "# AGENTS.md")
	assert.Contains(t, doc, "## Project")   // a project placeholder to fill in
	assert.Contains(t, doc, "## magus")     // the reproduced magus block
	assert.Contains(t, doc, vcsSafetyRule)  // the shared safety rule
	for _, r := range doc {
		require.Less(t, r, rune(128), "sample AGENTS.md must be plain ASCII")
	}
}
