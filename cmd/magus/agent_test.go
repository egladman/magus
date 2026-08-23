package main

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
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
func TestEvaluateBashGuard(t *testing.T) {
	tests := []struct {
		command string
		deny    bool
		context string // "" for none, else a substring the context must carry
	}{
		{command: "git stash", deny: true},
		{command: "git stash push -u", deny: true},
		{command: "cd /repo && git stash", deny: true},
		// Restoring a stash used to be treated as safe. It is not, in a repository with
		// more than one worktree: the stash stack is per-REPOSITORY, so an unqualified
		// pop takes whatever sits at stash@{0} - often another checkout's work - and
		// drops the entry once it applies. Naming the entry is the deliberate form.
		{command: "git stash pop", deny: true},
		{command: "git stash apply", deny: true},
		{command: "git stash drop", deny: true},
		{command: "git stash pop stash@{2}"},
		{command: "git stash apply stash@{0}"},
		// A PATH-SCOPED push moves only what it names, so the whole-tree reason
		// does not reach it. This is also the bootstrap-deadlock escape CLAUDE.md
		// documents - shelve the one hunk an old binary rejects, build, restore -
		// which this rule denied, putting the documented answer out of reach.
		{command: "git stash push -- magusfile.buzz"},
		{command: "git stash push -m wip -- spells/github/actions/spell.buzz"},
		// Naming nothing still stashes everything, pathspec-less flags included.
		{command: "git stash push", deny: true},
		{command: "git stash push -m wip", deny: true},
		{command: "git stash list"},
		{command: "git stash show"},
		// Deleting a worktree takes its uncommitted work with it, and in this repo that
		// work routinely belongs to another session.
		{command: "git worktree remove ../wt", deny: true},
		{command: "git worktree list"},
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
		{command: "git restore cmd/magus/agent.go", context: "magus-vcs-hygiene"},
		{command: "git checkout -- gen/", context: "role=output"},
		{command: "git checkout HEAD -- docs/gen", context: "role=output"},
		{command: "git clean -fd", deny: true},
		{command: "git clean -n"},
		{command: "git commit -m 'x'", context: "magus-vcs-hygiene"},
		// Push, not commit: committing mid-mess is ordinary, publishing is the
		// moment the work stops being yours alone.
		{command: "git push origin HEAD", context: "magus affected ci"},
		{command: "git push --force-with-lease", context: "magus affected ci"},
		// Stage-everything DENIES: `git add <path>` is an exact equivalent, so the
		// deny costs nothing, and one such call swept 69 files (a regenerated docs
		// site plus five untouched sources) into a commit about four methods.
		{command: "git add -A", deny: true},
		{command: "git add --all", deny: true},
		{command: "git add .", deny: true},
		{command: "git add -u", deny: true},
		// Deliberate staging is still only advised - that IS the replacement.
		{command: "git add cmd/magus/agent.go", context: "magus-vcs-hygiene"},
		{command: "git add docs/gen/index.html src/main.go", context: "magus-vcs-hygiene"},
		// A raw tool denies only when a registered spell renders that exact base
		// command and verb. Unsupported runners remain available: a guard funnels
		// capability Magus has, never removes capability it does not.
		{command: "go test ./...", deny: true},
		{command: "npm test"},
		{command: "npx prettier --check ."},
		{command: "pytest tests/"},
		{command: "cargo build --release", deny: true},
		{command: "gofmt -w x.go", deny: true},
		// Anchored to a COMMAND position, so the pattern appearing as TEXT is not a
		// match. This matters far more now these deny: `go test` and `git add -A`
		// turn up constantly in test data, docs, and commit messages, where
		// `git reset --hard` almost never did. Without anchoring, writing this very
		// test file through a shell heredoc was itself denied.
		{command: "echo 'run go test ./... to check'"},
		// A BACKSLASH-escaped separator is not a shell separator: it is a regex
		// alternation inside a quoted argument. Peeling must not reintroduce this -
		// splitting the line into segments does, which is why peeling substitutes.
		{command: `grep -n "golangci-lint\|mockery|gofmt" cmd/`},
		{command: "git commit -m 'stop using git add -A'", context: "magus-vcs-hygiene"},
		{command: "grep -rn 'go test' docs/", context: "knowledge graph"},
		// Still caught in every real command position.
		{command: "cd /repo && go test ./...", deny: true},
		{command: "make lint; pytest tests/"},
		{command: "go build ./... | tee log", deny: true},
		// Exempt: these bypass nothing, so advising on them is pure noise.
		{command: "gofmt -l ./libs"},
		{command: "gofmt -d x.go"},
		// `go build` denies at EVERY output path. Producing a binary is a write,
		// and the write rule has no destination-shaped exceptions.
		{command: "go build -o /tmp/magus ./cmd/magus", deny: true},
		{command: "go build ./...", deny: true},
		// PASS-THROUGH WRAPPERS. Each of these passed while its bare form denied,
		// because every raw-tool pattern is anchored at a command position and a
		// wrapper moves the real command off it. The guard peels them and judges
		// the payload, so the verdict is the inner command's on its own merits.
		{command: "mise exec -- env -u GOROOT go test ./...", deny: true},
		{command: "mise x -- go test ./...", deny: true},
		{command: "env -u GOROOT go test ./...", deny: true},
		{command: "GOFLAGS=-count=1 go test ./...", deny: true},
		{command: "GOFLAGS=-count=1 GOEXPERIMENT=jsonv2 go vet ./...", deny: true},
		{command: "bash -c 'go test ./...'", deny: true},
		{command: `sh -c "gofmt -w x.go"`, deny: true},
		{command: "timeout 300 go test ./...", deny: true},
		{command: "nohup pnpm build"},
		{command: "time npx prettier --write ."},
		{command: "nice -n 10 cargo build", deny: true},
		{command: "make deps && mise exec -- go generate ./...", deny: true},
		// Stacked wrappers reduce all the way down.
		{command: "env FOO=1 timeout 60 mise exec -- env -u GOROOT go test ./...", deny: true},
		// The wrapper is never the finding. Peeling exists so the payload can be
		// judged; only the actual command determines the verdict.
		{command: "mise exec -- magus run test"},
		{command: "env -u GOROOT magus run build"},
		{command: "mise exec -- env -u GOROOT go build -o /tmp/magus ./cmd/magus", deny: true},
		{command: "bash -c 'ls -la'"},
		{command: "mise install"},
		// `mise run <task>` runs a DECLARED mise task, not a smuggled command, so
		// it is not peeled - peeling would misattribute the task's contents.
		{command: "mise run setup"},
		// THE WRITE RULE. A build landing on a tracked path is a write, so only an
		// absolute -o (the documented `/tmp/magus` dev loop) is exempt.
		{command: "go build -o ./bin/magus ./cmd/magus", deny: true},
		{command: "go mod tidy", deny: true},
		// A raw tool is guarded only when a spell renders that exact base command
		// and verb. These programs have no direct rendered equivalent, so they
		// remain available instead of being denied by a stale generic list.
		{command: "go mod vendor"},
		{command: "govulncheck ./..."},
		{command: "ruff check ."},
		{command: "mypy ."},
		{command: "rustfmt src/main.rs"},
		{command: "vitest run"},
		{command: "buf lint", deny: true},
		{command: "golangci-lint run", deny: true},
		{command: "buf generate", deny: true},
		{command: "mockery"},
		// Trimming magus's own output with the shell. DENIED, not advised: as an
		// advisory this fired repeatedly in one session while its own author kept
		// piping magus into grep anyway - the same trained-reflex result the raw
		// tool advisory produced, so it gets the same answer.
		{command: "magus affected ci 2>&1 | tail -30", deny: true},
		{command: "/tmp/magus run test | head -5", deny: true},
		{command: "MAGUS_X=1 magus query foo | grep bar", deny: true},
		{command: "magus describe targets | wc -l", deny: true},
		{command: "magus run test -s | grep -i fail | head -3", deny: true},
		// Running magus from a COPY of the workspace in temp/scratchpad. Denied: the
		// verdict describes a tree nobody will ship. Taken from a real observed
		// command that chained a raw `go test`, four redirected magus runs and a
		// hand-rolled PASS/FAIL loop onto one `cd` into a scratchpad copy.
		{command: `SP=/private/tmp/claude-501/x/scratchpad; cd "$SP/fixci" && ./magus run generate:rw .`, deny: true},
		{command: "cd /tmp/copy && magus run lint .", deny: true},
		{command: "cd /private/tmp/x/scratchpad/repo && magus affected ci", deny: true},
		{command: "cd /var/folders/ab/xyz/T/repo && magus run test .", deny: true},
		// Timing magus with the shell. Advisory: magus already prints per-target
		// durations and a cached/ran verdict, and `-s` is what hides them - so the
		// shell timer measures the one number magus gave you and drops the rest.
		{command: "time magus run test .", context: "magus times itself"},
		{command: "time ./magus run test . -s", context: "cached"},
		// The wrapper peeling that judges `time go test` as `go test` would erase
		// the token this rule reads, so it works off the raw line.
		{command: "time go test ./...", deny: true},
		// Bounding magus with the shell. Advisory: run and affected take --timeout,
		// which cancels the run instead of signalling the process.
		{command: "timeout 300 magus run ci .", context: "--timeout 5m"},
		{command: "timeout -k 10s 5m ./magus affected ci --no-default-charms", context: "names the target"},
		{command: "timeout 60 sleep 30"},
		// Only run and affected carry the flag, so nothing else is advised toward it.
		{command: "timeout 60 magus graph build"},
		{command: "magus run test ."},
		// A cd WITHIN the workspace stays an advisory: naming the project is the
		// fix, and the run still describes the tree that ships.
		{command: "cd libs/gopherbuzz && magus run test .", context: "CWD-relative"},
		// --root is the sanctioned way to mean a different workspace, and a temp
		// path merely MENTIONED is not a relocation.
		{command: "magus run test . --root /tmp/other-workspace"},
		{command: "magus graph export -o json --tee /tmp/graph.json"},
		// REDIRECTS are denied on the same footing as pipes, and for the same
		// measured reason. These all passed until 2026-08-04, and one session used
		// every shape below to hide a gate's output from itself: `> /dev/null 2>&1`
		// reported an exit code with no cause and forced a re-run to learn it.
		{command: "magus run lint . > /tmp/x.txt", deny: true},
		{command: "magus run build . >> /tmp/log.txt", deny: true},
		{command: "magus run lint . -s 2>&1", deny: true},
		{command: "magus affected ci --silent > /dev/null 2>&1", deny: true},
		// --silent plus a redirect is the WORST case, not the careful one: silent
		// mode is quiet until it fails, so the redirect discards exactly the
		// diagnostics it exists to print.
		{command: "magus run lint . --silent > /tmp/x.txt", deny: true},
		// --tee is the sanctioned way to keep a copy: it writes the file AND shows
		// the output, so it is never denied.
		{command: "magus affected ci --tee /tmp/ci.log --silent"},
		// `magus query output <ref>` is the ONE exemption: a raw captured tool log
		// has no schema for magus to project, so searching it has no flag that
		// replaces it. The exemption covers redirects too.
		{command: "magus query output ref1a2b3c | grep -n error"},
		{command: "magus query output ref1a2b3c | tail -50"},
		{command: "magus query output ref1a2b3c > /tmp/out.txt"},
		// An input redirect FEEDS magus rather than hiding what it said.
		{command: "magus buzz - < script.buzz"},
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
		{command: `grep -rn "funcName" .`, context: "magus refs"},
		{command: "rg symbolName", context: "magus refs"},
		{command: `find . -name "*.go"`, context: "magus refs"},
		// magus is CWD-relative, so cd-then-magus is how the right command lands
		// on the wrong project. The project is an argument; only a different
		// WORKSPACE needs --root.
		{command: "cd libs/diagnostics && magus run test", context: "CWD-relative"},
		{command: "magus run test libs/diagnostics"},
		{command: "cd libs/diagnostics"},
		{command: "grep pattern onefile.txt"},
		{command: "grep -n x file.go"},
		{command: "cat x | grep y"},
		{command: "go version"},
		{command: "magus run test"},
		{command: "ls -la"},
		{command: "git status --porcelain"},
		{command: "git diff --cached --stat"},
		// Tree identity: a revision alone cannot identify a dirty tree, and
		// checkpoint adds the patch digest that can. Advise - reading the revision
		// is legitimate, and checkpoint is a superset rather than a substitute.
		{command: "git rev-parse HEAD", context: "magus vcs checkpoint"},
		{command: "git rev-parse --short HEAD", context: "magus vcs checkpoint"},
		// The build-stamp spelling: `git describe --tags` is asking for a version string
		// to embed, which a checkpoint does not replace.
		{command: "git describe --tags"},
		// `git stash create` returns a commit object without touching the working
		// tree or the stash stack, so it is not the destructive form.
		{command: "git stash create", context: "magus vcs checkpoint"},
		// rev-parse answers repository-LAYOUT questions too, and none of those is
		// asking which revision this is.
		{command: "git rev-parse --show-toplevel"},
		{command: "git rev-parse --git-dir"},
		{command: "git rev-parse --is-inside-work-tree"},
		// --abbrev-ref takes HEAD and answers with the BRANCH NAME, which a
		// checkpoint does not replace.
		{command: "git rev-parse --abbrev-ref HEAD"},
	}
	for _, tt := range tests {
		v := evaluateBashGuard(tt.command)
		if tt.deny {
			assert.NotEmpty(t, v.Deny, "%q must deny", tt.command)
			assert.Empty(t, v.Context, "%q denies, no context", tt.command)
			continue
		}
		cmds, _ := parseGuardCommands(tt.command)
		assert.Empty(t, v.Deny, "%q must not deny (parsed: %+v)", tt.command, cmds)
		if tt.context == "" {
			assert.Empty(t, v.Context, "%q must pass silently", tt.command)
		} else {
			assert.Contains(t, v.Context, tt.context, "%q context names the skill", tt.command)
		}
	}
}

