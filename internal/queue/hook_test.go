package queue

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/queue/types"
	sandboxenv "github.com/egladman/magus/internal/sandbox/env"
	magustypes "github.com/egladman/magus/types"
)

var (
	hookChange = types.Change{ID: "7", Head: strings.Repeat("a", 40)}
	hookUnits  = []string{"app"}
)

func init() { retryDelay = time.Millisecond }

// script is a hook running body with sh, which reads the appended inputs as $1 onward.
func script(body string) Command { return Command{"sh", "-c", body, "hook"} }

// lines is what a hook log holds, one entry per line.
func lines(log *bytes.Buffer) []string { return strings.Split(strings.TrimSpace(log.String()), "\n") }

func TestParseCommandReadsLiteralWords(t *testing.T) {
	for line, want := range map[string]Command{
		"magus run ci --no-default-charms":         {"magus", "run", "ci", "--no-default-charms"},
		"magus --sandbox=required run generate:rw": {"magus", "--sandbox=required", "run", "generate:rw"},
		`'/opt/my magus' run "ci"`:                 {"/opt/my magus", "run", "ci"},
		`./gate.sh a\ b 'x; y' "\$HOME *"`:         {"./gate.sh", "a b", "x; y", "$HOME *"},
		`sh -c 'test -f ok || exit 3'`:             {"sh", "-c", "test -f ok || exit 3"},
		"  make   test  ":                          {"make", "test"},
		`tool a~b 'a*b' --pattern=\*.go`:           {"tool", "a~b", "a*b", "--pattern=*.go"},
	} {
		got, err := ParseCommand("--gate", line)
		require.NoError(t, err, line)
		assert.Equal(t, want, got, line)
	}
}

// No shell runs a hook, so whatever a shell would act on is refused rather than passed
// on as text the line does not read as.
func TestParseCommandRefusesShellSyntax(t *testing.T) {
	for line, why := range map[string]string{
		"":                             "it names no command",
		"  ":                           "it names no command",
		"magus run ci $PROJECTS":       "$PROJECTS is a variable",
		`magus run ci "${HOME}/x"`:     "$HOME is a variable",
		"magus run ci $(cat projects)": "$(...) is a command substitution",
		"magus run ci `cat projects`":  "$(...) is a command substitution",
		"echo $((1+1))":                "$((...)) is arithmetic",
		"magus run ci | tee log":       "| joins two commands",
		"make && magus run ci":         "&& joins two commands",
		"make || true":                 "|| joins two commands",
		"make; magus run ci":           "it holds more than one command",
		"make\nmagus run ci":           "it holds more than one command",
		"magus run ci > log":           "> redirects it",
		"magus run ci 2>&1":            ">& redirects it",
		"magus run ci &":               "& runs it in the background",
		"! magus run ci":               "! negates it",
		"GOFLAGS=-v magus run ci":      "GOFLAGS= sets a variable",
		"rm *.tmp":                     "* is a pattern",
		"ls file?":                     "? is a pattern",
		"ls [ab]":                      "[ is a pattern",
		"echo {a,b}":                   "{ is a pattern",
		"ls ~/x":                       "~ is a home directory",
		"magus run ci # all of it":     "# starts a comment",
		"{ make; }":                    "it is a compound command",
		"if true; then make; fi":       "it is a compound command",
		"magus run 'ci":                "it does not parse as sh words",
	} {
		_, err := ParseCommand("--gate", line)
		var diag *magustypes.DiagnosticError
		require.True(t, errors.As(err, &diag), "%q: %v", line, err)
		assert.Equal(t, magustypes.QueueHookNotACommand, diag.Code, line)
		assert.Contains(t, err.Error(), why, line)
		assert.Contains(t, err.Error(), `--gate `, line)
		assert.Contains(t, err.Error(), "put the line in a script and point --gate at it", line)
	}
}

// A hook gets exactly its words and the appended inputs: nothing splits, expands or
// globs them, and nothing reaches it through the environment.
func TestGateGetsExactlyItsWordsAndUnitsAndNoQueueEnvironment(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "x.go"), nil, 0o644))
	var log bytes.Buffer
	g := CommandGate(Command{"printf", `[%s]\n`, "*", "$HOME"}, HookEnv{}, NewHookLog(&log))
	res, err := g.Validate(context.Background(), types.Candidate{Commit: "s1", Change: "7", Dir: dir}, []string{"libs/a b", "$HOME", "*", "/"})
	require.NoError(t, err)
	assert.True(t, res.Green)
	assert.Equal(t, []string{"[s1 #7] [*]", "[s1 #7] [$HOME]", "[s1 #7] [libs/a b]", "[s1 #7] [$HOME]", "[s1 #7] [*]", "[s1 #7] [/]"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`env`), HookEnv{}, NewHookLog(&log)).Validate(context.Background(), types.Candidate{Commit: "s1", Change: "7", Dir: dir}, hookUnits)
	require.NoError(t, err)
	assert.NotContains(t, log.String(), "MERGEQUEUE_")
}

