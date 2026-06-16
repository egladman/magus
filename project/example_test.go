package project

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// ExampleDiscover shows how to discover projects in a workspace root using
// the project package directly. Callers that want the full orchestrator
// (cache, telemetry, VCS integration) should use magus.Inspect instead.
func ExampleDiscover() {
	// Create a minimal workspace with two projects for illustration.
	root, err := os.MkdirTemp("", "magus-project-example-*")
	if err != nil {
		fmt.Println("setup error:", err)
		return
	}
	defer os.RemoveAll(root)

	for _, name := range []string{"api", "web"} {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Println("setup error:", err)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "magusfile.tl"), []byte(""), 0o644); err != nil {
			fmt.Println("setup error:", err)
			return
		}
	}

	ws, err := Discover(context.Background(), root)
	if err != nil {
		fmt.Println("inspect error:", err)
		return
	}

	for _, p := range ws.All() {
		fmt.Println(p.Path)
	}
	// Output:
	// api
	// web
}