// TestParseGuardCommands pins the resolution itself, separately from the
// verdicts it feeds. The decision table above proves the verdicts are right;
// this proves they are right for the right reason - that what the guard judges
// is the command the shell would actually run.
func TestParseGuardCommands(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    []guardCommand
	}{
		{"bare", "go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		// An assignment prefix is a separate AST field, so it never has to be
		// peeled and can never strand the payload.
		{"assignment prefix", "GOFLAGS=-count=1 go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"quoted assignment value", `GOFLAGS="-count=1 -v" go test ./...`, []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"env -u", "env -u GOROOT go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"mise exec", "mise exec -- go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"stacked wrappers", "mise exec -- env -u GOROOT go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"timeout duration is not the program", "timeout 300 go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"absolute path resolves to its base", "/usr/local/bin/go test ./...", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		// A -c payload is a script, so it is parsed rather than treated as a word.
		{"shell -c", "bash -c 'go test ./...'", []guardCommand{{Name: "go", Args: []string{"test", "./..."}}}},
		{"bundled -c flag", `sh -ec "go vet ./..."`, []guardCommand{{Name: "go", Args: []string{"vet", "./..."}}}},
		// Both sides of a compound are commands.
		{"compound", "make deps && mise exec -- go vet ./...", []guardCommand{
			{Name: "make", Args: []string{"deps"}},
			{Name: "go", Args: []string{"vet", "./..."}},
		}},
		// The tokenizing bugs the parser exists to make impossible: a separator
		// inside quotes is one word, structurally, not a pipe into another command.
		{"pipe inside quotes is one word", `grep -n "golangci-lint|gofmt" cmd/`, []guardCommand{
			{Name: "grep", Args: []string{"-n", "golangci-lint|gofmt", "cmd/"}},
		}},
		{"tool name in prose is an argument", "echo 'run go test to check'", []guardCommand{
			{Name: "echo", Args: []string{"run go test to check"}},
		}},
		// `mise run` is a declared task, not a smuggled command.
		{"mise run is not a wrapper", "mise run setup", []guardCommand{{Name: "mise", Args: []string{"run", "setup"}}}},
		// The wrapper is not the finding: a magus payload resolves and is judged
		// on its own merits, which is to say fine.
		{"magus payload", "mise exec -- magus run test", []guardCommand{{Name: "magus", Args: []string{"run", "test"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseGuardCommands(tt.command)
			assert.True(t, ok, "must parse")
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseGuardCommandsUnparsable pins the fail-open contract: a line the
// parser cannot read skips the raw-tool rule rather than guessing at it.
func TestParseGuardCommandsUnparsable(t *testing.T) {
	_, ok := parseGuardCommands("go test ./... && (")
	assert.False(t, ok)
	_, denied := firstRawToolDenied("go test ./... && (")
	assert.False(t, denied)
}

func TestRawToolGuardFollowsSpellCatalog(t *testing.T) {
	const spellName = "guard-catalog-test"
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(
		spellName,
		spells.WithTargets("verify"),
		spells.WithCommandRenderer(func(target string, _ []string) (string, []string, bool, error) {
			if target != "verify" {
				return "", nil, false, nil
			}
			return "catalog-tool", []string{"verify"}, true, nil
		}),
	))
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	match, ok := rawToolMatch(guardCommand{Name: "catalog-tool", Args: []string{"verify", "./..."}})
	require.True(t, ok)
	assert.Equal(t, guardToolMatch{spell: spellName, operation: "verify"}, match)
	assert.False(t, rawToolDenied(guardCommand{Name: "catalog-tool", Args: []string{"other"}}))
}

// The TOP-LEVEL TARGET is the form to teach, and it cannot be named: the guard
// ships in a binary and a workspace calls its targets whatever it likes, so the
// message points at discovery. The resolved spell op appears only as the
// arg-passthrough escape hatch, which is the one thing the target form does not
// cover as directly.
func TestRawToolGuardNamesTheReplacementAndForwarding(t *testing.T) {
	verdict := evaluateBashGuard("go test ./... -run TestFocused")
	require.NotEmpty(t, verdict.Deny)
	assert.Contains(t, verdict.Deny, "`magus run <target> <project>`")
	assert.Contains(t, verdict.Deny, "magus describe targets")
	assert.Contains(t, verdict.Deny, "magus run go::go-test")
	assert.Contains(t, verdict.Deny, "-- <tool-args>")
	assert.NotContains(t, verdict.Deny, "mise exec")

	// The op form must not lead: it is the exception, and a verdict that opens
	// with it teaches the dispreferred spelling to every reader.
	assert.Less(t, strings.Index(verdict.Deny, "magus run <target>"), strings.Index(verdict.Deny, "magus run go::go-test"),
		"the target form must precede the spell-op form")
}

// TestGuardVerdictsNameNoCanonicalTarget: test, build, lint, format and generate
// are THIS repository's target names, not magus vocabulary - another magusfile
// declares whatever it likes. A verdict compiled into the binary that instructs
// `magus run test` is therefore wrong in most workspaces it will ever judge, so a
// message names a target only when it resolved one from the workspace's own
// declarations (see regenerateAdvice), and otherwise points at discovery. `ci` is
// exempt: it is the one target name magus enforces (docs/recommendations.md), so
// `magus affected ci` is valid in every workspace.
func TestGuardVerdictsNameNoCanonicalTarget(t *testing.T) {
	canonical := regexp.MustCompile(`magus (?:run|affected) (?:test|build|lint|format|generate)\b`)
	for _, command := range []string{
		"go test ./...", "gofmt -w x.go", "go mod tidy",
		"git stash", "git stash pop", "git reset --hard", "git clean -fd",
		"git worktree remove ../wt", "git add -A", "git push origin HEAD",
		"git commit -m x", "git restore cmd/magus/agent.go",
		"magus describe targets | grep build", "magus run lint . > /tmp/x.txt",
		"cd /tmp/copy && magus run lint .", "cd libs/foo && magus run test",
		`grep -rn "funcName" .`, "magus notes edit x", "sed -i 's/a/b/' f.go",
	} {
		v := evaluateBashGuard(command)
		assert.NotRegexp(t, canonical, v.Deny, "%q names a canonical target in its deny reason", command)
		assert.NotRegexp(t, canonical, v.Context, "%q names a canonical target in its advisory", command)
	}
}

// TestGuardAdversarial is the hostile pass: every way found to smuggle a covered
// tool past the guard, and every way found to trip it on something innocent.
//
// It is written as an attack list rather than a feature list because that is how
// the failures actually arrived. The wrapper cases are not hypothetical. An
// agent does not need to intend evasion to evade; it just needs a habit and a
// toolchain that is awkward to reach. Treat any new entry here as a bug report,
// not a nice-to-have.
func TestGuardAdversarial(t *testing.T) {
	denied := []struct{ name, command string }{
		// Wrapper smuggling, the observed failure mode.
		{"mise exec", "mise exec -- go test ./..."},
		{"mise exec with tool pin", "mise exec go@1.26.5 -- go test ./..."},
		{"mise x", "mise x -- go test ./..."},
		{"env unset", "env -u GOROOT go test ./..."},
		{"env assignment operand", "env GOFLAGS=-v go test ./..."},
		{"env -i", "env -i go test ./..."},
		{"assignment prefix", "GOFLAGS=-count=1 go test ./..."},
		{"quoted assignment value", `GOFLAGS="-count=1 -v" go test ./...`},
		{"two assignment prefixes", "A=1 B=2 go test ./..."},
		{"timeout", "timeout 300 go test ./..."},
		{"timeout with flag", "timeout --foreground 5m go test ./..."},
		{"nice", "nice -n 10 go test ./..."},
		{"nice old syntax", "nice -10 go test ./..."},
		{"stdbuf", "stdbuf -o0 go test ./..."},
		{"nohup", "nohup go test ./..."},
		{"command builtin", "command go test ./..."},
		{"exec builtin", "exec go test ./..."},
		{"time", "time go test ./..."},
		{"xargs", "xargs -n1 go vet"},
		{"setsid", "setsid go test ./..."},
		{"sudo", "sudo go test ./..."},
		{"stacked wrappers", "mise exec -- env -u GOROOT timeout 60 go test ./..."},
		{"deeply stacked", "nohup nice -n 5 stdbuf -o0 env -u GOROOT go test ./..."},

		// Shell re-entry.
		{"bash -c", "bash -c 'go test ./...'"},
		{"sh -c double quotes", `sh -c "go test ./..."`},
		{"bundled flags", "bash -lc 'go test ./...'"},
		{"absolute shell path", "/bin/sh -c 'go test ./...'"},
		{"shell inside wrapper", "mise exec -- bash -c 'go test ./...'"},
		{"nested shells", `bash -c "sh -c 'go test ./...'"`},
		{"eval", `eval "go test ./..."`},

		// Program-name obfuscation. A regex could be beaten by every one of
		// these; a parser resolves the word first and then looks it up.
		{"absolute path", "/usr/local/bin/go test ./..."},
		{"relative path", "./bin/go test ./..."},
		{"quoted program", `"go" test ./...`},
		{"partially quoted program", `g"o" test ./...`},
		{"single-quoted fragment", "g'o' test ./..."},

		// Control flow: every branch is a command.
		{"semicolon", "make deps; go test ./..."},
		{"and-and", "make deps && go test ./..."},
		{"or-or", "make deps || go test ./..."},
		{"pipe", "go test ./... | tee log"},
		{"subshell", "(cd libs/diagnostics && go test ./...)"},
		{"brace block", "{ go test ./...; }"},
		{"if branch", "if true; then go test ./...; fi"},
		{"for body", "for d in a b; do go test ./$d; done"},
		{"while body", "while true; do go test ./...; done"},
		{"function body", "run() { go test ./...; }; run"},
		{"command substitution", "echo $(go test ./...)"},
		{"backtick substitution", "echo `go test ./...`"},
		{"background", "go test ./... &"},
		{"negated", "! go test ./..."},
		{"redirected", "go test ./... > /dev/null 2>&1"},

		// The write rule. `go build` produces a binary, so it is a write at EVERY
		// destination - including the /tmp dev loop that used to be exempt.
		{"relative build output", "go build -o ./bin/magus ./cmd/magus"},
		{"relative build output no dot", "go build -o bin/magus ./cmd/magus"},
		{"absolute build output", "go build -o /tmp/magus ./cmd/magus"},
		{"wrapped absolute build", "mise exec -- env -u GOROOT go build -o /tmp/magus ./cmd/magus"},
		{"bare build", "go build ./..."},
		{"go mod tidy", "go mod tidy"},
		{"gofmt -w", "gofmt -w ."},
		{"go generate", "go generate ./..."},
		{"wrapped write", "mise exec -- go generate ./..."},

		// Destructive git still denies however it is REACHED - the safety property
		// the old unanchored regexes existed for, kept by parsing both commands.
		{"stash after cd", "cd /repo && git stash"},
		{"stash in a subshell", "(cd libs/diagnostics && git stash)"},
		{"stash push", "git stash push -u"},
		{"bare stash", "git stash"},
		{"reset hard", "git reset --hard origin/main"},
		{"clean -fd", "git clean -fd"},
		{"checkout dot", "git checkout ."},
		{"checkout dash dash dot", "git checkout -- ."},
		{"restore dot", "git restore ."},
		{"stage everything", "git add -A"},
		{"stage dot", "git add ."},
		{"stash behind a wrapper", "bash -c 'git stash'"},
	}
	for _, tt := range denied {
		t.Run("deny/"+tt.name, func(t *testing.T) {
			cmds, _ := parseGuardCommands(tt.command)
			assert.NotEmpty(t, evaluateBashGuard(tt.command).Deny,
				"%q must deny (parsed: %+v)", tt.command, cmds)
		})
	}

	// The other half of the job. A guard that cries wolf gets switched off, and
	// these are the shapes that made it cry wolf: a tool NAME is not a tool CALL.
	allowed := []struct{ name, command string }{
		// The wrapper is never the finding.
		{"mise exec magus", "mise exec -- magus run test"},
		{"env magus", "env -u GOROOT magus run build"},
		{"mise run is a declared task", "mise run setup"},
		{"mise install", "mise install"},
		{"bash -c innocuous", "bash -c 'ls -la'"},

		// Documented exemptions.
		{"gofmt list", "gofmt -l ./libs"},
		{"gofmt diff", "gofmt -d x.go"},
		{"version probe", "golangci-lint --version"},
		{"go version", "go version"},
		{"go help", "go help test"},
		{"go mod download reads", "go mod download"},
		{"go list reads", "go list ./..."},
		{"go mod vendor has no spell operation", "go mod vendor"},
		{"prettier through an unsupported package runner", "npx prettier --write ."},

		// A tool name as DATA. Every one of these denied at some point.
		{"prose in echo", "echo 'run go test to check'"},
		{"prose in commit message", `git commit -m "stop reaching for go test"`},
		{"grep pattern", `grep -rn "go test" docs/`},
		{"pipe inside a quoted pattern", `grep -n "golangci-lint|gofmt" cmd/`},
		{"escaped alternation", `grep -n "golangci-lint\|mockery|gofmt" cmd/`},
		{"backtick in a quoted argument", "echo 'run `go test` first'"},
		{"tool name in a path", "cat cmd/magus/gofmt_test.go"},
		{"heredoc body is data", "cat <<'EOF'\ngo test ./...\nEOF"},

		// Neighbouring programs that merely start the same way.
		{"godoc", "godoc -http=:6060"},
		{"gopls", "gopls check ."},

		// Plain magus usage must never be obstructed.
		{"magus run", "magus run test"},
		{"magus affected", "magus affected ci"},

		// DESTRUCTIVE GIT COMMANDS AS PROSE. These denied until the git rules moved
		// onto the parser, and the cost was concrete: writing the magus-vcs-hygiene skill -
		// the document whose entire subject is these commands - through a heredoc
		// was blocked twice in one session.
		{"stash named in a heredoc", "cat <<'EOF' > s.md\nNever run git stash here.\nEOF"},
		{"stash named in an echo", "echo 'never run git stash to verify a build'"},
		{"clean named in a commit message", `git commit -m "document why git clean -fd is banned"`},
		{"reset --hard as documentation", "echo 'git reset --hard destroys untracked work'"},
		{"checkout dot inside a quoted string", `printf '%s' "git checkout . is denied"`},
		// Reading a stash stays safe, and so does restoring one you NAMED.
		{"stash list", "git stash list"},
		{"stash pop by ref", "git stash pop stash@{1}"},
		// A branch checkout is not a revert.
		{"checkout a branch", "git checkout main"},
		{"checkout -b", "git checkout -b feat/x"},
		// A scoped clean flag-less invocation is a dry run.
		{"clean -n", "git clean -n"},
		{"reset without --hard", "git reset HEAD~1"},
	}
	for _, tt := range allowed {
		t.Run("allow/"+tt.name, func(t *testing.T) {
			cmds, _ := parseGuardCommands(tt.command)
			assert.Empty(t, evaluateBashGuard(tt.command).Deny,
				"%q must not deny (parsed: %+v)", tt.command, cmds)
		})
	}
}

// TestGuardKnownHoles records what this guard CANNOT catch, as executable fact
// rather than as a caveat in a comment someone will not read.
//
// These are not todos. Each one is unclosable by anything short of running the
// command, and the entry exists so that nobody re-derives that the hard way, and
// so a future change that accidentally closes one is noticed. The conclusion to
// draw is the one the architecture already reflects: this guard is the fast,
// explanatory layer, and the filesystem sandbox is the enforcement. A hook that
// reads a command string is defence in depth, never a boundary.
func TestGuardKnownHoles(t *testing.T) {
	holes := []struct{ name, command, why string }{
		{
			"script file", "sh /tmp/build.sh",
			"the guard sees a path; the contents are not readable from the command line",
		},
		{
			"command substitution as the program", "$(which go) test ./...",
			"the program name is produced at runtime, so it has no literal value to resolve",
		},
		{
			"variable as the program", "$GO test ./...",
			"same: a parameter expansion has no value until the shell runs",
		},
		{
			"alias defined earlier in the session", "gt ./...",
			"an alias lives in the shell's state, not in the command line the hook receives",
		},
		{
			"make target that shells out", "make test",
			"the recipe is in a Makefile; only the make invocation is visible",
		},
	}
	for _, tt := range holes {
		t.Run(tt.name, func(t *testing.T) {
			assert.Empty(t, evaluateBashGuard(tt.command).Deny,
				"%q is a KNOWN HOLE (%s). If this now denies, the guard got stronger: move it into TestGuardAdversarial rather than deleting it.", tt.command, tt.why)
		})
	}
}

// TestDenyOutranksHeldAdvisory pins severity ordering across rules. A git rule
// that merely ADVISES used to answer first and return, so appending `git commit`
// to an otherwise-denied line downgraded the whole verdict to an advisory. That is
// not hypothetical: the observed command cd'd into a scratchpad copy, sent four
// magus runs to /dev/null, and ended in `git commit` - and the guard said "advise".
func TestDenyOutranksHeldAdvisory(t *testing.T) {
	const offending = `SP=/private/tmp/x/scratchpad; cd "$SP/fixci" && ./magus run generate . -s >/dev/null 2>&1`

	require.NotEmpty(t, evaluateBashGuard(offending).Deny, "the line alone must deny")
	for _, suffix := range []string{
		"; git commit -q -m x && git log --oneline -1",
		"; git status --porcelain",
		"; git add -- file.go",
	} {
		assert.NotEmpty(t, evaluateBashGuard(offending+suffix).Deny,
			"appending %q must not downgrade a deny to an advisory", suffix)
	}

	// The advisory still surfaces when nothing denies - holding it must not drop it.
	plain := evaluateBashGuard("git commit -q -m x")
	assert.Empty(t, plain.Deny)
	assert.NotEmpty(t, plain.Context, "a held advisory is still the answer when no rule denies")
}

// TestOutputGuardNamesTheReplacement pins the REASON each output denial gives,
// because a deny that only prohibits teaches the next reach for a workaround. The
// pattern being reinforced is "ask magus for the field" - so the pipe denial has
// to name the projection flags, and the redirect denial has to name --tee. Two
// distinct messages, because the right replacement differs by shape: a filter
// wanted one value, a redirect wanted a copy of the whole thing.
func TestOutputGuardNamesTheReplacement(t *testing.T) {
	piped := evaluateBashGuard("magus describe targets | grep build").Deny
	require.NotEmpty(t, piped)
	assert.Contains(t, piped, "-o name")
	assert.Contains(t, piped, "-o template=")
	assert.Contains(t, piped, "exit status", "a pipe replaces the exit status; that is why it is denied, not advised")

	redirected := evaluateBashGuard("magus affected ci --silent > /dev/null 2>&1").Deny
	require.NotEmpty(t, redirected)
	assert.Contains(t, redirected, "magus query output", "the captured log is already persisted; that is the replacement")
	assert.Contains(t, redirected, ".magus/logs/", "a failure names the full-log path, so capturing it is redundant")
	assert.Contains(t, redirected, "never console text",
		"--tee mirrors STRUCTURED output only; telling an agent to tee console output would write nothing")
	assert.Contains(t, redirected, "silent", "the -s + redirect combination is the case worth calling out")

	assert.NotEqual(t, piped, redirected, "the two shapes need different corrections")
}

func TestStageEverythingDenialNamesDirectStaging(t *testing.T) {
	verdict := evaluateBashGuard("git add -A")
	require.NotEmpty(t, verdict.Deny)
	assert.Contains(t, verdict.Deny, "magus describe file $(git diff --name-only)")
	assert.Contains(t, verdict.Deny, "git add -- <paths>")
	assert.NotContains(t, verdict.Deny, "magus vcs add")
}

// TestHookCmd covers the stdin-only guard boundary, the standard output arm,
// and the fail-open contract for empty input. Host-specific event extraction
// happens before the command is piped to magus.
// verdictDecisionRe finds every decision literal the guard assigns, in either
// spelling the code uses: the struct-literal `Decision: "pass"` and the
// assignment `verdict.Decision = "deny"`.
var verdictDecisionRe = regexp.MustCompile(`Decision(?::|\s*=)\s*"(\w+)"`)

// TestGuardDecisionsCoverEveryVerdictTheHookEmits keeps agent.GuardDecisions
// honest, which is what makes it usable as the contract the host-parity gate
// compares against (see TestHostGluesCoverTheGuardContract in dogfood_test.go).
//
// Two directions, and both are load-bearing:
//
//   - Every decision the hook can EMIT must be listed. A source scan rather
//     than a behavioral assertion, for the same reason
//     TestEveryCommandBindsDisplayFlags is one: catching an emitted-but-
//     unlisted decision at runtime would mean enumerating every input that
//     produces every verdict, and that enumeration is the thing that goes
//     stale.
//   - Every listed decision must RENDER distinctly. writeGuardVerdict's text
//     arm falls through to "pass" for anything it does not know, so a decision
//     added to the list but not to the renderer would report itself as a pass -
//     the quietest possible wrong answer.
//
// A decision that fails either direction is not a contract a host glue can be
// asked to declare a stance on.
func TestGuardDecisionsCoverEveryVerdictTheHookEmits(t *testing.T) {
	listed := make(map[string]bool)
	for _, d := range agent.GuardDecisions() {
		listed[d] = true
	}

	body, err := os.ReadFile("agent.go")
	require.NoError(t, err)
	found := false
	for _, m := range verdictDecisionRe.FindAllStringSubmatch(string(body), -1) {
		found = true
		assert.True(t, listed[m[1]],
			"agent.go emits the verdict decision %q, which agent.GuardDecisions does not list.\n"+
				"Add it there first: the host-parity gate asks every glue to declare a stance per\n"+
				"decision, and a decision missing from the contract is one no host was asked about.", m[1])
	}
	require.True(t, found, "found no decision literals in agent.go; verdictDecisionRe no longer matches how the guard assigns a decision")

	for _, d := range agent.GuardDecisions() {
		var out strings.Builder
		require.NoError(t, writeGuardVerdict(&out, OutputOptions{Format: FormatText}, guardVerdict{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      d,
			Reason:        "why",
			Context:       "why",
		}))
		assert.True(t, strings.HasPrefix(out.String(), d),
			"writeGuardVerdict renders the listed decision %q as %q: its text arm has no case for it and\n"+
				"fell through to the default, so the verdict reads as a pass.", d, strings.TrimSpace(out.String()))
	}
}

// TestAdviseInstalledSkillWrite pins the discriminator: the STAMP decides, not
// the path. Both files below sit in the same directory under a magus-* name,
// and only one of them is magus's to overwrite.
func TestAdviseInstalledSkillWrite(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	installed := write(".claude/skills/magus-run/SKILL.md", "---\nname: magus-run\nmetadata:\n  source: magus\n---\n\n# Running work\n")
	got := adviseInstalledSkillWrite(installed)
	assert.Contains(t, got, "INSTALLED skill")
	assert.Contains(t, got, "magus-workspace-rules")

	// A workspace's own skill lives in the same directory and must draw silence:
	// telling an author their hand-written file is generated is worse than
	// saying nothing.
	local := write(".claude/skills/"+agent.LocalSkillName+"/SKILL.md", "---\nname: "+agent.LocalSkillName+"\nmetadata:\n  source: workspace\n---\n\n# Our rules\n")
	assert.Empty(t, adviseInstalledSkillWrite(local))

	// Not a skill file, not in a skill directory, and not there at all.
	assert.Empty(t, adviseInstalledSkillWrite(write(".claude/skills/magus-run/README.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(write("docs/SKILL.md", "source: magus")))
	assert.Empty(t, adviseInstalledSkillWrite(filepath.Join(dir, ".claude", "skills", "magus-vcs-hygiene", "SKILL.md")))
}

func TestHookCmd(t *testing.T) {
	auditDir := t.TempDir()
	run := func(stdin string, args ...string) string {
		var out strings.Builder
		// The display flags live on a package global and default to whatever it
		// already holds, so one case passing -o json would otherwise leak into
		// every later case. Harmless in the real CLI (one command per process),
		// load-bearing here - and the reason this reset exists rather than a
		// local output flag, which is what the command used to have.
		global = globalFlags{}
		ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: auditDir, workspace: "/repo/magus"})
		// A deny now exits non-zero, which is the whole point of the guard: the
		// rendered verdict on stdout is unchanged, and the error is the blocking
		// signal a host reads. Anything OTHER than that is a real failure.
		if err := hookCmd(ctx, strings.NewReader(stdin), &out, args); err != nil {
			var silent errSilent
			require.ErrorAs(t, err, &silent)
			require.Equal(t, guardDenyExitCode, silent.exitCode)
		}
		return out.String()
	}

	assert.True(t, strings.HasPrefix(run("git commit -m x"), "advise: "))
	assert.True(t, strings.HasPrefix(run("git stash"), "deny: "))

	got := run("git stash", "-o", "json")
	assert.Contains(t, got, `"decision": "deny"`)
	assert.Contains(t, got, `"schema_version": 1`)
	assert.Contains(t, got, "magus-vcs-hygiene")

	// A template renders a host dialect; pass renders empty, deny fills it.
	tpl := `template={{if eq .decision "deny"}}{"permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}{{end}}`
	assert.Contains(t, run("git stash", "-o", tpl), `"permissionDecision":"deny"`)
	assert.Empty(t, strings.TrimSpace(run("ls", "-o", tpl)))

	// -o name is the bare decision word.
	assert.Equal(t, "deny\n", run("git stash", "-o", "name"))

	// Fail open on empty stdin; positional input is rejected instead of quietly
	// creating a second input contract.
	assert.Equal(t, "pass\n", run(""))
	var positionalOut strings.Builder
	err := hookCmd(context.Background(), strings.NewReader(""), &positionalOut, []string{"git", "stash"})
	require.ErrorContains(t, err, "no positional arguments")
}

func TestHookCmd_AppendsNormalizedActivity(t *testing.T) {
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	// git stash is denied, so hookCmd reports it by exiting non-zero while still
	// rendering the verdict in the requested format.
	err := hookCmd(ctx, strings.NewReader("git stash"), &out, []string{"-o", "name"})
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	require.Equal(t, guardDenyExitCode, silent.exitCode)
	assert.Equal(t, "deny\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, trail.KindAgentCommand, got.Kind)
	assert.Equal(t, "agent", got.Actor)
	assert.Equal(t, "/repo/magus", got.Workspace)
	assert.Equal(t, "shell.command", got.Action)
	assert.Equal(t, "guard: deny", got.Preview)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"shell.command","command":"git stash"}`, string(body))
	body, err = trail.ReadBlob(dir, got.ResponseRef)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"schema_version":1`)
	assert.Contains(t, string(body), `"decision":"deny"`)
	assert.Contains(t, string(body), `"reason":`)
}

func TestHookCmd_PathAndEmptyInputActivity(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "-o", "name"}))
	assert.Equal(t, "advise\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, trail.KindAgentCommand, got.Kind)
	assert.Equal(t, "agent", got.Actor)
	assert.Equal(t, "file.write", got.Action)
	assert.Equal(t, "guard: advise", got.Preview)
	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"path":"AGENTS.md","tool":"file.write"}`, string(body))

	emptyDir := t.TempDir()
	emptyCtx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: emptyDir, workspace: "/repo/magus"})
	out.Reset()
	require.NoError(t, hookCmd(emptyCtx, strings.NewReader(""), &out, nil))
	assert.Equal(t, "pass\n", out.String())
	events, err = trail.ReadRecent(emptyDir, 1)
	require.NoError(t, err)
	assert.Empty(t, events, "a hook with no command/path has no observable invocation to record")
}

// TestHookCmd_RecordsHostAttribution covers the --agent-name/--session/--event flags: the wrapper is
// the only party that knows which agent host ran the hook, so what it passes must survive onto
// the event line, not only into the request blob.
func TestHookCmd_RecordsHostAttribution(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &out,
		[]string{"--agent-name", "claude-code", "--session", "abc123", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	// Whole-struct assertion with the content-addressed and clock-dependent fields lifted out
	// first, so a new field on Event cannot be silently dropped by the hook producer.
	requestRef, responseRef := got.RequestRef, got.ResponseRef
	got.Ts, got.RequestRef, got.ResponseRef = 0, "", ""
	got.RequestBytes, got.ResponseBytes = 0, 0
	assert.Equal(t, trail.Event{
		Kind:      trail.KindAgentCommand,
		Actor:     "agent",
		Host:      "claude-code",
		Session:   "abc123",
		Workspace: "/repo/magus",
		Action:    "shell.command",
		Outcome:   trail.OutcomeOK,
		Preview:   "guard: pass",
	}, got)

	body, err := trail.ReadBlob(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"abc123","event":"PreToolUse","tool":"shell.command","command":"ls"}`, string(body))
	body, err = trail.ReadBlob(dir, responseRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"decision":"pass"}`, string(body))
}

// TestHookCmd_AttributionIsOptional holds the fail-open contract: attribution is best-effort
// metadata, so a wrapper that supplies none still gets a verdict and still records an event.
func TestHookCmd_AttributionIsOptional(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("ls"), &out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Empty(t, got.Host)
	assert.Empty(t, got.Session)

	// Omitted rather than recorded empty: the blob says nothing was known, not that the host is "".
	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"shell.command","command":"ls"}`, string(body))
}

// TestHookCmd_ObserveRecordsWithoutJudging pins what --observe is for. AGENTS.md is the exact
// path TestHookCmd_PathAndEmptyInputActivity gets an ADVISE for as a write, so a pass here is
// specifically the observation being exempted from the write rules rather than the rule
// failing to fire. Without it, a hook wired to a host's read tool would advise "you are
// editing a declared output" at a file the agent only opened.
func TestHookCmd_ObserveRecordsWithoutJudging(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "file.read", got.Action, "a reach is recorded under its own label, not as a write")

	// "observed", not "guard: pass". The trail already distinguishes the two, and recording
	// a verdict here would have every read claim the guard ran and cleared it - the exact
	// conflation --observe exists to remove. The wire verdict the host reads is still pass.
	assert.Equal(t, "observed", got.Preview)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"tool":"file.read","path":"AGENTS.md"}`, string(body))
	body, err = trail.ReadBlob(dir, got.ResponseRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"decision":""}`, string(body))
}

