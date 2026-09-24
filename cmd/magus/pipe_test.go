package main

import (
	"context"
	"testing"

	"github.com/egladman/magus"
)

func TestTakesProjectLocks(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"magus", "run", "build", "."}, true},
		{[]string{"/usr/bin/magus", "-C", "/w", "run", "test", "a"}, true},
		{[]string{"magus", "affected", "ci"}, true},
		{[]string{"magus", "affected", "ci", "--plan", "--preflight", "generate", "--no-default-charms"}, true},
		{[]string{"magus", "affected", "ci", "--plan", "--preflight=generate"}, true},
		{[]string{"magus", "clean", "."}, true},
		{[]string{"magus", "refs", "Open"}, true},
		{[]string{"magus", "graph", "build"}, true},
		// Read-only producers: a consumer of their stream starts at once.
		{[]string{"magus", "affected", "ci", "--plan"}, false},
		{[]string{"magus", "run", "--help"}, false},
		{[]string{"magus", "status", "--watch", "-o", "jsonl"}, false},
		{[]string{"magus", "ls", "-o", "json"}, false},
		{[]string{"magus", "query", "kind=target"}, false},
		{[]string{"magus", "buzz", "emit.buzz"}, false},
		// Nothing to classify: assume the worst, which costs a wait and never a refusal.
		{nil, true},
	} {
		if got := takesProjectLocks(tc.argv); got != tc.want {
			t.Errorf("takesProjectLocks(%q) = %v, want %v", tc.argv, got, tc.want)
		}
	}
}

// TestAdoptedRunGetsNoProcessStdio: the daemon's stdin is nobody's pipe.
func TestAdoptedRunGetsNoProcessStdio(t *testing.T) {
	if got := processStdioOption(context.Background()); len(got) != 1 {
		t.Fatalf("local run: %d options, want 1", len(got))
	}
	adopted := context.WithValue(context.Background(), magusCtxKey{}, (*magus.Magus)(nil))
	if got := processStdioOption(adopted); len(got) != 0 {
		t.Fatalf("adopted run: %d options, want none", len(got))
	}
}
