---
title: Using the Go SDK
description: Task-shaped recipes for calling magus from Go (open a workspace, list projects and targets, run a target and read its result, ask what a change affects, query the graph, judge a tool call with the guard) and one program that is its own harness.
tags: [go, sdk, library, guide, guard, harness, cache, embedding]
---

# Using the Go SDK

This guide does one thing per section with `import "github.com/egladman/magus"`. Each
section is a runnable program. The [Go SDK reference](../reference/go-sdk.md) explains
the shape of the API behind them: `Open` and `Inspect`, the interface roles, the
`List`/`Evaluate` axis, cancellation, and testing. Read it when a recipe here leaves you
asking why.

Every code block on this page is a Go example in
package magus's tests ([`magus_test.go`](https://github.com/egladman/magus/blob/main/magus_test.go), [`plan_test.go`](https://github.com/egladman/magus/blob/main/plan_test.go), [`diff_test.go`](https://github.com/egladman/magus/blob/main/diff_test.go), [`judge_test.go`](https://github.com/egladman/magus/blob/main/judge_test.go)),
copied here verbatim. The test suite compiles and runs each one and compares what it
prints against its `// Output:` comment, so a block that stops working fails the build
before the page goes stale.

The examples live in package `magus` itself, so they call `Open` where your program
calls `magus.Open`, and they decode records with magus's `internal/json`, which your
program replaces with `encoding/json` or its own JSON package.

## Setup

Check that `go get` resolves the version you pick before you depend on it; the
reference's [Before you `go get` it](../reference/go-sdk.md#before-you-go-get-it) says
how. The examples import:

```go
import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "github.com/egladman/magus/buzzengine"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/output"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)
```

The blank import of `buzzengine` links the engine that evaluates `magusfile.buzz`. A
program that opens a workspace holding a magusfile needs it, or `Open` and `Inspect`
fail with [MGS1104](../reference/codes/capability/MGS1104.md).

Each example builds a throwaway workspace with this helper. In your own program, pass
the root of a real workspace instead, or find it with `magus.FindRoot(dir)`.

```go
// newGuideWorkspace writes files, keyed by slash-separated workspace-relative path, into a
// new temporary directory and returns it with the function that removes it.
func newGuideWorkspace(files map[string]string) (root string, cleanup func()) {
	root, err := os.MkdirTemp("", "magus-guide-*")
	if err != nil {
		panic(err)
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			panic(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			panic(err)
		}
	}
	return root, func() { _ = os.RemoveAll(root) }
}
```

Several examples run a target. A target defined in a magusfile runs only inside the
magus module, so these attach a spell written in Go instead. This one has a `build`
target that copies a project's `.txt` files into `out/`, and it declares both: the
`.txt` files are its inputs and `out/` its output. Those declarations are what let magus
cache the target and know which projects a change reaches.

```go
// registerCopySpell registers a spell named name with one target, build, that copies
// every .txt file of a project into its out directory. The spell declares the .txt
// files as its inputs and out as its output, which is what lets magus cache the target.
// runs counts the builds that actually ran. The returned function unregisters it.
func registerCopySpell(name string, runs *int) (unregister func()) {
	build := func(_ context.Context, req spells.InvokeRequest) (any, error) {
		*runs++
		var copied []string
		inputs, err := filepath.Glob(filepath.Join(req.Dir, "*.txt"))
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(req.Dir, "out"), 0o755); err != nil {
			return nil, err
		}
		for _, in := range inputs {
			body, err := os.ReadFile(in)
			if err != nil {
				return nil, err
			}
			if err := os.WriteFile(filepath.Join(req.Dir, "out", filepath.Base(in)), body, 0o644); err != nil {
				return nil, err
			}
			copied = append(copied, filepath.Base(in))
		}
		return copied, nil
	}
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name,
		spells.WithTargets("build"),
		spells.WithSources("*.txt"),
		spells.WithOutputs("out/**"),
		spells.WithInvoker(build),
	))
	return func() { project.DefaultSpellRegistry().UnregisterSpell(name) }
}
```

## Open a workspace

`Inspect` reads a workspace without opening its cache. Use it for anything that only
looks. Every constructor takes a spawn rule, which has no default; `magus.TierCeiling`
is the one the CLI passes.

```go
// Example_openAWorkspace opens a workspace read-only and lists its projects. A
// directory holding a magusfile.buzz is a project.
func Example_openAWorkspace() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})
	defer cleanup()

	ctx := context.Background()
	ws, err := Inspect(ctx, root, WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("inspect:", err)
		return
	}
	for _, p := range ws.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// .
	// api
	// web
}
```

## List projects and targets

`ws.All()` lists the projects, as above. `ListTargets` lists the targets declared
across the workspace, with the spells that contribute each one. Here a
`WorkspaceRegistry` attaches the copy spell to `api` in Go, in place of an import in
that project's magusfile.