// TestHookCmd_ObserveOutranksPath holds the precedence the wrapper depends on: a host whose
// read event carries a file_path will send --observe alongside the envelope that sets --path,
// and the observation must win. The reverse would silently restore the false advisory.
func TestHookCmd_ObserveOutranksPath(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("AGENTS.md"), &out, []string{"--path", "--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "file.read", events[0].Action)
}

// TestHookCmd_RecordsTranscriptPath covers the session-to-transcript link. The id groups a
// session's events; the path is what a reader follows to see the rest. magus records the
// POINTER and never opens the file, which is what keeps the trail paths-and-timings.
func TestHookCmd_RecordsTranscriptPath(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	envelope := `{"hook_event_name":"PreToolUse","session_id":"s1","transcript_path":"/tmp/t.jsonl","tool_input":{"command":"ls"}}`
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "s1", got.Session)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"session":"s1","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"shell.command","command":"ls"}`, string(body))
}

// TestHookCmd_TranscriptFlagRecordsThePointer covers the FLAG path, which is the one the
// shipped observe template actually uses. That template extracts the path with jq and pipes
// plain text rather than the whole event, so nothing about the envelope is available to it -
// without the flag the transcript link exists only for hosts that pipe raw JSON, which is
// none of the ones magus ships a template for.
func TestHookCmd_TranscriptFlagRecordsThePointer(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader("internal/trail/trail.go"), &out,
		[]string{"--observe", "--agent-name", "claude-code", "--session", "s9", "--transcript", "/tmp/t.jsonl", "--event", "PreToolUse", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]
	assert.Equal(t, "file.read", got.Action)
	assert.Equal(t, "s9", got.Session)

	body, err := trail.ReadBlob(dir, got.RequestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"s9","transcript":"/tmp/t.jsonl","event":"PreToolUse","tool":"file.read","path":"internal/trail/trail.go"}`, string(body))
}

// TestHookCmd_ObserveWithNoInputRecordsNothing: a wrapper whose host event carried no path
// has nothing to report, and an observation with no subject is dropped like any other empty
// one rather than being invented as "." - which would claim a reach the host never described.
func TestHookCmd_ObserveWithNoInputRecordsNothing(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(""), &out, []string{"--observe", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	assert.Empty(t, events)
}

// TestHookCmd_RecordsSpawnFromEnvelope covers the delegation surface end to end: a host payload
// carrying a prompt rather than a command is recorded as a spawn, with the handed context in the
// blob and the cooperative delegation marker stamped onto the event.
//
// It also pins the thing that must NOT happen. The prompt below quotes `git stash`, which the
// command guard denies. A spawn is not a guard surface, so the verdict is a pass and the
// delegation is recorded rather than blocked for describing a denied command.
func TestHookCmd_RecordsSpawnFromEnvelope(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})
	envelope := `{"hook_event_name":"PreToolUse","session_id":"abc123","tool_name":"Task",` +
		`"tool_input":{"description":"audit the store","subagent_type":"Explore",` +
		`"prompt":"delegation: notes-store-6b\nDo not run git stash anywhere."}}`

	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out,
		[]string{"--agent-name", "claude-code", "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	got := events[0]

	requestRef := got.RequestRef
	got.Ts, got.RequestRef, got.RequestBytes = 0, "", 0
	assert.Equal(t, trail.Event{
		Kind:       trail.KindAgentSpawn,
		Actor:      "agent",
		Host:       "claude-code",
		Session:    "abc123",
		Workspace:  "/repo/magus",
		Action:     "Explore",
		Delegation: "notes-store-6b",
		Outcome:    trail.OutcomeOK,
	}, got)

	body, err := trail.ReadBlob(dir, requestRef)
	require.NoError(t, err)
	assert.JSONEq(t, `{"schema_version":1,"host":"claude-code","session":"abc123","event":"PreToolUse",`+
		`"tool":"Task","child":"Explore","delegation":"notes-store-6b",`+
		`"context":"delegation: notes-store-6b\nDo not run git stash anywhere."}`, string(body))
}

// TestHookCmd_SpawnWithoutMarkerOrLabel holds the two halves of the cooperative contract: an
// orchestrator that writes no marker still gets an audited handoff, just an uncorrelated one,
// and a host whose payload names no callee still records a spawn.
func TestHookCmd_SpawnWithoutMarkerOrLabel(t *testing.T) {
	global = globalFlags{}
	dir := t.TempDir()
	ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: dir, workspace: "/repo/magus"})

	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(`{"tool_input":{"prompt":"go and audit the store"}}`),
		&out, []string{"-o", "name"}))
	assert.Equal(t, "pass\n", out.String())

	events, err := trail.ReadRecent(dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, trail.KindAgentSpawn, events[0].Kind)
	assert.Equal(t, "agent.spawn", events[0].Action)
	assert.Empty(t, events[0].Delegation)
}

// TestDecodeHookEnvelope_CommandStillWinsOverPrompt guards the ordering that makes the spawn
// branch purely additive: a payload carrying both is judged as the command it carries, so no
// envelope the guard already evaluated can start skipping the guard because a prompt appeared
// beside it.
func TestDecodeHookEnvelope_CommandStillWinsOverPrompt(t *testing.T) {
	req, ok := decodeHookEnvelope(`{"tool_input":{"command":"git stash","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "git stash", req.Value)
	assert.False(t, req.IsSpawn)

	req, ok = decodeHookEnvelope(`{"tool_input":{"file_path":"MAGUS.md","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath)
	assert.False(t, req.IsSpawn)
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

// TestHookPathMode covers --path, the definitive (non-heuristic) arm: a
// declared target output is denied. The deny path needs a real workspace, so it
// is exercised end to end elsewhere; what matters here is that the mode parses,
// shares the standard output arm, and FAILS OPEN on anything it cannot classify.
func TestHookPathMode(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{name: "unclassifiable path", args: []string{"--path", "-o", "name"}},
		{name: "empty path", args: []string{"--path", "-o", "name"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			ctx := context.WithValue(context.Background(), hookActivityLocationKey{}, hookActivityLocation{base: t.TempDir(), workspace: "/repo/magus"})
			input := ""
			if tt.name == "unclassifiable path" {
				input = "/nonexistent/elsewhere.txt"
			}
			require.NoError(t, hookCmd(ctx, strings.NewReader(input), &out, tt.args))
			assert.Equal(t, "pass\n", out.String(),
				"an unclassifiable path says nothing: an advisory fired on a guess trains the reader to ignore it")
		})
	}
}

// fleetFixture stands up a workspace root and a delegation ledger holding delegations, and
// returns the context pinning both plus the root. Everything lands in temporary
// directories, so a guard test never reads or writes the checkout's real ledger.
func fleetFixture(t *testing.T, delegations ...types.Delegation) (context.Context, string) {
	t.Helper()
	root, cacheDir := t.TempDir(), t.TempDir()
	store := ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root})
	for _, u := range delegations {
		_, err := store.Put(t.Context(), u)
		require.NoError(t, err)
	}
	location := hookActivityLocation{base: cacheDir, workspace: root}
	return context.WithValue(t.Context(), hookActivityLocationKey{}, location), root
}

// fleetDelegations is the two-delegation plan most cases below grade against: two live workers with
// disjoint owned paths, one of them declaring a forbidden subtree inside its own.
func fleetDelegations() []types.Delegation {
	return []types.Delegation{
		{
			ID:         "delegation-a",
			Goal:       "own the ledger store\nacceptance: List stays cheap",
			OwnedPaths: []string{"internal/ledger/**"},
			State:      types.StateRunning,
		},
		{
			ID:             "delegation-b",
			Goal:           "grade writes in the guard",
			OwnedPaths:     []string{"cmd/magus/**", "docs/guard.md"},
			ForbiddenPaths: []string{"cmd/magus/gen/**"},
			State:          types.StateDeclared,
		},
	}
}

// TestGradeDelegatedWriteDenies pins the two denials and the fact each one must carry:
// the owning delegation's id, its goal's first line, and a next step. A denial that only says
// no sends the agent around the guard, which is the failure the whole ledger design is
// built to avoid.
func TestGradeDelegatedWriteDenies(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)

	t.Run("inside another live delegation's owned paths", func(t *testing.T) {
		got := gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "delegation-a", "the denial must name the owner")
		assert.Contains(t, got.Reason, "own the ledger store", "the denial must carry the owner's goal")
		assert.NotContains(t, got.Reason, "acceptance:",
			"only the goal's FIRST line belongs in a denial; the criteria block would bury the next step")
		assert.Contains(t, got.Reason, "re-partition", "the denial must name a next step")
		assert.Contains(t, got.Reason, "delegation-b", "the denial must say who magus thinks is writing")
	})

	t.Run("inside the acting delegation's own forbidden paths", func(t *testing.T) {
		// Also pins the precedence: cmd/magus/gen is inside delegation-b's owned tree AND on its
		// forbidden list, and the more specific declaration is the one that decides.
		got := gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "cmd/magus/gen/cli_flags.go"))
		require.Equal(t, "deny", got.Decision)
		assert.Contains(t, got.Reason, "FORBIDDEN")
		assert.Contains(t, got.Reason, "delegation-b")
		assert.Contains(t, got.Reason, "cmd/magus/gen/**", "the denial must quote the declaration it matched")
	})
}

// TestGradeDelegatedWritePasses covers the silences. Each is a case where the guard has
// no opinion, which is different from clearing the write: a later rule still gets to
// speak, and the empty Decision is what leaves room for it.
func TestGradeDelegatedWritePasses(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)

	t.Run("inside the acting delegation's own owned paths", func(t *testing.T) {
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "cmd/magus/agent.go")).Decision)
	})

	t.Run("ground no live delegation claims", func(t *testing.T) {
		// An orchestrator's owned set is a plan, not a census. Denying here would block a
		// delegation from a file nobody is competing for.
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "README.md")).Decision)
	})

	t.Run("outside the workspace", func(t *testing.T) {
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(t.TempDir(), "elsewhere.go")).Decision)
	})

	t.Run("un-enrolled on ground no delegation claims", func(t *testing.T) {
		assert.Empty(t, gradeDelegatedWrite(ctx, "", filepath.Join(root, "README.md")).Decision)
	})
}

// TestGradeDelegatedWriteIdleFleet is the zero-cost contract: with nothing to grade
// against, the guard reads the ledger and then says nothing, whatever the path.
func TestGradeDelegatedWriteIdleFleet(t *testing.T) {
	t.Run("no ledger at all", func(t *testing.T) {
		ctx, root := fleetFixture(t)
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("every delegation terminal", func(t *testing.T) {
		delegations := fleetDelegations()
		delegations[0].State, delegations[1].State = types.StatePass, types.StateNoReturn
		ctx, root := fleetFixture(t, delegations...)
		// A finished delegation has stopped competing for its paths, which is the rule
		// types.delegationOverlaps applies when it decides which pairs to report.
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no state recorded", func(t *testing.T) {
		delegations := fleetDelegations()
		delegations[0].State, delegations[1].State = "", ""
		ctx, root := fleetFixture(t, delegations...)
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "internal/ledger/store.go")).Decision)
	})

	t.Run("no trail location", func(t *testing.T) {
		// Pinned to an EMPTY location rather than left unpinned: an unpinned context sends
		// hookActivityTrail up from the CWD to this checkout's real cache dir, and the test
		// would then grade against whatever plan the developer is actually running.
		ctx := context.WithValue(t.Context(), hookActivityLocationKey{}, hookActivityLocation{})
		assert.Empty(t, gradeDelegatedWrite(ctx, "delegation-b", "internal/ledger/store.go").Decision)
	})
}

// TestGradeDelegatedWriteUnenrolled is the doctrine case: a writer magus cannot attribute
// is told what it is walking into and is never stopped. magus cannot tell "not part of the
// fleet" from "part of it and not saying so", and blocking a person in their own checkout
// is the wrong way to be wrong.
func TestGradeDelegatedWriteUnenrolled(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)
	got := gradeDelegatedWrite(ctx, "", filepath.Join(root, "internal/ledger/store.go"))
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "delegation-a", "the advisory must name the delegation already working there")
	assert.Contains(t, got.Context, "own the ledger store")
	assert.Contains(t, got.Context, "MAGUS_DELEGATION", "the advisory must say how to enroll")
	assert.Contains(t, got.Context, "seatbelt", "the advisory must say why it is not a block")
}

// TestGradeDelegatedWriteInvalidDelegationID pins the treated-as-absent contract. A typo'd id
// must not silently buy un-enrolled treatment: erroring would block the tool call over
// metadata, so the notice is the whole signal the writer gets.
func TestGradeDelegatedWriteInvalidDelegationID(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)

	t.Run("on unclaimed ground the notice stands alone", func(t *testing.T) {
		got := gradeDelegatedWrite(ctx, "delegation b!", filepath.Join(root, "README.md"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid delegation id")
		assert.Contains(t, got.Context, "MAGUS_DELEGATION")
	})

	t.Run("over-long ids are invalid too", func(t *testing.T) {
		got := gradeDelegatedWrite(ctx, strings.Repeat("u", types.MaxDelegationIDLen+1), filepath.Join(root, "README.md"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid delegation id")
	})

	t.Run("on owned ground it advises rather than denying", func(t *testing.T) {
		// The id is unusable, so the write is graded as un-enrolled - and an un-enrolled
		// write is never denied, even on another delegation's ground.
		got := gradeDelegatedWrite(ctx, "delegation b!", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "not a valid delegation id")
		assert.Contains(t, got.Context, "delegation-a")
	})

	t.Run("a valid id nobody declared is un-enrolled, not denied", func(t *testing.T) {
		got := gradeDelegatedWrite(ctx, "delegation-z", filepath.Join(root, "internal/ledger/store.go"))
		require.Equal(t, "advise", got.Decision)
		assert.Contains(t, got.Context, "delegation-a")
		assert.NotContains(t, got.Context, "not a valid delegation id")
	})
}

// TestGradeDelegatedWriteCorruptLedger is the fail-open case. A guard that blocked on a
// file it cannot parse would take the whole fleet down with one bad write; it says so
// instead, because a boundary that silently stopped being checked looks exactly like a
// fleet nobody declared.
func TestGradeDelegatedWriteCorruptLedger(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)
	location := ctx.Value(hookActivityLocationKey{}).(hookActivityLocation)
	require.NoError(t, os.WriteFile(filepath.Join(location.base, "ledger", "units.json"), []byte("{not json"), 0o644))

	got := gradeDelegatedWrite(ctx, "delegation-b", filepath.Join(root, "internal/ledger/store.go"))
	assert.NotEqual(t, "deny", got.Decision, "a ledger magus cannot read must never block an edit")
	require.Equal(t, "advise", got.Decision)
	assert.Contains(t, got.Context, "could not be read")
	assert.Contains(t, got.Context, "magus_ledger", "the advisory must name the surface that re-declares the plan")
}

// TestDeclarationCovering pins the glob vocabulary a denial rests on. The precision matters
// more here than in types.pathsIntersect, which over-reports on purpose: this answer blocks
// a write, and a guard that blocks legitimate edits is one agents learn to route around.
func TestDeclarationCovering(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		decl, rel string
		want      bool
	}{
		{"internal/ledger", "internal/ledger/store.go", true},
		{"internal/ledger/**", "internal/ledger/sub/store.go", true},
		{"internal/ledger/*.go", "internal/ledger/store.go", true},
		{"internal/ledger/*.go", "internal/ledger/sub/store.go", false},
		{"cmd/magus/agent.go", "cmd/magus/agent.go", true},
		{"cmd/magus/agent.go", "cmd/magus/agent_test.go", false},
		{"internal/ledger", "internal/ledgerkeeper/store.go", false},
		{"console/src/**/*.ts", "console/src/a/b.ts", true},
		// The case types.pathsIntersect deliberately gets "wrong": two delegations splitting one
		// directory by extension do NOT collide, and truncating both to "console/src" would
		// deny an edit nobody is competing for.
		{"console/src/**/*.css", "console/src/a/b.ts", false},
		{"**/*.go", "internal/ledger/store.go", true},
		{"", "internal/ledger/store.go", false},
		{"   ", "internal/ledger/store.go", false},
		{".", "internal/ledger/store.go", false},
		{"/", "internal/ledger/store.go", false},
	} {
		t.Run(tt.decl+" vs "+tt.rel, func(t *testing.T) {
			_, got := declarationCovering([]string{tt.decl}, tt.rel)
			assert.Equal(t, tt.want, got)
		})
	}

	t.Run("returns the declaration it matched, verbatim", func(t *testing.T) {
		decl, ok := declarationCovering([]string{"docs/**", "internal/ledger/**"}, "internal/ledger/store.go")
		require.True(t, ok)
		assert.Equal(t, "internal/ledger/**", decl, "a denial quotes the declaration as the orchestrator wrote it")
	})
}

// The delegation-id shape itself is pinned in internal/trail's TestValidDelegationID; the guard's
// treated-as-absent behavior for a bad id is pinned by TestGradeDelegatedWriteInvalidDelegationID.

// TestHookCmdGradesAgainstTheLedger drives the wire contract rather than the grader: the
// flag reaches the rule, a denial exits with the blocking status, and the flag outranks
// the environment.
func TestHookCmdGradesAgainstTheLedger(t *testing.T) {
	ctx, root := fleetFixture(t, fleetDelegations()...)
	run := func(stdin string, args ...string) (string, error) {
		global = globalFlags{}
		var out strings.Builder
		err := hookCmd(ctx, strings.NewReader(stdin), &out, args)
		return out.String(), err
	}
	owned := filepath.Join(root, "internal/ledger/store.go")

	t.Run("--delegation denies a write into another delegation's paths", func(t *testing.T) {
		got, err := run(owned, "--path", "--delegation", "delegation-b", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
		assert.Equal(t, "deny\n", got)
	})

	t.Run("MAGUS_DELEGATION supplies the default", func(t *testing.T) {
		t.Setenv(envHookDelegation, "delegation-b")
		_, err := run(owned, "--path", "-o", "name")
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		require.Equal(t, guardDenyExitCode, silent.exitCode)
	})

	t.Run("the flag wins over the environment", func(t *testing.T) {
		// The path belongs to delegation-a. Acting AS delegation-a it is the writer's own ground, so a
		// pass here proves the flag replaced the environment's delegation-b rather than joining it.
		t.Setenv(envHookDelegation, "delegation-b")
		_, err := run(owned, "--path", "--delegation", "delegation-a", "-o", "name")
		require.NoError(t, err, "acting as the owner must not be denied")
	})
}

// TestAdviseMemoryWrite pins the nudge to the two cross-host instruction files
// and to a capture-not-replication wording: it must name the journal WITHOUT
// telling the reader not to write the file, since host instructions belong
// exactly where they are being written.
func TestAdviseMemoryWrite(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"AGENTS.md", "CLAUDE.md", "claude.md", "/repo/nested/AGENTS.md", "  AGENTS.md  "} {
		advice := adviseMemoryWrite(path)
		require.NotEmpty(t, advice, "expected a memory advisory for %q", path)
		assert.Contains(t, advice, "magus memory put", "the advisory must name the command it is routing to")
	}
	for _, path := range []string{"", "README.md", "MAGUS.md", "docs/agents.md.tmpl", "agents.mdx"} {
		assert.Empty(t, adviseMemoryWrite(path), "no advisory belongs on %q", path)
	}
}

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

// TestDecodeHookEnvelope pins reading a host's hook payload directly. Without it, wiring
// the guard means `jq -r .tool_input.command | magus hook` - an extra dependency on the
// critical path of every tool call, in the one place that must not fail.
func TestDecodeHookEnvelope(t *testing.T) {
	cmdPayload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash",` +
		`"tool_input":{"command":"magus run ci | tail"}}`
	req, ok := decodeHookEnvelope(cmdPayload)
	require.True(t, ok)
	assert.Equal(t, "magus run ci | tail", req.Value)
	assert.False(t, req.IsPath)
	assert.Equal(t, "s1", req.Who.Session)
	assert.Equal(t, "PreToolUse", req.Who.Event)

	// A file_path payload is a WRITE, so the envelope decides the --path question too.
	writePayload := `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"MAGUS.md"}}`
	req, ok = decodeHookEnvelope(writePayload)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath, "a file_path payload must be judged as a path, not as a command")

	// Anything that is not a usable envelope is left alone for the bare-command form.
	for _, raw := range []string{
		"magus run ci | tail",           // a plain command
		`{"tool_input":{}}`,             // an envelope with nothing to judge
		`{not json`,                     // malformed
		`{"tool_input":{"command":""}}`, // explicitly empty
	} {
		_, ok := decodeHookEnvelope(raw)
		assert.False(t, ok, "must fall through to the literal form: %q", raw)
	}
}

// TestEnforceVerdictBlocksOnlyDeny pins the half that makes the guard real. Every rule was
// reachable and correct while the process exited 0, so a host read success and ran the
// command anyway: the guard looked installed and enforced nothing.
func TestEnforceVerdictBlocksOnlyDeny(t *testing.T) {
	text := OutputOptions{Format: FormatText}
	err := enforceVerdict(text, guardVerdict{Decision: "deny", Reason: "no"})
	require.Error(t, err, "a deny must exit non-zero or it blocks nothing")
	var silent errSilent
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, guardDenyExitCode, silent.exitCode)

	assert.NoError(t, enforceVerdict(text, guardVerdict{Decision: "advise", Context: "fyi"}),
		"advice teaches and must never block")
	assert.NoError(t, enforceVerdict(text, guardVerdict{Decision: "pass"}))

	// The exit code is the enforcement and does not depend on the rendering: a structured
	// consumer that got a zero status would be told the same lie in a different shape.
	require.Error(t, enforceVerdict(OutputOptions{Format: FormatJSON}, guardVerdict{Decision: "deny", Reason: "no"}))
}

// TestGuardDenyPrintsItsReasonOnce: text mode already renders the full reason to stdout, and
// every guard template this repo ships reads the verdict from stdout - one discards stderr
// outright - so an unconditional stderr copy reached nobody who lacked another channel and
// simply printed a kilobyte-plus reason twice to a terminal. A structured format renders no
// prose, so there stderr is the only readable channel and keeps it.
func TestGuardDenyPrintsItsReasonOnce(t *testing.T) {
	deny := guardVerdict{SchemaVersion: agent.GuardSchemaVersion, Decision: "deny", Reason: "because"}

	var stdout bytes.Buffer
	require.NoError(t, writeGuardVerdict(&stdout, OutputOptions{Format: FormatText}, deny))
	assert.Equal(t, 1, strings.Count(stdout.String(), "because"), "stdout renders the reason once")
	assert.Empty(t, captureStderr(t, func() {
		_ = enforceVerdict(OutputOptions{Format: FormatText}, deny)
	}), "and stderr must not repeat what stdout just said")

	assert.Contains(t, captureStderr(t, func() {
		_ = enforceVerdict(OutputOptions{Format: FormatJSON}, deny)
	}), "because", "a structured format renders no prose, so stderr carries it")
}

// TestDenyNotesWrite covers the only deny on the path surface. The negative cases matter
// more than the positive one: this rule blocks work, so it must be silent in every
// workspace that did not opt in by DECLARING a store.
func TestDenyNotesWrite(t *testing.T) {
	root := t.TempDir()
	// A workspace magus.FindRoot can resolve, so the rule reaches its real decision
	// rather than bailing out on a missing workspace and passing for the wrong reason.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })

	// Nothing declared: the feature is off, so nothing is judged and nothing is guessed.
	globalCfg.Knowledge.Notes.Shared = ""
	for _, path := range []string{"notes/a.md", filepath.Join(root, "notes", "a.md"), "internal/foo.go"} {
		assert.Empty(t, denyNotesWrite(path),
			"with no declared store, %q must pass - a deny fired on a guess blocks work in a workspace that never opted in", path)
	}

	globalCfg.Knowledge.Notes.Shared = "notes"
	// The store must exist to be defended - see TestDenyNotesWriteRequiresTheStoreToExist.
	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes", "nested"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "team", "notes"), 0o755))

	for _, path := range []string{"notes/a.md", "./notes/a.md", filepath.Join(root, "notes", "a.md"), "  notes/nested/b.md  "} {
		reason := denyNotesWrite(path)
		require.NotEmpty(t, reason, "expected a deny for %q", path)
		assert.Contains(t, reason, "NOTES store", "the reason names what was blocked")
		assert.Contains(t, reason, "magus memory put", "and routes the agent somewhere it MAY write")
		assert.Contains(t, reason, "magus notes edit", "and says how a person writes it instead")
	}

	// A path outside the declared store is untouched, including one that merely looks
	// like it (notes-archive shares the prefix but is a different directory).
	for _, path := range []string{"internal/foo.go", "docs/notes.md", "notes-archive/a.md", "../outside/a.md"} {
		assert.Empty(t, denyNotesWrite(path), "%q is not in the declared store", path)
	}

	// The exclusion follows the declaration, not the name.
	globalCfg.Knowledge.Notes.Shared = "team/notes"
	assert.Empty(t, denyNotesWrite("notes/a.md"), "a different directory named notes is not the store")
	assert.NotEmpty(t, denyNotesWrite("team/notes/a.md"))
}

// TestDenyNotesWriteRequiresTheStoreToExist closes the hole a user-global config opens.
// magus reads config from an explicit --config path or $XDG_CONFIG_HOME before the
// workspace, so one global `knowledge.notes.path` would declare a store in every
// workspace. A declaration nobody acted on must defend nothing.
// TestDenyNotesWriteIgnoresAForeignDeclaration: the merged config carries settings from
// outside this repo (user-global, an explicit --config anywhere on disk), so a `notes.shared`
// set once on a machine is "declared" in every workspace on it. Acting on that alone would
// deny writes in repositories that never adopted the policy, so a declaration this repo did
// not make is backed by the on-disk store or it defends nothing.
func TestDenyNotesWriteIgnoresAForeignDeclaration(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })
	globalCfg.Knowledge.Notes.Shared = "notes"

	// No magus.yaml here, so the declaration can only have come from elsewhere on the
	// machine. This repo never opted in.
	assert.Empty(t, denyNotesWrite("notes/a.md"),
		"a declaration this repo did not make must not deny writes in it")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "notes"), 0o755))
	assert.NotEmpty(t, denyNotesWrite("notes/a.md"),
		"a store that exists on disk is defended whoever declared it")
}

