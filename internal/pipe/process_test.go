package pipe

import (
	"context"
	"slices"
	"testing"

	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// A run naming no projects takes every project its upstream's run.scope records named,
// once each and in the order they were first named.
func TestScopeProjects(t *testing.T) {
	var lines []report.Line
	for _, raw := range []string{
		`{"schema":"magus.report/v1alpha1","type":"run.scope","label":"2 projects","projects":["libs/x","."]}`,
		`{"schema":"magus.report/v1alpha1","type":"run.target.result","project":"libs/y","target":"build","status":"ok","cache_hit":false}`,
		`{"schema":"magus.report/v1alpha1","type":"run.scope","label":"libs/x","projects":["libs/x","libs/z"]}`,
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
	if got := ScopePaths([]types.Target{{Path: ""}, {Path: "a"}, {Path: "a"}}); !slices.Equal(got, []string{".", "a"}) {
		t.Fatalf("ScopePaths = %q", got)
	}
}

// A stage with no pipe beside it proves nothing and trades no records.
func TestStdoutStageWithoutAPipe(t *testing.T) {
	var s *StdoutStage
	if s.WritesRecords() {
		t.Fatalf("a nil stage writes records")
	}
	s.Linger()
	in, pid := RecordsIn(context.Background())
	if in != nil || pid != 0 {
		t.Fatalf("a process that proved nothing reads records")
	}
}

// The server's stdin is nobody's pipe, so an adopted run gets no process streams.
func TestAdoptedRunGetsNoProcessStdio(t *testing.T) {
	if _, ok := StdioFromContext(ProcessStdioContext(context.Background(), false, nil)); !ok {
		t.Fatalf("local run: no streams on its context")
	}
	if _, ok := StdioFromContext(ProcessStdioContext(context.Background(), true, nil)); ok {
		t.Fatalf("adopted run: the process's streams are on its context")
	}
}
