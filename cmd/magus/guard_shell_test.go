package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
		// A TREE-ISH before the pathspec is still the whole tree. These read as narrow
		// reverts while the first operand was compared against ".", so the revision was
		// what got tested and the pathspec was never reached.
		{command: "git checkout HEAD -- .", deny: true},
		{command: "git checkout HEAD .", deny: true},
		{command: "git checkout origin/main -- .", deny: true},
		{command: "git restore --source HEAD .", deny: true},
		{command: "git restore --source=HEAD .", deny: true},
		{command: "git checkout main"},
		{command: "git checkout -b feat/x"},
		{command: "git restore .", deny: true},
		// A path-scoped revert advises now: discarding a file because you did not
		// hand-edit it is the most common wrong reflex about generated output.
		{command: "git restore cmd/magus/agent.go", context: "magus-vcs-hygiene"},
		{command: "git checkout -- gen/", context: "role=output"},
		{command: "git checkout HEAD -- docs/gen", context: "role=output"},
		{command: "git clean -fd", deny: true},
		{command: "git clean -fdx", deny: true},
		{command: "git clean --force", deny: true},
		{command: "git clean -n"},
		// READ-ONLY clean invocations. Matching any word containing one of fdxX denied
		// both of these on the letters inside the flag NAME - "dry" and "exclude" - for
		// commands that delete nothing.
		{command: "git clean --dry-run"},
		{command: "git clean --exclude=build"},
		{command: "git clean -n -fd"},
		{command: "git clean -ndx"},
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
		// The deny holds wherever the stage-everything call sits on the line. It used to
		// be graded in the ADVISORY pass, so any earlier git command that advised answered
		// first and the deny was never reached.
		{command: "git restore -- x && git add -A", deny: true},
		{command: "git status && git add .", deny: true},
		{command: "git add -A && git push", deny: true},
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

// TestGitGuardFallbackPrefersTheDeny pins the unparsable-line half, where the file's own
// invariant is that an over-eager deny is the safe direction: there is no AST, and the work
// these rules protect cannot be recovered.
//
// Both cases answered with something weaker. The push ADVISORY was ordered above the
// stage-all deny, and the safe-stash pattern listed the destructive restores as safe, so
// each line got a reminder or nothing where the parsed path denies.
func TestGitGuardFallbackPrefersTheDeny(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		"git add -A && git push && (",
		"git stash pop && (",
		"git stash apply && (",
		"git stash drop && (",
		"git stash branch wip && (",
	} {
		_, parsed := parseGuardCommands(cmd)
		require.False(t, parsed, "%q must be unparsable or it does not exercise the fallback", cmd)
		v := evaluateBashGuard(cmd)
		assert.NotEmpty(t, v.Deny, "%q must deny on the fallback path", cmd)
	}

	// Reading a stash is still safe, whether or not the line parses.
	for _, cmd := range []string{"git stash list && (", "git stash show && ("} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "%q only reads", cmd)
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

// TestGuardDeniesReadAck is the integrity property the whole read-receipt feature rests on.
//
// A receipt claims a PERSON read something. An agent able to mint one turns the measure into
// a formality it satisfies on the way past - and it would, because stamping the changeset is
// the obvious tidy-up at the end of a task. The guard is the right place because of what it
// sees: it is wired into agent hosts, so everything reaching it came from an agent, and a
// person at a terminal never meets this rule.
func TestGuardDeniesReadAck(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`magus diff ` + "--ack",
		`./magus diff --impact ` + "--ack",
		`cd /tmp && magus diff ` + "--ack" + ` --reason x`,
		// A GLOBAL FLAG before the verb is the same invocation, and the anchored pattern
		// walked past every one of them: magus accepts its display and workspace flags
		// ahead of the subcommand, so this spelling minted a receipt unguarded.
		`magus -o json diff ` + "--ack",
		`magus --root . diff ` + "--ack",
		// A line the parser cannot read still falls back to the pattern.
		`magus diff ` + "--ack" + ` && (`,
	} {
		v := evaluateBashGuard(cmd)
		assert.NotEmpty(t, v.Deny, "expected a deny for %q", cmd)
		assert.Contains(t, v.Deny, "only a person can record one")
	}
}

// Reading the report is exactly what an agent SHOULD do, so the deny must not reach it. A
// rule that swallowed the read path would push agents off the surface entirely, which is the
// opposite of the point: an agent that cannot mint a receipt should still be able to say
// which files carry none.
func TestGuardAllowsReadingTheReport(t *testing.T) {
	t.Parallel()
	for _, cmd := range []string{
		`magus diff --impact`,
		`magus diff -o json`,
	} {
		assert.Empty(t, evaluateBashGuard(cmd).Deny, "unexpected deny for %q", cmd)
	}
}

// The pattern this caught was mine, run perhaps twenty times in one session: format, then
// lint, then generate, as separate invocations. `lint` needs `format` needs `generate`, so
// the last one alone does all three - every earlier call was a workspace reload to redo work
// the next call redid anyway.
//
// An advisory rather than a deny: two independent targets on one line is real work, and only
// the dependency graph knows which case a given chain is.
func TestChainedRunIsAdvisedNotDenied(t *testing.T) {
	chained := []string{
		"./magus run format . --silent; ./magus run lint . --silent",
		"magus run generate . && magus run lint .",
		"./magus run generate . --silent; ./magus run format . --silent; ./magus run lint . --silent",
		// The gate counts too, and this exact line is how the rule's own author tripped MGS4007
		// an hour after writing it: console:build left an output behind that the gate then read.
		"./magus run build console --silent; ./magus affected ci --no-default-charms",
	}
	for _, cmd := range chained {
		v := evaluateBashGuard(cmd)
		assert.Empty(t, v.Deny, "a chain is questionable, not forbidden: %s", cmd)
		assert.Contains(t, v.Context, "compose through ctx.needs", cmd)
	}

	// One invocation is the shape being taught, and must stay silent.
	for _, cmd := range []string{
		"./magus run lint . --silent",
		"magus run build api web/studio",
		"echo 'magus run format . ; magus run lint .'",
	} {
		assert.Empty(t, evaluateBashGuard(cmd).Context, "should not fire: %s", cmd)
	}
}
