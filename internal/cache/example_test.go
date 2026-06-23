package cache

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
)

// ExampleCache_Run shows the minimal round-trip: a miss on the first call runs
// the work; a second call for the same step replays it as a hit without running.
//
// The cache's own progress logging is raised to Error level so only the example's
// own prints reach stdout, keeping the output deterministic (the real CLI logs
// "[cache] miss/hit (<duration>)" lines, whose durations vary run to run).
func ExampleCache_Run() {
	// A fresh, private store so the first run is always a cold miss regardless of
	// any earlier run or example. t.TempDir is unavailable in an Example, so this
	// manages its own temp dir.
	dir, err := os.MkdirTemp("", "magus-cache-example-run")
	if err != nil {
		fmt.Println("setup:", err)
		return
	}
	defer os.RemoveAll(dir)

	c, err := Open(dir, WithLog("text", slog.LevelError))
	if err != nil {
		fmt.Println("open:", err)
		return
	}

	step := Step{
		ProjectPath:   "api",
		WorkspaceRoot: ".",
		Target:        "build",
	}

	ran := 0
	fn := func(_ context.Context) error {
		ran++
		return nil
	}

	// First run: cold miss → fn is called.
	r1, err := c.Run(context.Background(), step, fn)
	if err != nil {
		fmt.Println("run1:", err)
		return
	}
	fmt.Println("run1 hit:", r1.Hit)

	// Second run on the same store: hit → fn is skipped.
	r2, err := c.Run(context.Background(), step, fn)
	if err != nil {
		fmt.Println("run2:", err)
		return
	}
	fmt.Println("run2 hit:", r2.Hit)
	fmt.Println("fn ran:", ran, "time(s)")

	// Output:
	// run1 hit: false
	// run2 hit: true
	// fn ran: 1 time(s)
}

// ExampleCache_RunAll shows fan-out across multiple steps with bounded
// concurrency and per-result callbacks. RunAll schedules its steps concurrently,
// so the order callbacks fire in is not deterministic; the example collects the
// outcomes and sorts them before printing.
func ExampleCache_RunAll() {
	dir, err := os.MkdirTemp("", "magus-cache-example-runall")
	if err != nil {
		fmt.Println("setup:", err)
		return
	}
	defer os.RemoveAll(dir)

	c, err := Open(dir, WithLog("text", slog.LevelError))
	if err != nil {
		fmt.Println("open:", err)
		return
	}

	steps := []Step{
		{ProjectPath: "api", WorkspaceRoot: ".", Target: "test"},
		{ProjectPath: "web", WorkspaceRoot: ".", Target: "test"},
	}

	var mu sync.Mutex
	var missed []string
	results, err := c.RunAll(
		context.Background(), steps,
		func(_ context.Context, _ Step) error { return nil },
		WithConcurrency(4),
		OnMiss(func(r *Result) {
			mu.Lock()
			missed = append(missed, r.ProjectPath)
			mu.Unlock()
		}),
	)
	if err != nil {
		fmt.Println("runall:", err)
		return
	}

	sort.Strings(missed)
	fmt.Println("results:", len(results))
	fmt.Println("missed:", missed)

	// Output:
	// results: 2
	// missed: [api web]
}
