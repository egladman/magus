package magus

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refuseEverything = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("Not in this workspace.");
});

magus\guard.write(fun (req: WriteRequest) > GuardVerdict {
    return magus\guard.deny("No writes here.");
});
`

// openRefusing opens a workspace whose magus\guard rules deny every command and every
// write, with a build target on the root project that counts its runs.
func openRefusing(t *testing.T) (*Magus, *atomic.Int32) {
	t.Helper()
	testkit.Isolate(t)
	runs := &atomic.Int32{}
	spellName := register.Spell(t, "zzz-judge-test-spell",
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			runs.Add(1)
			return nil, nil
		}),
	)
	w := testkit.NewWorkspace(t)
	w.Magusfile(refuseEverything)
	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(t.Context(), w.Root(), WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m, runs
}

// A deny is the judgment the caller asked for, reason included, never an error.
func TestJudgeCommandReturnsADenyAsAJudgment(t *testing.T) {
	m, _ := openRefusing(t)

	j, err := m.JudgeCommand(t.Context(), "s1", m.Root(), "ls")
	require.NoError(t, err)
	assert.Equal(t, string(types.GuardDeny), j.Decision)
	assert.Contains(t, j.Reason, "Not in this workspace.")
	assert.NotEmpty(t, j.Rule, "every verdict names its rule")
}

func TestJudgeWriteJudgesThePathAsAWrite(t *testing.T) {
	m, _ := openRefusing(t)

	j, err := m.JudgeWrite(t.Context(), "s1", m.Root(), "notes.txt")
	require.NoError(t, err)
	assert.Equal(t, string(types.GuardDeny), j.Decision)
	assert.Contains(t, j.Reason, "No writes here.")
}

// Rules that do not load are a judgment that could not be made, not a pass.
func TestJudgeCommandErrorsWhenTheRulesDoNotLoad(t *testing.T) {
	m, _ := openRefusing(t)
	broken := []byte("import \"magus\";\n\nmagus\\guard.command(\n")
	require.NoError(t, os.WriteFile(filepath.Join(m.Root(), "magusfile.buzz"), broken, 0o644))

	j, err := m.JudgeCommand(t.Context(), "s1", m.Root(), "ls")
	require.Error(t, err)
	assert.Equal(t, types.GuardJudgment{}, j)
}

// An in-process run is judged as the `magus run` line the CLI would be, before anything runs.
func TestRunIsJudgedBeforeAnythingRuns(t *testing.T) {
	m, runs := openRefusing(t)

	err := m.Run(t.Context(), []types.Target{{Path: ".", Name: "build"}}, WithSession("s1"))
	var denied *types.GuardDeniedError
	require.ErrorAs(t, err, &denied)
	assert.Contains(t, denied.Judgment.Reason, "Not in this workspace.")
	assert.Zero(t, runs.Load(), "a denied run runs nothing")
}

// The CLI's host already judged the invocation, so the run under its mark is not judged a
// second time.
func TestRunUnderTheCLIsMarkIsNotJudgedAgain(t *testing.T) {
	m, runs := openRefusing(t)

	require.NoError(t, m.Run(guard.WithHostJudged(t.Context()), []types.Target{{Path: ".", Name: "build"}}))
	assert.Equal(t, int32(1), runs.Load())
}

func TestRunLinesAreTheCLIsCommandLines(t *testing.T) {
	lines, err := runLines([]types.Target{{Path: ".", Name: "build"}, {Path: "libs/a", Name: "test"}}, []string{"-run", "Test A"})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"magus run build . -- -run 'Test A'",
		"magus run test libs/a -- -run 'Test A'",
	}, lines)

	affected, err := affectedLine("ci", "origin/main")
	require.NoError(t, err)
	assert.Equal(t, []string{"magus affected ci --base origin/main"}, affected)

	_, err = runLines([]types.Target{{Path: ".", Name: "build"}}, []string{"a\x00b"})
	assert.Error(t, err, "a NUL byte has no command line to judge")
}

func openRefusingExample() (*exampleTB, *Magus) {
	tb, ws := newExample(map[string]string{"magusfile.buzz": refuseEverything})
	m, err := Open(context.Background(), ws.Root(), WithSpawnRule(TierCeiling))
	if err != nil {
		tb.close()
		panic(err)
	}
	tb.Cleanup(func() { _ = m.Close() })
	return tb, m
}

// ExampleMagus_JudgeCommand shows a deny as an answer: the error is nil and the judgment
// carries the decision, the rule that made it and that rule's reason.
func ExampleMagus_JudgeCommand() {
	tb, m := openRefusingExample()
	defer tb.close()

	j, err := m.JudgeCommand(context.Background(), "example", m.Root(), "ls")
	fmt.Println("error:", err)
	fmt.Println("decision:", j.Decision)
	fmt.Println("names its rule:", j.Rule != "")
	fmt.Println("gives the rule's reason:", strings.Contains(j.Reason, "Not in this workspace."))
	// Output:
	// error: <nil>
	// decision: deny
	// names its rule: true
	// gives the rule's reason: true
}

// ExampleMagus_JudgeWrite judges a path as a write and answers in the same shape.
func ExampleMagus_JudgeWrite() {
	tb, m := openRefusingExample()
	defer tb.close()

	j, err := m.JudgeWrite(context.Background(), "example", m.Root(), "notes.txt")
	fmt.Println("error:", err)
	fmt.Println("decision:", j.Decision)
	fmt.Println("names its rule:", j.Rule != "")
	fmt.Println("gives the rule's reason:", strings.Contains(j.Reason, "No writes here."))
	// Output:
	// error: <nil>
	// decision: deny
	// names its rule: true
	// gives the rule's reason: true
}

// Example_judgeAToolCall asks the guard about two shell commands before either runs.
// A deny is an answer with a nil error.
func Example_judgeAToolCall() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     guardPolicy,
		"api/magusfile.buzz": "",
	})
	defer cleanup()

	ctx := context.Background()
	m, err := Open(ctx, root, WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	for _, command := range []string{"cp api/greeting.txt api/out/", "magus run build api"} {
		j, err := m.JudgeCommand(ctx, "guide-session", root, command)
		if err != nil {
			fmt.Println("judge:", err)
			return
		}
		reason, _, _ := strings.Cut(j.Reason, "\n")
		fmt.Printf("%s: %s %s %s\n", command, j.Decision, j.Rule, reason)
	}
	// Output:
	// cp api/greeting.txt api/out/: deny workspace:targets-only Build through the target: magus run build api
	// magus run build api: pass
}
