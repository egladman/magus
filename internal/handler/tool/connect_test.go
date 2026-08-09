package tool

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	toolv1 "github.com/egladman/magus/proto/gen/go/magus/tool/v1"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type fakeWS struct{ projects []*types.Project }

func (f fakeWS) All() []*types.Project { return f.projects }
func (f fakeWS) Root() string          { return "/tmp" }

// A project whose spells declare no probe contributes no rows, so the view never shows
// a tool magus cannot actually ask about.
func TestListToolsSkipsProjectsWithNoProbedTools(t *testing.T) {
	s := NewService(fakeWS{projects: []*types.Project{{Path: ".", Name: "root"}}}, nil)
	resp, err := s.ListTools(context.Background(), connect.NewRequest(&toolv1.ListToolsRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.Msg.Projects) != 0 {
		t.Fatalf("want no projects, got %d", len(resp.Msg.Projects))
	}
}

// The verdict a page renders must be the one a terminal would raise for the same pair,
// or the console and the CLI disagree about whether a build can run.
func TestVerdictMatchesTheCLIDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bounds  spells.VersionBounds
		version string
		want    toolv1.Verdict
		code    string
	}{
		{"inside", spells.VersionBounds{Min: "1.21"}, "v1.26.5", toolv1.Verdict_VERDICT_INSIDE, ""},
		{"below the floor", spells.VersionBounds{Min: "2.0"}, "v1.4.2", toolv1.Verdict_VERDICT_TOO_OLD, "MGS3005"},
		{"at the ceiling", spells.VersionBounds{Below: "25"}, "v25.0.0", toolv1.Verdict_VERDICT_TOO_NEW, "MGS3006"},
		// An unprobeable tool is not a violation - the same call the CLI's bounds check
		// makes - and must never render as INSIDE, which would read as "checked, fine".
		{"unprobed", spells.VersionBounds{Min: "2.0"}, "", toolv1.Verdict_VERDICT_UNKNOWN, ""},
		{"unparsable bound", spells.VersionBounds{Min: "latest"}, "v1.0.0", toolv1.Verdict_VERDICT_UNKNOWN, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, code := verdict(tc.bounds, tc.version)
			if got != tc.want || code != tc.code {
				t.Fatalf("got %v/%q, want %v/%q", got, code, tc.want, tc.code)
			}
		})
	}
}

// An unconstrained window is nil on the wire, so a client can tell "no window declared"
// from "a window with empty ends".
func TestBoundsNilWhenUnconstrained(t *testing.T) {
	if bounds(spells.VersionBounds{}) != nil {
		t.Fatal("want nil for an unconstrained window")
	}
	if b := bounds(spells.VersionBounds{Below: "25"}); b == nil || b.Below != "25" {
		t.Fatalf("want the ceiling carried, got %v", b)
	}
}
