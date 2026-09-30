package magus

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// A probe can come back six ways and only three of them are a comparison. The other three
// must stay distinct, because an empty version reads as "not found", which is a claim about
// a binary that may well be installed.
func TestBuildToolRowSeparatesEveryProbeOutcome(t *testing.T) {
	tool := func(sup spells.VersionBounds) spells.Tool {
		return spells.Tool{Probe: spells.Command{Bin: "node"}, Supported: sup}
	}
	for _, tc := range []struct {
		name       string
		t          spells.Tool
		projBounds spells.VersionBounds
		raw        string
		err        error
		want       types.ToolRow
	}{
		{
			name: "inside the window", t: tool(spells.VersionBounds{Min: "18"}),
			projBounds: spells.VersionBounds{Min: "22", Below: "25"}, raw: "v22.14.0",
			want: types.ToolRow{InstalledVersion: "v22.14.0", Verdict: types.ToolVerdictInside,
				SpellBounds: ">= 18", WorkspaceBounds: ">= 22, < 25", Effective: ">= 22, < 25"},
		},
		{
			name: "below the floor", t: tool(spells.VersionBounds{}),
			projBounds: spells.VersionBounds{Min: "22"}, raw: "v18.0.0",
			want: types.ToolRow{InstalledVersion: "v18.0.0", Verdict: types.ToolVerdictTooOld, DiagnosticCode: "MGS3005",
				WorkspaceBounds: ">= 22", Effective: ">= 22"},
		},
		{
			name: "at the ceiling", t: tool(spells.VersionBounds{}),
			projBounds: spells.VersionBounds{Below: "25"}, raw: "v26.5.0",
			want: types.ToolRow{InstalledVersion: "v26.5.0", Verdict: types.ToolVerdictTooNew, DiagnosticCode: "MGS3006",
				WorkspaceBounds: "< 25", Effective: "< 25"},
		},
		{
			// The binary is absent, or refused to run: the only outcome that is "not found".
			name: "the probe could not run", t: tool(spells.VersionBounds{Min: "22"}),
			err: errors.New("exec: node: not found"),
			want: types.ToolRow{Verdict: types.ToolVerdictUnprobed, ProbeError: "exec: node: not found",
				SpellBounds: ">= 22", Effective: ">= 22"},
		},
		{
			// It ran and said nothing version-shaped, which is not a missing binary.
			name: "the probe printed nothing readable", t: tool(spells.VersionBounds{Min: "22"}),
			raw:  "nightly",
			want: types.ToolRow{Verdict: types.ToolVerdictUnreadable, SpellBounds: ">= 22", Effective: ">= 22"},
		},
		{
			// A bound that survived decode unparsed. Not a violation, and not "fine".
			name: "the bound cannot be compared", t: tool(spells.VersionBounds{Min: "latest"}),
			raw: "v22.14.0",
			want: types.ToolRow{InstalledVersion: "v22.14.0", Verdict: types.ToolVerdictUnknown,
				SpellBounds: ">= latest", Effective: ">= latest"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := tc.want
			want.Project, want.Bin, want.Spell = "console", "node", "typescript"
			assert.Equal(t, want, buildToolRow("console", "node", "typescript", tc.t, tc.projBounds, tc.raw, tc.err))
		})
	}
}

// below is the first version REJECTED, not the last accepted, so it can never render as a
// maximum: "< 25" accepts 24.19.0 and rejects 25.0.0.
func TestRenderWindowNeverPrintsBelowAsAMaximum(t *testing.T) {
	assert.Equal(t, ">= 22, < 25", renderWindow(spells.VersionBounds{Min: "22", Below: "25"}))
	assert.Equal(t, ">= 1.26", renderWindow(spells.VersionBounds{Min: "1.26"}))
	assert.Equal(t, "< 25", renderWindow(spells.VersionBounds{Below: "25"}))
	assert.Empty(t, renderWindow(spells.VersionBounds{}))
}

// Two spells in one project can declare the same bin. Without the spell as a third key
// their order is whatever the sort happened to do, and a read-only command's JSON output
// would differ between runs on identical input.
func TestSortToolRowsIsTotalOverTiedProjectAndBin(t *testing.T) {
	order := func(rows []types.ToolRow) []string {
		sortToolRows(rows)
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Project + "/" + r.Bin + "/" + r.Spell
		}
		return out
	}
	want := []string{"console/node/bundler", "console/node/typescript", "docs/node/typescript"}
	require.Equal(t, want, order([]types.ToolRow{
		{Project: "docs", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "bundler"},
	}))
	assert.Equal(t, want, order([]types.ToolRow{
		{Project: "console", Bin: "node", Spell: "bundler"},
		{Project: "docs", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "typescript"},
	}))
}

func TestJoinLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	golang := []spells.Lifecycle{{Key: "go", Cycles: []spells.ReleaseCycle{
		{Cycle: "1.25", EOL: "2026-08-19"},
		{Cycle: "1.26", EOL: "2027-02-10"},
		{Cycle: "1.27"},
	}}}
	for _, tc := range []struct {
		name      string
		row       types.ToolRow
		state     string
		lifecycle []spells.Lifecycle
		cycle     string
		eol       string
		support   string
	}{
		{"past its end of life", types.ToolRow{Lifecycle: "go", InstalledVersion: "v1.25.3"}, types.LifecycleLive, golang, "1.25", "2026-08-19", "eol"},
		{"supported", types.ToolRow{Lifecycle: "go", InstalledVersion: "v1.26.5"}, types.LifecycleLive, golang, "1.26", "2027-02-10", "supported"},
		{"no end date announced", types.ToolRow{Lifecycle: "go", InstalledVersion: "v1.27.1"}, types.LifecycleLive, golang, "1.27", "", "unannounced"},
		{"no cycle carries the version", types.ToolRow{Lifecycle: "go", InstalledVersion: "v2.0.0"}, types.LifecycleLive, golang, "", "", "unknown"},
		{"the provider did not answer this key", types.ToolRow{Lifecycle: "nodejs", InstalledVersion: "v22.3.0"}, types.LifecycleLive, golang, "", "", "unknown"},
		{"unreached and nothing stored", types.ToolRow{Lifecycle: "go", InstalledVersion: "v1.26.5"}, types.LifecycleUnreached, nil, "", "", "unknown"},
		{"no version was read", types.ToolRow{Lifecycle: "go"}, types.LifecycleLive, golang, "", "", "unknown"},
		{"no provider wired", types.ToolRow{Lifecycle: "go", InstalledVersion: "v1.25.3"}, types.LifecycleUnwired, nil, "", "", ""},
		{"the spell names no product", types.ToolRow{InstalledVersion: "v1.25.3"}, types.LifecycleLive, golang, "", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := tc.row
			joinLifecycle(&row, tc.state, tc.lifecycle, now)
			assert.Equal(t, tc.cycle, row.Cycle)
			assert.Equal(t, tc.eol, row.EOL)
			assert.Equal(t, tc.support, row.Support)
		})
	}
}