// TestDenyNotesWriteDefendsAnEmptyDeclaredStore is the regression guard for the hole that
// dogfooding found on 2026-08-13: with the key declared and no note yet written, a direct
// file write to notes/<name>.md PASSED.
//
// The gate was "the directory exists", on the reasoning that a person creates the store by
// writing the first note, so an agent could never bring it into being. The reverse held.
// An agent could author the store's FIRST note - the single entry with nothing beside it to
// look wrong against - and the deny would switch on immediately afterwards, defending the
// forgery it had just let through. The opt-in is the committed key, not the directory.
func TestDenyNotesWriteDefendsAnEmptyDeclaredStore(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("// scratch\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		[]byte("knowledge:\n  notes:\n    shared: notes\n"), 0o644))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	saved := globalCfg.Knowledge.Notes.Shared
	t.Cleanup(func() { globalCfg.Knowledge.Notes.Shared = saved })
	globalCfg.Knowledge.Notes.Shared = "notes"

	require.NoDirExists(t, filepath.Join(root, "notes"), "the store has no files yet - that is the case under test")
	assert.NotEmpty(t, denyNotesWrite("notes/first.md"),
		"a repo that committed the key is defended before its first note exists, or an agent writes that first note")
	assert.NotEmpty(t, denyNotesWrite(filepath.Join(root, "notes", "nested", "deep.md")))

	// Still scoped: declaring a notes store does not defend the rest of the repo.
	assert.Empty(t, denyNotesWrite("internal/foo.go"))
}

// TestGuardDeniesAuthoringANote closes the surface the path rule cannot see. `magus notes
// edit` can take prose on stdin, which is a COMMAND rather than a file write, so without
// this an agent could author a note through a boundary that is supposed to be about who is
// writing rather than which surface they reached for.
func TestGuardDeniesAuthoringANote(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"magus notes edit team-conventions",
		"printf 'prose' | magus notes edit team-conventions --anchor project:.",
		"cat body.md | ./magus notes edit foo",
	} {
		v := evaluateBashGuard(cmd)
		assert.NotEmpty(t, v.Deny, "expected a deny for %q", cmd)
		assert.Contains(t, v.Deny, "magus memory put", "the reason routes to the store an agent MAY write")
	}
	// Reading is untouched: the boundary is on authorship, not on access.
	for _, cmd := range []string{"magus notes ls", "magus notes get foo", "magus notes verify"} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "%q only reads", cmd)
	}
}

