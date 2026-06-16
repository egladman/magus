package vcs

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

// ExampleResolve shows how to resolve the active VCS and base ref for a
// workspace root. Resolve is typically called once per magus invocation;
// its result is cached in the Workspace.
func ExampleResolve() {
	// In production this would be the workspace root on disk.
	// The example uses a non-existent path so it falls back gracefully.
	root := "/nonexistent/path"

	res, err := Resolve(context.Background(), root, "", types.VCSOptions{})
	if err != nil {
		// Resolve returns an error when no VCS is detected. Callers may
		// choose to proceed without VCS support (affected set = all projects).
		fmt.Println("no vcs:", err)
		return
	}

	fmt.Println("vcs:", res.Name)
	fmt.Println("base:", res.Base)
}
