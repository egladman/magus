package main

import (
	"context"
	"slices"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
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

// A run naming no projects takes every project its upstream's run.scope records named,
// once each and in the order they were first named.
func TestScopeProjects(t *testing.T) {
	var lines []report.Line
	for _, raw := range []string{
		`{"schema":5,"type":"run.scope","label":"2 projects","projects":["libs/x","."]}`,
		`{"schema":5,"type":"run.target.result","project":"libs/y","target":"build","status":"ok","cache_hit":false}`,
		`{"schema":5,"type":"run.scope","label":"libs/x","projects":["libs/x","libs/z"]}`,
	} {
		line, err := report.ParseLine([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, line)
	}
	got, err := scopeProjects(lines)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"libs/x", ".", "libs/z"}; !slices.Equal(got, want) {
		t.Fatalf("scopeProjects = %q, want %q", got, want)
	}
	if got := scopePaths([]types.Target{{Path: ""}, {Path: "a"}, {Path: "a"}}); !slices.Equal(got, []string{".", "a"}) {
		t.Fatalf("scopePaths = %q", got)
	}
}

// A stage with no pipe beside it proves nothing and trades no records.
func TestPipeStageWithoutAPipe(t *testing.T) {
	var s *pipeStage
	if s.writesRecords() {
		t.Fatalf("a nil stage writes records")
	}
	s.linger()
	in, pid := pipeRecordsIn(context.Background())
	if in != nil || pid != 0 {
		t.Fatalf("a process that proved nothing reads records")
	}
}

// TestAdoptedRunGetsNoProcessStdio: the server's stdin is nobody's pipe.
func TestAdoptedRunGetsNoProcessStdio(t *testing.T) {
	if got := processStdioOption(context.Background()); len(got) != 1 {
		t.Fatalf("local run: %d options, want 1", len(got))
	}
	adopted := context.WithValue(context.Background(), magusCtxKey{}, (*magus.Magus)(nil))
	if got := processStdioOption(adopted); len(got) != 0 {
		t.Fatalf("adopted run: %d options, want none", len(got))
	}
}
