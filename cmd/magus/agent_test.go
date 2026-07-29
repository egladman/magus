package main

import (
	"archive/tar"
	"bytes"
	"context"
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
	skills, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	require.Len(t, skills, 7)
	for _, skill := range skills {
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
	written, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false)
	require.NoError(t, err)
	require.NotEmpty(t, written)

	const rel = ".claude/skills/magus-query/SKILL.md"
	skillPath := filepath.Join(dir, rel)
	assert.Contains(t, written, rel)

	body, err := os.ReadFile(skillPath)
	require.NoError(t, err)
	skills, err := agentSkills.EmbeddedSkills()
	require.NoError(t, err)
	var query agent.AgentSkill
	for _, skill := range skills {
		if skill.Name == "magus-query" {
			query = skill
			break
		}
	}
	require.NotEmpty(t, query.Name)
	assert.Equal(t, string(agentSkills.StampSkill(agentSkills.RenderSkill(query))), string(body))
}

// TestInstallSkillTreeDestinationsShareBytes proves the host-agnostic promise:
// every destination receives byte-identical files, only the directory differs.
func TestInstallSkillTreeDestinationsShareBytes(t *testing.T) {
	dir := t.TempDir()
	dests := agent.WellKnownSkillDirs()
	for _, dest := range dests {
		_, err := agentSkills.WriteSkillTree(dir, dest, false)
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
	_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false)
	require.NoError(t, err)

	_, err = agentSkills.WriteSkillTree(dir, ".claude/skills", false)
	require.Error(t, err, "a second install without --force must refuse")
	assert.Contains(t, err.Error(), "already exists")

	_, err = agentSkills.WriteSkillTree(dir, ".claude/skills", true)
	assert.NoError(t, err, "--force overwrites")
}

func TestInstallSkillTreeRefusesAbsoluteDestination(t *testing.T) {
	dir := t.TempDir()
	_, err := agentSkills.WriteSkillTree(dir, "/tmp/abs/skills", false)
	require.Error(t, err, "an absolute destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")

	_, err = agentSkills.WriteSkillTree(dir, "~/.config/skills", false)
	require.Error(t, err, "a tilde-prefixed destination must be refused")
	assert.Contains(t, err.Error(), "outside the working tree")
}

func TestSkillTarIsReproducibleAndExtracts(t *testing.T) {
	dir := t.TempDir()
	body, err := agentSkills.SkillTar(".claude/skills")
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
	body2, err := agentSkills.SkillTar(".claude/skills")
	require.NoError(t, err)
	assert.Equal(t, body, body2, "SkillTar must be byte-stable across calls")
}

func TestInstallAgentsSectionCreatesReplacesPreserves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")

	// No AGENTS.md: created holding just the managed section.
	written, err := agentSkills.WriteAgentsSection(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"AGENTS.md"}, written)
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(body), "# AGENTS.md\n"))
	assert.Contains(t, string(body), "magus:skills:begin")
	assert.Contains(t, string(body), "agent-skill-version:")

	// Existing AGENTS.md with other content: section appended, content preserved.
	require.NoError(t, os.WriteFile(path, []byte("# My agents notes\n\nkeep me\n"), 0o644))
	_, err = agentSkills.WriteAgentsSection(dir)
	require.NoError(t, err)
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Contains(t, string(body), "keep me")
	assert.Contains(t, string(body), "magus:skills:begin")

	// Re-install: the section is replaced in place, not duplicated.
	_, err = agentSkills.WriteAgentsSection(dir)
	require.NoError(t, err)
	body, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(string(body), "magus:skills:begin"), "re-install must not duplicate the section")
	assert.Equal(t, 1, strings.Count(string(body), "keep me"))
}

func TestStampSkillAppendsExactlyOneFooter(t *testing.T) {
	out := string(agentSkills.StampSkill([]byte("---\nname: x\n---\nbody\n")))
	assert.Equal(t, 1, strings.Count(out, "generated by: magus agent install"))
	assert.True(t, strings.HasSuffix(out, "-->\n"), "footer is the last line")
}