func TestGateIsGreenOnExitZeroAndRedOtherwise(t *testing.T) {
	dir := t.TempDir()
	g := CommandGate(script(`test -f ok`), HookEnv{}, nil)
	cand := types.Candidate{Commit: "s1", Change: "7", Dir: dir}
	res, err := g.Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.False(t, res.Green)
	assert.Equal(t, "the gate exited 1", res.Summary, "the hook line is the verdict's to record, never the prose's")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "ok"), nil, 0o644))
	res, err = g.Validate(context.Background(), cand, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
}

// seenEnv reads the environment a hook wrote with `env -0 > seen` in dir.
func seenEnv(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	seen := map[string]string{}
	for kv := range strings.SplitSeq(strings.TrimSuffix(string(raw), "\x00"), "\x00") {
		name, value, _ := strings.Cut(kv, "=")
		seen[name] = value
	}
	return seen
}

// A hook runs the changes' code, so of the queue's environment it gets only what
// magus's sandbox gives a sandboxed child and the workspace's passthrough: a
// credential, a GitHub Actions file command and anything else unnamed never reach it.
func TestAHookSeesOnlyWhatTheSandboxAllows(t *testing.T) {
	for _, name := range []string{"MISE_GITHUB_TOKEN", "FOO_SECRET", "RANDOM_VAR", "MERGEQUEUE_TOKEN", "GITHUB_ENV"} {
		t.Setenv(name, "leaked")
	}
	t.Setenv("PASSED", "p")
	t.Setenv("GLOB_X", "g")
	dir, scratch := t.TempDir(), t.TempDir()
	env := HookEnv{Passthrough: []string{"PASSED", "GLOB_*"}, Scratch: []ScratchVar{{Name: "GOCACHE", Dir: "go-build"}}, Fixed: []string{"SET=s"}}
	_, err := CommandGate(script(`env -0 > seen`), env, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir, Scratch: scratch}, hookUnits)
	require.NoError(t, err)
	seen := seenEnv(t, dir)

	for _, name := range []string{"MISE_GITHUB_TOKEN", "FOO_SECRET", "RANDOM_VAR", "MERGEQUEUE_TOKEN", "GITHUB_ENV"} {
		assert.NotContains(t, seen, name, "%s reaches the hook", name)
	}
	assert.Equal(t, os.Getenv("PATH"), seen["PATH"])
	assert.Equal(t, filepath.Join(scratch, "go-build"), seen["GOCACHE"])
	assert.Equal(t, "p", seen["PASSED"])
	assert.Equal(t, "g", seen["GLOB_X"])
	assert.Equal(t, "s", seen["SET"])
	allowed := slices.Concat(sandboxenv.DefaultAllow(), []string{"PASSED", "GLOB_X", "GOCACHE", "SET"},
		[]string{"SHLVL", "_", "OLDPWD"}) // what sh sets itself
	for name := range seen {
		assert.Contains(t, allowed, name, "%s reaches the hook", name)
	}
}