```go
// Example_listProjectsAndTargets lists every target the workspace declares, here the
// build target a spell attached in Go contributes, beside the canonical ci target.
func Example_listProjectsAndTargets() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
	})
	defer cleanup()
	var runs int
	defer registerCopySpell("guide-list", &runs)()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell("guide-list"))

	ctx := context.Background()
	ws, err := Inspect(ctx, root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("inspect:", err)
		return
	}
	targets, err := ws.ListTargets(ctx)
	if err != nil {
		fmt.Println("list targets:", err)
		return
	}
	for _, t := range targets {
		fmt.Println(t.Name, t.Kind, t.Spells)
	}
	// Output:
	// ci canonical []
	// build spell [guide-list]
}
```

## Run a target and read its result

Running needs the cache, so open the workspace with `Open`, which returns a
`*magus.Magus`, and close it when you are done. `Run` reports progress through a sink.
A sink built for `output.JSONL` writes one JSON record per event, and each
`run.target.result` record carries the target's status, whether the cache answered it,
and the ref of its stored output. This helper keeps those records:

```go
// targetResult is the part of a run.target.result record these examples read.
type targetResult struct {
	Type     string `json:"type"`
	Project  string `json:"project"`
	Target   string `json:"target"`
	Status   string `json:"status"`
	CacheHit bool   `json:"cache_hit"`
	Ref      string `json:"ref"`
}
```

```go
// runRecorded runs targets on m and returns the per-target results the run recorded.
func runRecorded(ctx context.Context, m *Magus, targets []types.Target) ([]targetResult, error) {
	var records bytes.Buffer
	sink, err := NewSink(output.JSONL, &records, io.Discard)
	if err != nil {
		return nil, err
	}
	runErr := m.Run(ctx, targets, WithSink(sink))
	if err := sink.Close(); err != nil {
		return nil, err
	}
	var results []targetResult
	lines := bufio.NewScanner(&records)
	for lines.Scan() {
		var r targetResult
		if err := json.Unmarshal(lines.Bytes(), &r); err != nil {
			return nil, err
		}
		if r.Type == "run.target.result" {
			results = append(results, r)
		}
	}
	return results, errors.Join(runErr, lines.Err())
}
```

`OutputByRef` reads what a ref names: the stored output and a descriptor of the run that
produced it. It is the call behind `magus query output <ref>`.

```go
// Example_runATarget runs a target, reads its result from the run's records, and
// reads the stored output its ref names.
func Example_runATarget() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"api/greeting.txt":   "hello\n",
	})
	defer cleanup()
	var runs int
	defer registerCopySpell("guide-run", &runs)()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell("guide-run"))

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	results, err := runRecorded(ctx, m, []types.Target{{Path: "api", Name: "build"}})
	if err != nil {
		fmt.Println("run:", err)
		return
	}
	for _, r := range results {
		fmt.Printf("%s:%s %s, cache hit %v\n", r.Project, r.Target, r.Status, r.CacheHit)

		_, d, err := m.OutputByRef(ctx, r.Ref)
		if err != nil {
			fmt.Println("output:", err)
			return
		}
		fmt.Printf("ref names %s:%s, failed %v\n", d.Project, d.Target, d.Failed)
	}
	built, err := os.ReadFile(filepath.Join(root, "api", "out", "greeting.txt"))
	if err != nil {
		fmt.Println("read output:", err)
		return
	}
	fmt.Printf("api/out/greeting.txt: %s", built)
	// Output:
	// api:build ok, cache hit false
	// ref names api:build, failed false
	// api/out/greeting.txt: hello
}
```

## Ask what a change affects

`AffectedFromPaths` takes changed paths and answers which projects hold them (`Seed`)
and which projects depend on those (`Affected`). `Affected(ctx, base)` asks the same of
a version-control diff against `base`. Here `web` declares a dependency on `api`.

```go
// Example_whatADiffAffects asks which projects a change to one file reaches. web
// depends on api, so a change inside api affects both.
func Example_whatADiffAffects() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"api/greeting.txt":   "hello\n",
		"web/magusfile.buzz": "",
	})
	defer cleanup()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("web", WithDependsOn("api"))

	ctx := context.Background()
	ws, err := Inspect(ctx, root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("inspect:", err)
		return
	}
	res, err := ws.AffectedFromPaths(ctx, []string{"api/greeting.txt"})
	if err != nil {
		fmt.Println("affected:", err)
		return
	}
	fmt.Println("changed in:", res.Seed)
	fmt.Println("affected:", res.Affected)
	// Output:
	// changed in: [api]
	// affected: [api web]
}
```

## Query the graph

`std.MagusQuery` is the Go side of `magus query`: ranked matches over the workspace's
knowledge graph, with a verdict that keeps "verified absent" apart from "not indexed".
It reads the workspace from its context: `m.Context(ctx)` returns one carrying the
workspace `Open` returned.