// TestGuardAdvisesCheckpointOnTreeIdentity pins the scoping, which is the whole
// difficulty of this rule: `git rev-parse` answers repository-layout questions as
// well as identity ones, and only the identity forms have a magus superset.
//
// A deny would be wrong twice over - reading a revision is legitimate, and
// checkpoint ADDS to it rather than replacing it.
func TestGuardAdvisesCheckpointOnTreeIdentity(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"git rev-parse HEAD",
		"git rev-parse --short HEAD",
		"git rev-parse --verify HEAD",
		"git rev-parse HEAD~1",
		"git rev-parse @",
		"git describe",
		"git stash create",
		"cd libs/foo && git rev-parse HEAD",
	} {
		v := evaluateBashGuard(cmd)
		assert.Empty(t, v.Deny, "%q reads: advise, never block", cmd)
		assert.Contains(t, v.Context, "magus vcs checkpoint", "%q must name the superset", cmd)
	}

	for _, cmd := range []string{
		"git rev-parse --show-toplevel",
		"git rev-parse --git-dir",
		"git rev-parse --is-inside-work-tree",
		"git rev-parse --show-cdup",
		"git rev-parse --abbrev-ref HEAD",
		// A branch whose NAME starts with those four letters is an ordinary revision.
		"git rev-parse HEADLESS_BRANCH",
		// The build-stamp spellings: a version string to embed, not the identity of a
		// tree being handed to someone. This repository's own go_build target uses both.
		"git describe --tags --always",
		"git describe --always",
	} {
		v := evaluateBashGuard(cmd)
		assert.Empty(t, v.Deny)
		assert.NotContains(t, v.Context, "magus vcs checkpoint",
			"%q is not asking which revision this is", cmd)
	}

	// A destructive stash form is still a deny: adding `create` to the safe list
	// must not have widened the arm.
	for _, cmd := range []string{"git stash", "git stash push -u", "git stash pop"} {
		assert.NotEmpty(t, evaluateBashGuard(cmd).Deny, "%q must still deny", cmd)
	}
}