// Every kind of hook takes the same HookEnv: a passthrough name and a fixed assignment
// reach a gate, a regeneration and a facts hook alike, over the queue's own value.
func TestEveryHookTakesItsHookEnv(t *testing.T) {
	t.Setenv("PASSED", "p")
	t.Setenv("FIXED", "queue")
	env := HookEnv{Passthrough: []string{"PASSED", "FIXED"}, Fixed: []string{"FIXED=f"}}
	record := script(`echo "$PASSED $FIXED" > seen; echo '{"units": ["//..."]}'`)
	ctx := context.Background()

	gated := t.TempDir()
	_, err := CommandGate(record, env, nil).Validate(ctx, types.Candidate{Commit: "s", Dir: gated, Scratch: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	regenerated := t.TempDir()
	require.NoError(t, CommandRegenerate(record, env, nil)(ctx, types.Regeneration{Dir: regenerated, Scratch: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	asked := t.TempDir()
	_, err = CommandFacts(record, asked, env, nil).AllUnits(ctx)
	require.NoError(t, err)

	for _, dir := range []string{gated, regenerated, asked} {
		seen, err := os.ReadFile(filepath.Join(dir, "seen"))
		require.NoError(t, err)
		assert.Equal(t, "p f\n", string(seen), dir)
	}
}

func TestAPassthroughThatIsNoGlobIsAnError(t *testing.T) {
	_, err := CommandGate(Command{"true"}, HookEnv{Passthrough: []string{"*"}}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
	require.ErrorContains(t, err, "sandbox.env.passthrough")
}

// A candidate's lines name its change, and a commit gated as it stands says it is the
// base, so the interleaved log of several gates reads on its own.
func TestGateTagsEveryOutputLineWithTheCommitAndWhatItHolds(t *testing.T) {
	var log bytes.Buffer
	g := CommandGate(script(`printf 'one\ntwo\n'; echo three >&2; printf 'no newline'`), HookEnv{}, NewHookLog(&log))
	_, err := g.Validate(context.Background(), types.Candidate{Commit: "0123456789abcdef", Change: "7", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #7] one", "[0123456789ab #7] two", "[0123456789ab #7] three", "[0123456789ab #7] no newline"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`echo one`), HookEnv{}, NewHookLog(&log)).Validate(context.Background(), types.Candidate{Commit: "fedcba9876543210", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.Equal(t, []string{"[fedcba987654 base] one"}, lines(&log))
}

func TestGateRunsATemporaryFailureAgainAndThenCallsItRed(t *testing.T) {
	g := CommandGate(script(`echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`), HookEnv{}, nil)
	res, err := g.Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)

	res, err = CommandGate(script(`exit 75`), HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err, "the change's processes chose the exit status, so it proves nothing about the machine")
	assert.False(t, res.Green)
	assert.Equal(t, "the gate exited 75 (temporary failure) 3 times", res.Summary)
}

// A change's own OOM kill, or a death by any other signal, is its red verdict. Before, it
// was the machine's failure, and a change dying that way stopped its partition every run.
func TestAGateKilledByASignalIsTheChangesRedVerdict(t *testing.T) {
	for body, why := range map[string]string{
		`kill -KILL $$`:                  "was killed (signal: killed)",
		`sh -c 'kill -KILL $$'; exit $?`: "exited 137",
		`exit 143`:                       "exited 143",
	} {
		res, err := CommandGate(script(body), HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
		require.NoError(t, err, body)
		assert.False(t, res.Green)
		assert.Contains(t, res.Summary, why, body)
	}
}

// Nothing a hook started outlives it: before, a background process kept running after
// the gate returned, into the next candidate and past the verdict it led to.
func TestAHooksProcessesDoNotOutliveIt(t *testing.T) {
	dir := t.TempDir()
	res, err := CommandGate(script(`(sleep 0.3; touch late) & echo started`), HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
	time.Sleep(600 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}

func TestARegenerationGetsItsUnitsAsArgumentsAndIsRefusedWhenItFails(t *testing.T) {
	regen := CommandRegenerate(script(`cat > got; printf '[%s]' "$@" > units`), HookEnv{}, nil)
	dir := t.TempDir()
	require.NoError(t, regen(context.Background(), types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"app/gen/a", "app/gen/b"}, Units: []string{"app", "lib"}}))
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "app/gen/a\napp/gen/b\n", string(got))
	units, err := os.ReadFile(filepath.Join(dir, "units"))
	require.NoError(t, err)
	assert.Equal(t, "[app][lib]", string(units))

	err = CommandRegenerate(script(`exit 3`), HookEnv{}, nil)(context.Background(), types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, &types.RefusedError{Reason: "the regeneration exited 3", Paths: []string{"x"}}, refused)

	err = CommandRegenerate(script(`kill -KILL $$`), HookEnv{}, nil)(context.Background(), types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "was killed")
}

func TestScratchVarsPointIntoEachHooksOwnScratchDirectory(t *testing.T) {
	vars := HookEnv{Scratch: []ScratchVar{{Name: "GOCACHE", Dir: "go-build"}, {Name: "XDG_CACHE_HOME", Dir: "cache/xdg"}}}
	dir, scratch := t.TempDir(), t.TempDir()
	res, err := CommandGate(script(`echo "$GOCACHE $XDG_CACHE_HOME" > seen; test -d "$XDG_CACHE_HOME"`), vars, nil).
		Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir, Scratch: scratch}, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green, "the queue creates each directory")
	seen, err := os.ReadFile(filepath.Join(dir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(scratch, "go-build")+" "+filepath.Join(scratch, "cache/xdg")+"\n", string(seen))

	regenDir, regenScratch := t.TempDir(), t.TempDir()
	require.NoError(t, CommandRegenerate(script(`echo "$GOCACHE" > seen`), vars, nil)(context.Background(),
		types.Regeneration{Dir: regenDir, Scratch: regenScratch, Change: hookChange, Paths: []string{"x"}, Units: hookUnits}))
	seen, err = os.ReadFile(filepath.Join(regenDir, "seen"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(regenScratch, "go-build")+"\n", string(seen))
}

func TestParseScratchVarRefusesWhatWouldLeaveTheScratchDirectory(t *testing.T) {
	got, err := ParseScratchVar("MAGUS_CACHE_DIR=magus/./c")
	require.NoError(t, err)
	assert.Equal(t, ScratchVar{Name: "MAGUS_CACHE_DIR", Dir: "magus/c"}, got)
	for spec, want := range map[string]string{
		"GOCACHE":                "is not NAME=DIR",
		"GOCACHE=":               "is not NAME=DIR",
		"1X=d":                   "is not NAME=DIR",
		"A-B=d":                  "is not NAME=DIR",
		"GOCACHE=../out":         "../out leaves the scratch directory",
		"GOCACHE=/tmp/go":        "/tmp/go leaves the scratch directory",
		"GOCACHE=a/../../escape": "a/../../escape leaves the scratch directory",
	} {
		_, err := ParseScratchVar(spec)
		require.ErrorContains(t, err, want, spec)
	}
}

// A hook that cannot start is a machine failure: a checkout gone, or a program nothing
// provides, which no change's code chose.
func TestAHookThatCannotStartIsTheMachinesFailure(t *testing.T) {
	_, err := CommandGate(Command{"true"}, HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: filepath.Join(t.TempDir(), "gone")}, hookUnits)
	require.ErrorContains(t, err, "gate on `s`")
	_, err = CommandGate(Command{"no-such-gate-program"}, HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
	require.ErrorContains(t, err, "gate on `s`")
}

// facts is a build tool's facts command: the queue appends the fact it asks for, as a
// person running `facts affected` would type it.
var facts = script(`case "$1" in
	affected) read p; printf '{"count": 1, "matrix": [], "affected": ["%s"], "unbounded_by": ""}' "${p%%/*}";;
	outputs) echo '{"outputs": ["gen/a", "elsewhere"], "updated": ["docs/b.md", "gen/a"], "maintained": [".gitattributes"]}';;
	generation) cat > asked; echo '{"units": ["app"], "code": ["app/gen.go"], "unbounded": ""}';;
	all) echo '{"units": ["//..."]}';;
	*) exit 9;;
	esac`)

func TestCommandFactsGetTheFactAskedForAsTheirArgument(t *testing.T) {
	// magus affected --plan prints a shard plan with affected and unbounded_by beside it.
	got, unboundedBy, err := CommandFacts(facts, t.TempDir(), HookEnv{}, nil).Affected(context.Background(), hookChange, []string{"app/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, got)
	assert.Empty(t, unboundedBy)
}

func TestCommandFactsWithoutASetAreUnboundedAndAFailureIsAnError(t *testing.T) {
	ctx := context.Background()
	_, unboundedBy, err := CommandFacts(script(`echo '{}'`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, []string{"x"})
	require.NoError(t, err)
	assert.Equal(t, "the affected hook printed no affected set", unboundedBy)

	_, _, err = CommandFacts(script(`exit 3`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, []string{"x"})
	require.EqualError(t, err, "affected hook: exited 3")

	got, unboundedBy, err := CommandFacts(script(`exit 3`), t.TempDir(), HookEnv{}, nil).Affected(ctx, hookChange, nil)
	require.NoError(t, err, "a change touching nothing is not asked about")
	assert.Equal(t, []string{}, got)
	assert.Empty(t, unboundedBy)
}

// A facts command that answers only "outputs" declares no update and maintains nothing.
func TestCommandFactsReadAnOutputsOnlyAnswer(t *testing.T) {
	writes, err := CommandFacts(script(`echo '{"outputs": ["gen/a"]}'`), t.TempDir(), HookEnv{}, nil).Classify(context.Background(), []string{"gen/a", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true}}, writes)
}

// The auto_resolve fact is the build tool's classification of one merge, asked with the
// merge base's content and the merge; a command that cannot answer it settles nothing.
func TestCommandFactsAnswerAutoResolve(t *testing.T) {
	dir := t.TempDir()
	facts := script(`case "$1" in
	auto_resolve) cat > asked; echo '{"auto_resolve": true, "verdict": "CHANGELOG.md: prose (a glob)"}';;
	*) exit 9;;
	esac`)
	verdict, ok, err := CommandFacts(facts, dir, HookEnv{}, nil).AutoResolvable(context.Background(), "CHANGELOG.md", []byte("a\n"), []byte("a\nb\n"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "CHANGELOG.md: prose (a glob)", verdict)
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"path": "CHANGELOG.md", "base": "a\n", "merged": "a\nb\n"}`, string(asked))

	verdict, ok, err = CommandFacts(script(`exit 9`), dir, HookEnv{}, nil).AutoResolvable(context.Background(), "x.go", nil, nil)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Equal(t, "x.go: the facts command answered no auto_resolve (auto_resolve hook: exited 9)", verdict)
}

func TestCommandFactsAnswerWritesGenerationAndEveryUnit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := CommandFacts(facts, dir, HookEnv{}, nil)
	writes, err := f.Classify(ctx, []string{"gen/a", "src/b", "docs/b.md", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true, Updated: true}, "docs/b.md": {Updated: true}, ".gitattributes": {Maintained: true}}, writes,
		"only what was asked about")

	g, err := f.Generation(ctx, []string{"gen/a"}, []string{"app/gen.go", "docs/x.md"})
	require.NoError(t, err)
	assert.Equal(t, types.Generation{Units: []string{"app"}, Code: []string{"app/gen.go"}}, g)
	assert.False(t, regenerationProven(g))
	asked, err := os.ReadFile(filepath.Join(dir, "asked"))
	require.NoError(t, err)
	assert.JSONEq(t, `{"outputs": ["gen/a"], "changed": ["app/gen.go", "docs/x.md"]}`, string(asked))

	all, err := f.AllUnits(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"//..."}, all, "the build tool's own spelling of every unit")

	_, err = CommandFacts(script(`echo '{"units": []}'`), dir, HookEnv{}, nil).AllUnits(ctx)
	require.EqualError(t, err, "all hook named no unit", "no arguments would leave what runs to the tool's default")
}

// The queue appends units after the hook's own words with no "--" between, so a unit
// a hook could read as an option never reaches it.
func TestAUnitAHookCouldReadAsAnOptionIsNeverAppended(t *testing.T) {
	for _, unit := range []string{"-x", "--gate=sh", ""} {
		dir := t.TempDir()
		_, err := CommandGate(script(`touch ran`), HookEnv{}, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, []string{"app", unit})
		require.ErrorContains(t, err, "which a hook would read as an option", "%q", unit)
		assert.NoFileExists(t, filepath.Join(dir, "ran"), "%q", unit)
	}
}

// Paths reach a hook's stdin one per line, and git allows a line break in a path, which
// would read as two paths: such a path is never written.
func TestAPathWithALineBreakNeverReachesAHooksStdin(t *testing.T) {
	ctx := context.Background()
	for _, broken := range []string{"gen/a\napp/main.go", "gen/a\r"} {
		dir := t.TempDir()
		err := CommandRegenerate(script(`cat > got`), HookEnv{}, nil)(ctx, types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"gen/ok", broken}, Units: hookUnits})
		var refused *types.RefusedError
		require.ErrorAs(t, err, &refused, "%q", broken)
		assert.Equal(t, []string{broken}, refused.Paths)
		assert.NoFileExists(t, filepath.Join(dir, "got"), "the regeneration never ran")

		got, unboundedBy, err := CommandFacts(script(`exit 9`), dir, HookEnv{}, nil).Affected(ctx, hookChange, []string{"app/x.go", broken})
		require.NoError(t, err, "the hook is not asked")
		assert.Nil(t, got)
		assert.Contains(t, unboundedBy, "holds a line break", "every unit is gated instead")

		writes, err := CommandFacts(script(`cat > asked; echo '{"outputs": ["gen/ok"]}'`), dir, HookEnv{}, nil).Classify(ctx, []string{"gen/ok", broken})
		require.NoError(t, err)
		assert.Equal(t, map[string]types.Writes{"gen/ok": {Output: true}}, writes, "left unclassified, which is source")
		asked, err := os.ReadFile(filepath.Join(dir, "asked"))
		require.NoError(t, err)
		assert.Equal(t, "gen/ok\n", string(asked))
	}
}