```go
// ExampleMagus_Context asks the workspace's knowledge graph for every project, on the
// context m.Context returns. The verdict says whether zero matches would mean absent or
// unknown.
func ExampleMagus_Context() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     "",
		"api/magusfile.buzz": "",
		"web/magusfile.buzz": "",
	})
	defer cleanup()

	ctx := context.Background()
	m, err := Open(ctx, root, WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	out, err := std.MagusQuery(m.Context(ctx), "kind=project", types.KnowledgeQueryOptions{})
	if err != nil {
		fmt.Println("query:", err)
		return
	}
	ids := make([]string, 0, len(out.Matches))
	for _, match := range out.Matches {
		ids = append(ids, match.ID)
	}
	slices.Sort(ids)
	fmt.Println("verdict:", out.Answer.Verdict)
	fmt.Println(strings.Join(ids, "\n"))
	// Output:
	// verdict: found
	// project:.
	// project:api
	// project:web
}
```

`types.WithWorkspace(ctx, m)` is not the same: the graph doors fail on it with "this
workspace has no knowledge graph".

## Judge a tool call before it runs

`JudgeCommand` asks the agent guard what it would decide about a shell command, with no
agent host in the loop. It applies the built-in rules and the workspace's own rule. This
root magusfile registers a rule that denies every program but `magus` and names the
command to run instead:

```go
// guardPolicy is a root magusfile whose command rule denies every program but magus,
// naming the target to run instead.
const guardPolicy = `import "magus";

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    foreach (c in req.commands) {
        if (c.program != "magus") {
            return magus\guard.deny("Build through the target: magus run build api", {"rule": "targets-only"});
        }
    }
    return magus\guard.allow();
});
`
```

A deny is an answer, not an error: `err` is nil, `Decision` is `deny`, `Rule` names the
rule and `Reason` is written for the agent. Its first line is the remedy; later lines
point at the full verdict. A pass has `Decision` set to `pass`. `JudgeWrite` judges a
file write the same way.

```go
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
```

## A program that is its own harness

The pieces above make a harness: deterministic scaffolding around whatever proposes the
work. Here the proposals are a fixed list standing in for a model. The guard judges each
one before anything happens; the denied copy never runs, and its reason is what the
agent would read next. The allowed command runs as a target with declared inputs and
outputs. The second run of the same inputs is answered from the cache: the record says
`cache hit true`, and the build counter shows the copy did not run again.

```go
// Example_aProgramThatIsItsOwnHarness is the whole loop in one program: an agent
// proposes shell commands, the guard judges each before it runs, the allowed one runs as
// a target with declared inputs and outputs, and a second run of the same inputs is
// answered from the cache without running the build again.
func Example_aProgramThatIsItsOwnHarness() {
	root, cleanup := newGuideWorkspace(map[string]string{
		"magusfile.buzz":     guardPolicy,
		"api/magusfile.buzz": "",
		"api/greeting.txt":   "hello\n",
	})
	defer cleanup()
	var runs int
	defer registerCopySpell("guide-harness", &runs)()

	reg := NewWorkspaceRegistry()
	reg.RegisterProject("api", WithSpell("guide-harness"))

	ctx := context.Background()
	m, err := Open(ctx, root, WithWorkspaceRegistry(reg), WithSpawnRule(TierCeiling))
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer m.Close()

	// What a model asked to run, in order. A real harness reads these from its agent loop.
	proposed := []string{"cp api/greeting.txt api/out/", "magus run build api", "magus run build api"}
	for _, command := range proposed {
		j, err := m.JudgeCommand(ctx, "guide-session", root, command)
		if err != nil {
			fmt.Println("judge:", err)
			return
		}
		if j.Decision == "deny" {
			reason, _, _ := strings.Cut(j.Reason, "\n")
			fmt.Printf("denied %q: %s\n", command, reason)
			continue
		}
		target, ok := strings.CutPrefix(command, "magus run ")
		name, path, _ := strings.Cut(target, " ")
		if !ok {
			fmt.Printf("allowed %q, which this harness does not run\n", command)
			continue
		}
		results, err := runRecorded(ctx, m, []types.Target{{Path: path, Name: name}})
		if err != nil {
			fmt.Println("run:", err)
			return
		}
		for _, r := range results {
			fmt.Printf("ran %s:%s: %s, cache hit %v, builds so far %d\n", r.Project, r.Target, r.Status, r.CacheHit, runs)
		}
	}
	// Output:
	// denied "cp api/greeting.txt api/out/": Build through the target: magus run build api
	// ran api:build: ok, cache hit false, builds so far 1
	// ran api:build: ok, cache hit true, builds so far 1
}
```

`Run` judges each target itself, as the `magus run <target> <project>` line the CLI
would be judged by, so a harness that skips `JudgeCommand` still cannot run a denied
target: the deny comes back as a `*types.GuardDeniedError` and nothing runs. Judging
first lets the harness hand the reason back to the agent before it tries.

## Next

- The [Go SDK reference](../reference/go-sdk.md) for the interface roles, the options a
  workspace reads from your process, and testing code that uses the SDK.
- [`magus\guard.command`](../reference/guard-command.md) for the request a command rule
  receives and how it combines with the built-in rules.
