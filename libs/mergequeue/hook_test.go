package mergequeue

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/mergequeue/types"
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
		"magus run ci --no-default-charms":        {"magus", "run", "ci", "--no-default-charms"},
		"magus --sandbox-enabled run generate:rw": {"magus", "--sandbox-enabled", "run", "generate:rw"},
		`'/opt/my magus' run "ci"`:                {"/opt/my magus", "run", "ci"},
		`./gate.sh a\ b 'x; y' "\$HOME *"`:        {"./gate.sh", "a b", "x; y", "$HOME *"},
		`sh -c 'test -f ok || exit 3'`:            {"sh", "-c", "test -f ok || exit 3"},
		"  make   test  ":                         {"make", "test"},
		`tool a~b 'a*b' --pattern=\*.go`:          {"tool", "a~b", "a*b", "--pattern=*.go"},
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
	g := CommandGate(Command{"printf", `[%s]\n`, "*", "$HOME"}, nil, NewHookLog(&log))
	res, err := g.Validate(context.Background(), types.Candidate{Commit: "s1", Change: "7", Dir: dir}, []string{"libs/a b", "$HOME", "*", "/"})
	require.NoError(t, err)
	assert.True(t, res.Green)
	assert.Equal(t, []string{"[s1 #7] [*]", "[s1 #7] [$HOME]", "[s1 #7] [libs/a b]", "[s1 #7] [$HOME]", "[s1 #7] [*]", "[s1 #7] [/]"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`env`), nil, NewHookLog(&log)).Validate(context.Background(), types.Candidate{Commit: "s1", Change: "7", Dir: dir}, hookUnits)
	require.NoError(t, err)
	assert.NotContains(t, log.String(), "MERGEQUEUE_")
}

