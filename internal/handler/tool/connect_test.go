package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	toolv1 "github.com/egladman/magus/proto/gen/go/magus/tool/v1"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWS struct{ projects []*types.Project }

func (f fakeWS) All() []*types.Project { return f.projects }

// probedProject builds a project whose spell probes through an injected function, so the
// cache, the TTL, and the failure paths are exercised without forking anything. The
// counter is the only way to prove a probe did not run.
func probedProject(path, spellName, bin string, supported spells.VersionBounds, ws spells.VersionBounds, out func() (string, error), calls *int) *types.Project {
	sp := spells.NewSpell(spellName,
		spells.WithTools(map[string]spells.Tool{
			bin: {Probe: spells.Command{Bin: bin, Args: []string{"--version"}}, Supported: supported},
		}),
		spells.WithVersionProber(func(_ context.Context, _ spells.Command, _ string) (string, error) {
			*calls++
			return out()
		}),
	)
	p := &types.Project{Path: path, Name: path, Dir: "/tmp/" + path, ResolvedSpells: []*spells.Spell{sp}}
	if !ws.IsZero() {
		p.ToolBounds = map[string]spells.VersionBounds{bin: ws}
	}
	return p
}

func list(t *testing.T, s *Service, project string) *toolv1.ListToolsResponse {
	t.Helper()
	resp, err := s.ListTools(t.Context(), connect.NewRequest(&toolv1.ListToolsRequest{Project: project}))
	require.NoError(t, err)
	return resp.Msg
}

// A project whose spells declare no probe contributes no rows, so the view never shows
// a tool magus cannot actually ask about.
func TestListToolsSkipsProjectsWithNoProbedTools(t *testing.T) {
	s := NewService(fakeWS{projects: []*types.Project{{Path: ".", Name: "root"}}})
	assert.Empty(t, list(t, s, "").Projects)
}

// The three windows travel separately, because the first question about a failing bound
// is who set it and the intersection has already discarded that.
func TestListToolsCarriesBothDeclarationsAndTheIntersection(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node",
		spells.VersionBounds{Min: "18"}, spells.VersionBounds{Min: "22", Below: "25"},
		func() (string, error) { return "v26.5.0", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})

	msg := list(t, s, "")
	require.Len(t, msg.Projects, 1)
	require.Len(t, msg.Projects[0].Tools, 1)
	row := msg.Projects[0].Tools[0]

	assert.Equal(t, "node", row.Bin)
	assert.Equal(t, "typescript", row.Spell)
	assert.Equal(t, "v26.5.0", row.InstalledVersion)
	assert.Equal(t, "18", row.SpellBounds.GetMin())
	assert.Equal(t, "22", row.WorkspaceBounds.GetMin())
	// The narrower floor wins, and the ceiling only one side declared survives.
	assert.Equal(t, "22", row.Effective.GetMin())
	assert.Equal(t, "25", row.Effective.GetBelow())
	assert.Equal(t, toolv1.Verdict_VERDICT_TOO_NEW, row.Verdict)
	assert.Equal(t, "MGS3006", row.DiagnosticCode)
	assert.NotNil(t, row.ProbedAt, "a reading that happened carries its age")
}

// A second request inside the TTL must not fork again. This is the only reason the
// handler holds a cache at all, and it was previously untested.
func TestListToolsServesTheCacheWithinTheTTL(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{},
		func() (string, error) { return "v22.14.0", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})

	first := list(t, s, "").Projects[0].Tools[0]
	second := list(t, s, "").Projects[0].Tools[0]

	assert.Equal(t, 1, calls, "the second request must be served from the cache")
	assert.Equal(t, first.ProbedAt.AsTime(), second.ProbedAt.AsTime(), "and it must report the age of the reading it served, not of the request")
}

// Past the TTL the reading is taken again, or the view would pin a version installed
// minutes ago and never notice a toolchain change.
func TestListToolsReprobesAfterTheTTL(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{},
		func() (string, error) { return "v22.14.0", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})
	s.ttl = time.Nanosecond

	list(t, s, "")
	list(t, s, "")
	assert.Equal(t, 2, calls)
}

// A failed probe is cached like any other reading. Caching only successes left the tools
// that are absent - the population this view exists to show - re-forking every request,
// which is the fork loop the TTL was written to prevent.
func TestListToolsCachesAFailedProbe(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{Min: "22"},
		func() (string, error) { return "", errors.New("exec: node: not found") }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})

	row := list(t, s, "").Projects[0].Tools[0]
	list(t, s, "")

	assert.Equal(t, 1, calls, "an absent tool must not re-fork on every request")
	assert.Empty(t, row.InstalledVersion)
	assert.Equal(t, toolv1.Verdict_VERDICT_UNKNOWN, row.Verdict, "an absent tool is not a violation")
	assert.Empty(t, row.DiagnosticCode)
}

// Output carrying no version is a reading that happened and told us nothing. It must not
// render as INSIDE, and it must not be mistaken for a tool that is missing.
func TestListToolsReportsUnreadableOutputAsUnknown(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{Min: "22"},
		func() (string, error) { return "nightly", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})

	row := list(t, s, "").Projects[0].Tools[0]
	assert.Equal(t, toolv1.Verdict_VERDICT_UNKNOWN, row.Verdict)
	assert.NotNil(t, row.ProbedAt, "the probe ran, so the row carries when")
}

