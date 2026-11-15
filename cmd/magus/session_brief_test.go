package main

import (
	"os"
	"testing"
)

// TestBriefRefusalHelperProcess is the process TestTheCLIPrintsTheBriefRefusal
// starts. A fresh process builds its own stderr display handler, which an
// earlier in-process runCLI would otherwise have bound to another stderr.
func TestBriefRefusalHelperProcess(t *testing.T) {
	root := os.Getenv(briefRefusalRootVar)
	if root == "" {
		t.Skip("not invoked as the brief refusal helper process")
	}
	t.Setenv("MAGUS_BROKER", "required")
	os.Args = []string{"magus", "--root", root, "session", "--brief"}
	os.Exit(runCLI())
}