// TestGuardAdvisesRelockOnDependencyMutations covers the one rule that routes to a
// CHARM rather than a command. Re-resolving dependencies writes state that is not
// reproducible from a clean checkout, which is the whole line between rw and relock
// (types.CharmRelock), and relock is under-discoverable: nothing prompts for a
// reserved charm nobody declared.
//
// ADVISE, never deny: the third deny trigger needs an exact equivalent, and there
// is none - magus has no verb that re-resolves dependencies on its own.
func TestGuardAdvisesRelockOnDependencyMutations(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"go get github.com/foo/bar@latest",
		"pnpm add lodash",
		"pnpm up",
		"npm update",
		"yarn upgrade",
		"cargo update",
		"uv lock",
		"poetry update",
		"pip-compile",
		"cd libs/foo && pnpm add lodash",
	} {
		v := evaluateBashGuard(cmd)
		assert.Empty(t, v.Deny, "%q is legitimate work with no magus equivalent: advise, never block", cmd)
		assert.Contains(t, v.Context, ":relock", "%q must name the charm that makes the write legal", cmd)
	}

	// A DENIED re-resolution still carries the route. `go mod tidy` is both a covered
	// spell op and a dependency refresh, and the deny answers first, so without this
	// the reader is sent to a target that would refuse the write.
	tidy := evaluateBashGuard("go mod tidy")
	require.NotEmpty(t, tidy.Deny)
	assert.Contains(t, tidy.Deny, ":relock")

	// Applying a lockfile is not re-resolving one, and installing a tool is not a
	// dependency at all. Firing here would put an advisory on the most routine
	// command in a JS repo.
	for _, cmd := range []string{"npm ci", "npm install", "pnpm install", "mise install", "go mod vendor", "go mod edit -require=x@v1"} {
		assert.NotContains(t, evaluateBashGuard(cmd).Context, ":relock", "%q does not re-resolve dependencies", cmd)
	}
}