// Two spells declaring the same bin ask different questions - the argv comes from the
// spell - so they must not share one cached answer.
func TestListToolsKeysTheCacheBySpell(t *testing.T) {
	calls := 0
	prober := func(name string) *spells.Spell {
		return spells.NewSpell(name,
			spells.WithTools(map[string]spells.Tool{
				"node": {Probe: spells.Command{Bin: "node", Args: []string{name}}},
			}),
			spells.WithVersionProber(func(_ context.Context, cmd spells.Command, _ string) (string, error) {
				calls++
				if cmd.Args[0] == "typescript" {
					return "v22.14.0", nil
				}
				return "v18.0.0", nil
			}),
		)
	}
	p := &types.Project{Path: "console", Name: "console", Dir: "/tmp/console",
		ResolvedSpells: []*spells.Spell{prober("typescript"), prober("bundler")}}
	s := NewService(fakeWS{projects: []*types.Project{p}})

	tools := list(t, s, "").Projects[0].Tools
	require.Len(t, tools, 2)
	assert.Equal(t, 2, calls, "one cached answer must not be served for two different probes")
	got := map[string]string{tools[0].Spell: tools[0].InstalledVersion, tools[1].Spell: tools[1].InstalledVersion}
	assert.Equal(t, map[string]string{"typescript": "v22.14.0", "bundler": "v18.0.0"}, got)
}

// A tool keyed by a declared constant has no argv to run, so it is not shown. `magus
// describe tools` skips it for the same reason; the two surfaces must agree about which
// tools exist.
func TestListToolsSkipsAToolWithNoArgvToRun(t *testing.T) {
	sp := spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{
		"node": {Key: spells.VersionKey{Const: "pinned-1"}},
	}))
	s := NewService(fakeWS{projects: []*types.Project{{Path: "console", Dir: "/tmp/console", ResolvedSpells: []*spells.Spell{sp}}}})
	assert.Empty(t, list(t, s, "").Projects)
}

// Naming a project narrows the response to it.
func TestListToolsFiltersToOneProject(t *testing.T) {
	calls := 0
	a := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{}, func() (string, error) { return "v22.14.0", nil }, &calls)
	b := probedProject("docs", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{}, func() (string, error) { return "v22.14.0", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{a, b}})

	msg := list(t, s, "docs")
	require.Len(t, msg.Projects, 1)
	assert.Equal(t, "docs", msg.Projects[0].Path)
}

// A project that does not exist is a client error. Returned as an empty list it would be
// indistinguishable from a project that declares no tool.
func TestListToolsRejectsAnUnknownProject(t *testing.T) {
	s := NewService(fakeWS{projects: []*types.Project{{Path: ".", Name: "root"}}})
	_, err := s.ListTools(t.Context(), connect.NewRequest(&toolv1.ListToolsRequest{Project: "nope"}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

// An abandoned request stops forking. Walking the rest of the workspace to return a blank
// report as success is worse than saying the request was cancelled.
func TestListToolsStopsProbingWhenTheRequestIsAbandoned(t *testing.T) {
	calls := 0
	p := probedProject("console", "typescript", "node", spells.VersionBounds{}, spells.VersionBounds{}, func() (string, error) { return "v22.14.0", nil }, &calls)
	s := NewService(fakeWS{projects: []*types.Project{p}})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.ListTools(ctx, connect.NewRequest(&toolv1.ListToolsRequest{}))
	require.Error(t, err)
	assert.Equal(t, connect.CodeCanceled, connect.CodeOf(err))
	assert.Equal(t, 0, calls)
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
		// An unprobeable tool is not a violation - the same call the CLI's window check
		// makes - and must never render as INSIDE, which would read as "checked, fine".
		{"unprobed", spells.VersionBounds{Min: "2.0"}, "", toolv1.Verdict_VERDICT_UNKNOWN, ""},
		{"unparsable bound", spells.VersionBounds{Min: "latest"}, "v1.0.0", toolv1.Verdict_VERDICT_UNKNOWN, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := verdict(tc.bounds, tc.version)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.code, diagnosticFor(got))
		})
	}
}

// A verdict nobody enumerated must surface as "we could not check". Rendering it as
// INSIDE would report an unchecked tool as fine, which is the one wrong answer this view
// must never give.
func TestVerdictFallsBackToUnknownNotInside(t *testing.T) {
	assert.Equal(t, toolv1.Verdict_VERDICT_UNKNOWN, verdict(spells.VersionBounds{Min: "not-a-version"}, "v1.0.0"))
}

// An unconstrained window is nil on the wire, so a client can tell "no window declared"
// from "a window with empty ends".
func TestBoundsNilWhenUnconstrained(t *testing.T) {
	assert.Nil(t, boundsToProto(spells.VersionBounds{}))
	b := boundsToProto(spells.VersionBounds{Below: "25"})
	require.NotNil(t, b)
	assert.Equal(t, "25", b.Below)
}
