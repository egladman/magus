package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ExampleCache_Run shows the minimal round-trip: miss on first call,
// hit on second call for the same step.
func ExampleCache_Run() {
	dir := filepath.Join(os.TempDir(), "magus-cache-example")
	c, err := Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	step := Step{
		ProjectPath:   "api",
		WorkspaceRoot: ".",
		Target:        "build",
	}

	fn := func(_ context.Context) error {
		fmt.Println("building api…")
		return nil
	}

	// First run: miss → fn is called.
	r1, _ := c.Run(context.Background(), step, fn)
	fmt.Println("hit:", r1.Hit) // hit: false

	// Second run with a fresh cache in read mode: hit → fn is skipped.
	_ = os.Setenv("MAGUS_CACHE_MODE", "read")
	c2, _ := Open(dir)
	r2, _ := c2.Run(context.Background(), step, fn)
	fmt.Println("hit:", r2.Hit) // hit: true
}

// ExampleCache_RunAll shows fan-out across multiple steps with a shared
// limiter, bounded concurrency, and per-result callbacks.
func ExampleCache_RunAll() {
	dir := filepath.Join(os.TempDir(), "magus-cache-example")
	c, err := Open(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	steps := []Step{
		{ProjectPath: "api", WorkspaceRoot: ".", Target: "test"},
		{ProjectPath: "web", WorkspaceRoot: ".", Target: "test"},
	}

	_, err = c.RunAll(
		context.Background(), steps,
		func(_ context.Context, s Step) error {
			fmt.Println("testing", s.ProjectPath)
			return nil
		},
		WithConcurrency(4),
		OnHit(func(r *Result) {
			fmt.Println("cached:", r.ProjectPath)
		}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