func TestGateIsGreenOnExitZeroAndRedOtherwise(t *testing.T) {
	dir := t.TempDir()
	g := CommandGate(script(`test -f ok`), nil, nil)
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

// A gate runs the changes' code; before, it inherited the write token with everything
// else in the queue's environment.
func TestHooksNeverSeeTheQueuesCredentials(t *testing.T) {
	t.Setenv("MERGEQUEUE_TOKEN", "write")
	t.Setenv("GITHUB_TOKEN", "read")
	t.Setenv("KEPT", "yes")
	var log bytes.Buffer
	g := CommandGate(script(`echo "[$MERGEQUEUE_TOKEN][$GITHUB_TOKEN][$KEPT]"`), nil, NewHookLog(&log))
	_, err := g.Validate(context.Background(), types.Candidate{Commit: "s", Change: "7", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.Equal(t, []string{"[s #7] [][][yes]"}, lines(&log))
}

// A candidate's lines name its change, and a commit gated as it stands says it is the
// base, so the interleaved log of several gates reads on its own.
func TestGateTagsEveryOutputLineWithTheCommitAndWhatItHolds(t *testing.T) {
	var log bytes.Buffer
	g := CommandGate(script(`printf 'one\ntwo\n'; echo three >&2; printf 'no newline'`), nil, NewHookLog(&log))
	_, err := g.Validate(context.Background(), types.Candidate{Commit: "0123456789abcdef", Change: "7", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"[0123456789ab #7] one", "[0123456789ab #7] two", "[0123456789ab #7] three", "[0123456789ab #7] no newline"}, lines(&log))

	log.Reset()
	_, err = CommandGate(script(`echo one`), nil, NewHookLog(&log)).Validate(context.Background(), types.Candidate{Commit: "fedcba9876543210", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.Equal(t, []string{"[fedcba987654 base] one"}, lines(&log))
}

func TestGateRunsATemporaryFailureAgainAndThenCallsItRed(t *testing.T) {
	g := CommandGate(script(`echo x >> tries; test "$(wc -l < tries)" -ge 3 || exit 75`), nil, nil)
	res, err := g.Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)

	res, err = CommandGate(script(`exit 75`), nil, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
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
		res, err := CommandGate(script(body), nil, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
		require.NoError(t, err, body)
		assert.False(t, res.Green)
		assert.Contains(t, res.Summary, why, body)
	}
}

// Nothing a hook started outlives it: before, a background process kept running after
// the gate returned, into the next candidate and past the verdict it led to.
func TestAHooksProcessesDoNotOutliveIt(t *testing.T) {
	dir := t.TempDir()
	res, err := CommandGate(script(`(sleep 0.3; touch late) & echo started`), nil, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: dir}, hookUnits)
	require.NoError(t, err)
	assert.True(t, res.Green)
	time.Sleep(600 * time.Millisecond)
	assert.NoFileExists(t, filepath.Join(dir, "late"))
}

func TestARegenerationGetsItsUnitsAsArgumentsAndIsRefusedWhenItFails(t *testing.T) {
	regen := CommandRegenerate(script(`cat > got; printf '[%s]' "$@" > units`), nil, nil)
	dir := t.TempDir()
	require.NoError(t, regen(context.Background(), types.Regeneration{Dir: dir, Change: hookChange, Paths: []string{"app/gen/a", "app/gen/b"}, Units: []string{"app", "lib"}}))
	got, err := os.ReadFile(filepath.Join(dir, "got"))
	require.NoError(t, err)
	assert.Equal(t, "app/gen/a\napp/gen/b\n", string(got))
	units, err := os.ReadFile(filepath.Join(dir, "units"))
	require.NoError(t, err)
	assert.Equal(t, "[app][lib]", string(units))

	err = CommandRegenerate(script(`exit 3`), nil, nil)(context.Background(), types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, &types.RefusedError{Reason: "the regeneration exited 3", Paths: []string{"x"}}, refused)

	err = CommandRegenerate(script(`kill -KILL $$`), nil, nil)(context.Background(), types.Regeneration{Dir: t.TempDir(), Change: hookChange, Paths: []string{"x"}, Units: hookUnits})
	require.ErrorAs(t, err, &refused)
	assert.Contains(t, refused.Reason, "was killed")
}

func TestScratchVarsPointIntoEachHooksOwnScratchDirectory(t *testing.T) {
	vars := []ScratchVar{{Name: "GOCACHE", Dir: "go-build"}, {Name: "XDG_CACHE_HOME", Dir: "cache/xdg"}}
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
		"GITHUB_TOKEN=d":         "the queue removes GITHUB_TOKEN from every hook",
		"MERGEQUEUE_TOKEN=d":     "the queue removes MERGEQUEUE_TOKEN from every hook",
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
	_, err := CommandGate(Command{"true"}, nil, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: filepath.Join(t.TempDir(), "gone")}, hookUnits)
	require.ErrorContains(t, err, "gate on `s`")
	_, err = CommandGate(Command{"no-such-gate-program"}, nil, nil).Validate(context.Background(), types.Candidate{Commit: "s", Dir: t.TempDir()}, hookUnits)
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
	got, unboundedBy, err := CommandFacts(facts, t.TempDir(), nil).Affected(context.Background(), hookChange, []string{"app/main.go"})
	require.NoError(t, err)
	assert.Equal(t, []string{"app"}, got)
	assert.Empty(t, unboundedBy)
}

func TestCommandFactsWithoutASetAreUnboundedAndAFailureIsAnError(t *testing.T) {
	ctx := context.Background()
	_, unboundedBy, err := CommandFacts(script(`echo '{}'`), t.TempDir(), nil).Affected(ctx, hookChange, []string{"x"})
	require.NoError(t, err)
	assert.Equal(t, "the affected hook printed no affected set", unboundedBy)

	_, _, err = CommandFacts(script(`exit 3`), t.TempDir(), nil).Affected(ctx, hookChange, []string{"x"})
	require.EqualError(t, err, "affected hook: exited 3")

	got, unboundedBy, err := CommandFacts(script(`exit 3`), t.TempDir(), nil).Affected(ctx, hookChange, nil)
	require.NoError(t, err, "a change touching nothing is not asked about")
	assert.Equal(t, []string{}, got)
	assert.Empty(t, unboundedBy)
}

// A facts command that answers only "outputs" declares no update and maintains nothing.
func TestCommandFactsReadAnOutputsOnlyAnswer(t *testing.T) {
	writes, err := CommandFacts(script(`echo '{"outputs": ["gen/a"]}'`), t.TempDir(), nil).Classify(context.Background(), []string{"gen/a", ".gitattributes"})
	require.NoError(t, err)
	assert.Equal(t, map[string]types.Writes{"gen/a": {Output: true}}, writes)
}

func TestCommandFactsAnswerWritesGenerationAndEveryUnit(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	f := CommandFacts(facts, dir, nil)
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

	_, err = CommandFacts(script(`echo '{"units": []}'`), dir, nil).AllUnits(ctx)
	require.EqualError(t, err, "all hook named no unit", "no arguments would leave what runs to the tool's default")
}