func TestStampSkillInjectsProvenanceInsideFrontmatter(t *testing.T) {
	out := string(agentSkills.StampSkill([]byte("---\nname: x\ndescription: y\n---\nbody\n")))
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
	_, err := agentSkills.WriteSkillTree(dir, ".claude/skills", false)
	require.NoError(t, err)
	_, err = agentSkills.WriteAgentsSection(dir)
	require.NoError(t, err)

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
func TestEvaluateBashGuard(t *testing.T) {
	tests := []struct {
		command string
		deny    bool
		context string // "" for none, else a substring the context must carry
	}{
		{command: "git stash", deny: true},
		{command: "git stash push -u", deny: true},
		{command: "cd /repo && git stash", deny: true},
		{command: "git stash pop"},
		{command: "git stash list"},
		{command: "git reset --hard origin/main", deny: true},
		{command: "git reset HEAD~1"},
		{command: "git reset && tool --hard-mode"},
		{command: "git checkout .", deny: true},
		{command: "git checkout -- .", deny: true},
		{command: "git checkout main"},
		{command: "git checkout -b feat/x"},
		{command: "git restore .", deny: true},
		// A path-scoped revert advises now: discarding a file because you did not
		// hand-edit it is the most common wrong reflex about generated output.
		{command: "git restore cmd/magus/agent.go", context: "magus-vcs"},
		{command: "git checkout -- gen/", context: "role=output"},
		{command: "git checkout HEAD -- docs/gen", context: "role=output"},
		{command: "git clean -fd", deny: true},
		{command: "git clean -n"},
		{command: "git commit -m 'x'", context: "magus-vcs"},
		{command: "git add -A", context: "magus-vcs"},
		// The advisory names an explicit ladder: top-level target, then a single
		// spell op which still runs through magus. The old text ended with "if no
		// target covers this work, proceed", which read as permission to reach
		// straight for the raw binary.
		{command: "go test ./...", context: "<spell>::<op>"},
		{command: "npm test", context: "magus-run"},
		{command: "npx prettier --check .", context: "magus-run"},
		{command: "pytest tests/", context: "magus-run"},
		{command: "cargo build --release", context: "magus-run"},
		{command: "gofmt -w x.go", context: "magus-run"},
		// Exempt: these bypass nothing, so advising on them is pure noise.
		{command: "go build -o /tmp/magus ./cmd/magus"},
		{command: "gofmt -l ./libs"},
		{command: "gofmt -d x.go"},
		// The rule set is language-agnostic on purpose: magus workspaces are not
		// Go-only, and a guard that only knows Go is useless in a Rust or JS repo.
		{command: "ruff check .", context: "magus-run"},
		{command: "mypy .", context: "magus-run"},
		{command: "rustfmt src/main.rs", context: "magus-run"},
		{command: "vitest run", context: "magus-run"},
		{command: "buf lint", context: "magus-run"},
		{command: "golangci-lint run", context: "magus-run"},
		{command: "buf generate", context: "magus-run"},
		{command: "mockery", context: "magus-run"},
		// Trimming magus's own output with the shell: magus has output flags, and
		// a pipe discards exactly what the agent then has to guess at.
		{command: "magus affected ci 2>&1 | tail -30", context: "--silent"},
		{command: "/tmp/magus run test | head -5", context: "--silent"},
		{command: "MAGUS_X=1 magus query foo | grep bar", context: "--silent"},
		// magus must be the COMMAND, not a substring: these are paths and text.
		{command: "grep -n x cmd/magus/agent_test.go | head"},
		{command: "ls cmd/magus | wc -l"},
		{command: "cat x | magus buzz -"},
		// jq composes with -o json rather than fighting it.
		{command: "magus graph export -o json | jq ."},
		// Repo-wide code search: the graph answers from declared sources. Narrow on
		// purpose - reading one file with grep is not a structural question.
		// Denied, not advised: a repo-wide text search is the habit that keeps the
		// graph unused, and an advisory is scrolled past. The reason must ROUTE -
		// refs for code symbols, query for domain entities - because an agent that
		// tries `magus query someFunc`, gets 0, and gives up is the failure mode.
		{command: `grep -rn "funcName" .`, deny: true},
		{command: "rg symbolName", deny: true},
		{command: `find . -name "*.go"`, deny: true},
		// magus is CWD-relative, so cd-then-magus is how the right command lands
		// on the wrong project. The project is an argument; only a different
		// WORKSPACE needs --root.
		{command: "cd libs/diag && magus run test", context: "CWD-relative"},
		{command: "magus run test libs/diag"},
		{command: "cd libs/diag"},
		{command: "grep pattern onefile.txt"},
		{command: "grep -n x file.go"},
		{command: "cat x | grep y"},
		{command: "go version"},
		{command: "magus run test"},
		{command: "ls -la"},
		{command: "git status --porcelain"},
		{command: "git diff --cached --stat"},
	}
	for _, tt := range tests {
		v := evaluateBashGuard(tt.command)
		if tt.deny {
			assert.NotEmpty(t, v.Deny, "%q must deny", tt.command)
			assert.Empty(t, v.Context, "%q denies, no context", tt.command)
			continue
		}
		assert.Empty(t, v.Deny, "%q must not deny", tt.command)
		if tt.context == "" {
			assert.Empty(t, v.Context, "%q must pass silently", tt.command)
		} else {
			assert.Contains(t, v.Context, tt.context, "%q context names the skill", tt.command)
		}
	}
}

// TestAgentHookCmd covers the neutral plumbing around the guard: the three
// input forms (arguments, raw stdin, --from-json extraction), the -o arm, and
// the fail-open contract for unreadable input.
func TestAgentHookCmd(t *testing.T) {
	run := func(stdin string, args ...string) string {
		var out strings.Builder
		require.NoError(t, agentHookCmd(context.Background(), strings.NewReader(stdin), &out, args))
		return out.String()
	}

	// Argument input, default text output. A command with dash tokens rides
	// behind the -- terminator so the flag parser leaves it alone.
	assert.Equal(t, "pass\n", run("", "--", "ls", "-la"))
	assert.True(t, strings.HasPrefix(run("", "git", "stash"), "deny: "))

	// Raw stdin input ("-" and bare both read stdin).
	assert.True(t, strings.HasPrefix(run("git commit -m x", "-"), "advise: "))
	assert.True(t, strings.HasPrefix(run("git stash"), "deny: "))

	// --from-json extraction from a host event document.
	event := `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git stash"}}`
	got := run(event, "--from-json", "tool_input.command", "-o", "json")
	assert.Contains(t, got, `"decision": "deny"`)
	assert.Contains(t, got, `"schema_version": 1`)
	assert.Contains(t, got, "magus-vcs")

	// A template renders a host dialect; pass renders empty, deny fills it.
	tpl := `template={{if eq .decision "deny"}}{"permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}{{end}}`
	assert.Contains(t, run(event, "--from-json", "tool_input.command", "-o", tpl), `"permissionDecision":"deny"`)
	passEvent := `{"tool_input":{"command":"ls"}}`
	assert.Empty(t, strings.TrimSpace(run(passEvent, "--from-json", "tool_input.command", "-o", tpl)))

	// -o name is the bare decision word.
	assert.Equal(t, "deny\n", run(event, "--from-json", "tool_input.command", "-o", "name"))

	// Fail open: malformed JSON, a missing path, and empty stdin are all a pass.
	assert.Equal(t, "pass\n", run("not json", "--from-json", "tool_input.command"))
	assert.Equal(t, "pass\n", run(`{"other":1}`, "--from-json", "tool_input.command"))
	assert.Equal(t, "pass\n", run(""))
}

// TestExtractJSONString pins the dot-path walk and its error cases.
func TestExtractJSONString(t *testing.T) {
	doc := []byte(`{"a":{"b":"deep"},"top":"x","n":3}`)
	s, err := extractJSONString(doc, "a.b")
	require.NoError(t, err)
	assert.Equal(t, "deep", s)
	s, err = extractJSONString(doc, "top")
	require.NoError(t, err)
	assert.Equal(t, "x", s)

	_, err = extractJSONString(doc, "a.missing")
	assert.ErrorContains(t, err, "not found")
	_, err = extractJSONString(doc, "n")
	assert.ErrorContains(t, err, "not a string")
	_, err = extractJSONString(doc, "top.deeper")
	assert.ErrorContains(t, err, "no object")
	_, err = extractJSONString([]byte("nope"), "a")
	assert.ErrorContains(t, err, "not valid JSON")
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

// TestAgentHookPathMode covers --path, the definitive (non-heuristic) arm: a
// declared target output is denied. The deny path needs a real workspace, so it
// is exercised end to end elsewhere; what matters here is that the mode parses,
// shares the standard output arm, and FAILS OPEN on anything it cannot classify.
func TestAgentHookPathMode(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "unclassifiable path", args: []string{"--path", "-o", "name", "--", "/nonexistent/elsewhere.txt"}},
		{name: "empty path", args: []string{"--path", "-o", "name", "--", ""}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			require.NoError(t, agentHookCmd(context.Background(), strings.NewReader(""), &out, tt.args))
			assert.Equal(t, "pass\n", out.String(), "an unclassifiable path must fail open, never block an edit")
		})
	}
}
