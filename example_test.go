package magus_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/libs/testkit/register"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// exampleTB lets an example build a testkit workspace: an Example has no *testing.T, and
// testkit takes a testkit.TB. It panics on the methods no example calls, so a new use
// fails loudly. TestMain already isolates the environment, so nothing calls Setenv.
type exampleTB struct {
	cleanups []func()
}

func (*exampleTB) Skip(...any)        { panic("exampleTB: Skip") }
func (*exampleTB) Setenv(_, _ string) { panic("exampleTB: Setenv") }
func (*exampleTB) Helper()            {}

func (e *exampleTB) Cleanup(f func()) { e.cleanups = append(e.cleanups, f) }

func (e *exampleTB) TempDir() string {
	dir, err := os.MkdirTemp("", "magus-example-*")
	if err != nil {
		panic(err)
	}
	e.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (*exampleTB) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

// close runs the cleanups last in, first out, as testing.T does.
func (e *exampleTB) close() {
	for i := len(e.cleanups) - 1; i >= 0; i-- {
		e.cleanups[i]()
	}
}

// newExample returns a workspace holding files, keyed by workspace-relative path, and the
// exampleTB that owns it. The caller defers close.
func newExample(files map[string]string) (*exampleTB, *testkit.Workspace) {
	tb := &exampleTB{}
	ws := testkit.NewWorkspace(tb)
	ws.WriteTree(files)
	return tb, ws
}

// monorepo is a workspace laid out the way a monorepo is.
func monorepo() (*exampleTB, *testkit.Workspace) {
	return newExample(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})
}

// ExampleInspect shows how to discover projects in a workspace without
// opening the cache. Inspect is the right entry point for read-only
// commands (list, graph, describe) where cache overhead is unnecessary.
func ExampleInspect() {
	// A directory is a project if it contains a magusfile.buzz.
	tb, w := newExample(map[string]string{"myapp/magusfile.buzz": ""})
	defer tb.close()

	ws, err := magus.Inspect(context.Background(), w.Root(), magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		fmt.Println("inspect error:", err)
		return
	}

	for _, p := range ws.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// myapp
}

// ExampleOpen opens a workspace, which also opens its cache, and lists its projects. A
// program that only reads the workspace calls Inspect instead.
func ExampleOpen() {
	tb, ws := monorepo()
	defer tb.close()

	m, err := magus.Open(context.Background(), ws.Root(), magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	for _, p := range m.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// .
	// api
	// web
}

// ExampleMagus_ExpandAffected expands a target over the projects a changeset touches. A
// workspace that is not under version control cannot tell which projects changed, so it
// falls back to every project and says so in the third result.
func ExampleMagus_ExpandAffected() {
	tb, ws := monorepo()
	defer tb.close()

	m, err := magus.Open(context.Background(), ws.Root(), magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	targets, _, fellBack, err := m.ExpandAffected(context.Background(), "ci", "")
	if err != nil {
		fmt.Println("expand:", err)
		return
	}
	fmt.Println("fell back to every project:", fellBack)
	for _, t := range targets {
		fmt.Println(t.Path + ":" + t.Name)
	}
	// Output:
	// fell back to every project: true
	// .:ci
	// api:ci
	// web:ci
}

// ExampleWorkspaceRegistry_withSpell attaches a spell to a project in Go instead of in a
// magusfile, then reads back the targets the spell contributes. The example registers the
// spell itself so its output does not depend on which built-in spells the program links.
func ExampleWorkspaceRegistry_withSpell() {
	tb, ws := monorepo()
	defer tb.close()

	const name = "example-lint"
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name, spells.WithTargets("lint")))
	defer project.DefaultSpellRegistry().UnregisterSpell(name)

	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject("api", magus.WithSpell(name))

	m, err := magus.Inspect(context.Background(), ws.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		fmt.Println("inspect:", err)
		return
	}
	targets, err := m.ListTargets(context.Background())
	if err != nil {
		fmt.Println("list targets:", err)
		return
	}
	for _, t := range targets {
		if t.Kind == "spell" {
			fmt.Println(t.Name, t.Spells)
		}
	}
	// Output:
	// lint [example-lint]
}

type countingObserver struct {
	builds atomic.Int32
}

func (o *countingObserver) Observe(e types.GraphEvent) {
	if e.Op == types.GraphOpBuild {
		o.builds.Add(1)
	}
}

// ExampleMagus_Run runs a target that a spell registered in Go contributes; a magusfile
// target does not run outside the magus module. WithSession names the session the guard
// judges the run under. WithObserver sees the graph builds this run makes and no other's.
func ExampleMagus_Run() {
	tb, ws := newExample(map[string]string{"magusfile.buzz": ""})
	defer tb.close()

	spell := register.Spell(tb, "example-build",
		spells.WithTargets("build"),
		spells.WithInvoker(func(context.Context, spells.InvokeRequest) (any, error) {
			fmt.Println("build ran")
			return nil, nil
		}),
	)
	reg := magus.NewWorkspaceRegistry()
	reg.RegisterProject(".", magus.WithSpell(spell))

	m, err := magus.Open(context.Background(), ws.Root(), magus.WithWorkspaceRegistry(reg), magus.WithSpawnRule(magus.TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	obs := &countingObserver{}
	err = m.Run(context.Background(), []types.Target{{Path: ".", Name: "build"}},
		magus.WithSession("example"), magus.WithObserver(obs))
	var denied *types.GuardDeniedError
	switch {
	case errors.As(err, &denied):
		fmt.Println("denied:", denied.Judgment.Reason)
	case err != nil:
		fmt.Println("run:", err)
	}
	fmt.Println("graph built under the run:", obs.builds.Load() > 0)
	// Output:
	// build ran
	// graph built under the run: false
}

// refuseEverything is a magusfile whose guard rules deny every command and every write.
const refuseEverything = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("Not in this workspace.");
});

magus\guard.write(fun (req: WriteRequest) > GuardVerdict {
    return magus\guard.deny("No writes here.");
});
`

func openRefusing() (*exampleTB, *magus.Magus) {
	tb, ws := newExample(map[string]string{"magusfile.buzz": refuseEverything})
	m, err := magus.Open(context.Background(), ws.Root(), magus.WithSpawnRule(magus.TierCeiling))
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
	tb, m := openRefusing()
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
	tb, m := openRefusing()
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