// TestGuardDeniesInPlaceSed: `-i` is the one sed flag that WRITES, and the two
// implementations read each other's spelling as garbage - GNU takes `sed -i 's/x/y/' f` as
// an edit while BSD reads that script as the backup suffix, and `sed -i ”` inverts it. A
// command that worked where it was written mangles the file on the next machine, and it has
// already written by the time anyone looks. Reading with sed is untouched.
func TestGuardDeniesInPlaceSed(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"sed -i 's/a/b/' f.go",
		"sed -i '' 's/a/b/' f.go",
		"sed -i.bak s/a/b/ f",
		"sed --in-place=.bak s/a/b/ f",
		"cat x | sed -i s/a/b/ y",
		"find . -name '*.go' -exec sed -i 's/a/b/' {} +",
	} {
		v := evaluateBashGuard(cmd)
		assert.NotEmpty(t, v.Deny, "expected a deny for %q", cmd)
	}
	for _, cmd := range []string{
		"sed -n '1,5p' f.go",
		"sed 's/a/b/' in.txt",
		"cat f | sed -e 's/a/b/'",
		"echo x | sed s/x/y/",
	} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "%q only reads: sed is not the problem, writing in place is", cmd)
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

// TestGuardDeniesScriptedRewrite: `sed -i` is denied, so the next thing to hand is a
// python one-liner that substitutes and writes - the same edit, by a route the sed rule
// cannot see. This is not hypothetical: a `\.Sum\b` rewrite aimed at one proto field also
// rewrote the OTel SDK's metricdata.Sum and a histogram data point's dp.Sum, because a
// pattern cannot tell one project's symbol from a dependency's symbol of the same name.
//
// The negative cases matter as much: an interpreter that only WRITES is ordinary authoring
// and must stay available, or the guard costs more than the mistake it prevents.
func TestGuardDeniesScriptedRewrite(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`python3 -c "import io,re; s=io.open('f.go').read(); s=re.sub(r'\bA\b','B',s); io.open('f.go','w').write(s)"`,
		`python3 - <<'PY'` + "\n" + `s=re.subn(r'\bP50\b','P50Seconds',s)` + "\n" + `io.open(p,'w').write(s)` + "\nPY",
		`perl -pi -e 's/a/b/' f.go`,
		`perl -i.bak -pe s/a/b/ f`,
		`ruby -i -pe 'gsub(/a/,"b")' f.rb`,
	} {
		assert.NotEmpty(t, evaluateBashGuard(cmd).Deny, "expected a deny for %q", cmd)
	}
	for _, cmd := range []string{
		// Authoring a file is not a rewrite: no substitution, nothing to mis-target.
		`python3 -c "io.open('new.go','w').write(body)"`,
		// Reading and reporting, however it greps, writes nothing.
		`python3 -c "print(re.sub(r'a','b',s))"`,
		`perl -ne 'print if /a/' f.go`,
		`node -e "console.log(x.replace(/a/,'b'))"`,
	} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "%q does not substitute-and-write: %q", cmd, cmd)
	}
}

// TestSearchGuardRoutesAColdIndex pins the half of the routing that decides whether an
// agent trusts the graph at all. `magus refs` answers "unknown, not absent" when a project
// is not indexed, and an agent that reads that as "no matches" falls back to a text match -
// which is exactly the fallback the advisory exists to prevent.
func TestSearchGuardRoutesAColdIndex(t *testing.T) {
	t.Parallel()
	v := evaluateBashGuard(`grep -rn "someFunc" .`)
	assert.Contains(t, v.Context, "magus graph build", "a cold index must name the command that fixes it")
	assert.Contains(t, v.Context, "unknown, not absent", "the verdict's meaning is the point, not just the command")
}
