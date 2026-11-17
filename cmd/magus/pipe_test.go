package main

import (
	"context"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/pipe"
)

func TestTakesProjectLocks(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want bool
	}{
		{[]string{"magus", "run", "build", "."}, true},
		{[]string{"/usr/bin/magus", "-C", "/w", "run", "test", "a"}, true},
		{[]string{"magus", "affected", "ci"}, true},
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

// Both ends of a pipe classify a stage with pipeRecordStage, so what it answers is what
// the pipe carries: records from a run or a script, and the explicit format or the
// verb's own product everywhere else.
func TestPipeRecordStage(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"run", "format", "libs/x"}, true},
		{[]string{"-C", "/w", "run", "test"}, true},
		{[]string{"run", "test:rw", ".", "--", "-o", "x"}, true},
		{[]string{"affected", "ci"}, true},
		{[]string{"buzz", "failures.buzz"}, true},
		{[]string{"buzz", "-e", "print(1)"}, true},
		{[]string{"buzz", "pick.buzz", "--", "--test"}, true},
		// An explicit -o, in any spelling or position, keeps the format asked for.
		{[]string{"run", "test", "-o", "json"}, false},
		{[]string{"-o", "text", "run", "test"}, false},
		{[]string{"run", "test", "--output=jsonl"}, false},
		// A verb whose stdout is its product keeps it.
		{[]string{"run", "ls"}, false},
		{[]string{"run", "build", "--graph"}, false},
		{[]string{"run", "build", "--detach"}, false},
		{[]string{"run", "--help"}, false},
		{[]string{"affected", "ci", "--plan"}, false},
		{[]string{"affected", "ls"}, false},
		{[]string{"affected", "test", "--bisect"}, false},
		// A script read from stdin has stdin as its source; the other modes run no script.
		{[]string{"buzz"}, false},
		{[]string{"buzz", "-"}, false},
		{[]string{"buzz", "-t", "x.buzz"}, false},
		{[]string{"buzz", "--check", "x.buzz"}, false},
		{[]string{"buzz", "lsp"}, false},
		{[]string{"ls"}, false},
		{[]string{"status", "--watch"}, false},
		{nil, false},
	} {
		if got := pipeRecordStage(tc.args); got != tc.want {
			t.Errorf("pipeRecordStage(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
	// -o decides only what a stage writes; it still reads the records written to it.
	if !tradesRecords([]string{"run", "lint", "-o", "json"}) {
		t.Errorf("tradesRecords(run lint -o json) = false")
	}
}

// TestAdoptedRunGetsNoProcessStdio: the server's stdin is nobody's pipe.
func TestAdoptedRunGetsNoProcessStdio(t *testing.T) {
	if _, ok := pipe.StdioFromContext(processStdioContext(context.Background())); !ok {
		t.Fatalf("local run: no streams on its context")
	}
	adopted := context.WithValue(context.Background(), magusCtxKey{}, (*magus.Magus)(nil))
	if _, ok := pipe.StdioFromContext(processStdioContext(adopted)); ok {
		t.Fatalf("adopted run: the process's streams are on its context")
	}
}
