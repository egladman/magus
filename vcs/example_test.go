package vcs

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/magus/types"
)

// ExampleResolve shows how Resolve picks the active VCS and base ref for a
// workspace root. Resolve is typically called once per magus invocation and its
// result is cached in the Workspace.
//
// With no VCS markers on disk and no overrides, Resolve does not error: it falls
// back to the built-in default driver (git) with source "default" and git's
// default base ref. The MAGUS_VCS_* environment variables are cleared first so
// the output does not depend on the caller's environment.
func ExampleResolve() {
	for _, k := range []string{
		"MAGUS_VCS_ENABLED",
		"MAGUS_VCS_NAME",
		"MAGUS_VCS_BASE_REF",
		"MAGUS_VCS_GIT_BASE_REF",
	} {
		_ = os.Unsetenv(k)
	}

	// A path with no .git/.hg/.jj marker, so auto-detection finds nothing and
	// Resolve uses the default driver.
	root := "/nonexistent/path"

	res, err := Resolve(context.Background(), root, "", types.VCSOptions{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("source:", res.Source)
	fmt.Println("vcs:", res.Name)
	fmt.Println("base:", res.Base)

	// Output:
	// source: default
	// vcs: git
	// base: origin/main
}
