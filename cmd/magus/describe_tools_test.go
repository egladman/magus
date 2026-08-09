package main

import (
	"cmp"
	"errors"
	"slices"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A probe can come back six ways and only three of them are a comparison. The other three
// used to render identically as an empty version and "not found", which is a claim about
// a binary that may well be installed - so every one of them is pinned here.
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
		version    string
		verdict    string
		code       string
		cell       string
	}{
		{
			name: "inside the window", t: tool(spells.VersionBounds{Min: "18"}),
			projBounds: spells.VersionBounds{Min: "22", Below: "25"}, raw: "v22.14.0",
			version: "v22.14.0", verdict: verdictInside, cell: "v22.14.0",
		},
		{
			name: "below the floor", t: tool(spells.VersionBounds{}),
			projBounds: spells.VersionBounds{Min: "22"}, raw: "v18.0.0",
			version: "v18.0.0", verdict: verdictTooOld, code: "MGS3005", cell: "v18.0.0",
		},
		{
			name: "at the ceiling", t: tool(spells.VersionBounds{}),
			projBounds: spells.VersionBounds{Below: "25"}, raw: "v26.5.0",
			version: "v26.5.0", verdict: verdictTooNew, code: "MGS3006", cell: "v26.5.0",
		},
		{
			// The binary is absent, or refused to run. This is the ONLY outcome that
			// justifies printing "not found".
			name: "the probe could not run", t: tool(spells.VersionBounds{Min: "22"}),
			raw: "", err: errors.New("exec: node: not found"),
			verdict: verdictUnprobed, cell: "not found",
		},
		{
			// It ran. It just did not say anything version-shaped, which is legal output
			// and must not be reported as a missing binary.
			name: "the probe printed nothing readable", t: tool(spells.VersionBounds{Min: "22"}),
			raw: "nightly", verdict: verdictUnreadable, cell: "unreadable",
		},
		{
			// A bound that survived decode unparsed. Not a violation, and not "fine".
			name: "the bound cannot be compared", t: tool(spells.VersionBounds{Min: "latest"}),
			raw: "v22.14.0", version: "v22.14.0", verdict: verdictUnknown, cell: "v22.14.0",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := buildToolRow("console", "node", "typescript", tc.t, tc.projBounds, tc.raw, tc.err)
			assert.Equal(t, tc.version, row.InstalledVersion)
			assert.Equal(t, tc.verdict, row.Verdict)
			assert.Equal(t, tc.code, row.DiagnosticCode)
			assert.Equal(t, tc.cell, installedCell(row))
			if tc.err != nil {
				assert.Equal(t, tc.err.Error(), row.ProbeError, "why it could not run is the actionable half")
			} else {
				assert.Empty(t, row.ProbeError)
			}
		})
	}
}

// A failing row's first question is whose bound it is, and Intersect has already discarded
// that by the time a verdict exists. Both declarations have to survive onto the row.
func TestBuildToolRowKeepsBothDeclarationsAndTheIntersection(t *testing.T) {
	row := buildToolRow("console", "node", "typescript",
		spells.Tool{Probe: spells.Command{Bin: "node"}, Supported: spells.VersionBounds{Min: "18"}},
		spells.VersionBounds{Min: "22", Below: "25"}, "v26.5.0", nil)

	assert.Equal(t, ">= 18", row.SpellBounds)
	assert.Equal(t, ">= 22, < 25", row.WorkspaceBounds)
	assert.Equal(t, ">= 22, < 25", row.Effective, "the narrower floor wins and the only ceiling survives")
	assert.Equal(t, "spell+ws", declaredByCell(row))
}

func TestDeclaredByNamesWhicheverSideSetTheWindow(t *testing.T) {
	spellOnly := buildToolRow("c", "node", "ts", spells.Tool{Probe: spells.Command{Bin: "node"}, Supported: spells.VersionBounds{Min: "18"}}, spells.VersionBounds{}, "v22.0.0", nil)
	wsOnly := buildToolRow("c", "node", "ts", spells.Tool{Probe: spells.Command{Bin: "node"}}, spells.VersionBounds{Min: "18"}, "v22.0.0", nil)
	neither := buildToolRow("c", "node", "ts", spells.Tool{Probe: spells.Command{Bin: "node"}}, spells.VersionBounds{}, "v22.0.0", nil)

	assert.Equal(t, "spell", declaredByCell(spellOnly))
	assert.Equal(t, "workspace", declaredByCell(wsOnly))
	assert.Equal(t, "-", declaredByCell(neither))
	assert.Equal(t, "unconstrained", windowCell(neither), "a blank cell would read as missing data")
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
func TestToolRowSortIsTotalOverTiedProjectAndBin(t *testing.T) {
	sortRows := func(rows []toolRow) []string {
		slices.SortFunc(rows, func(a, b toolRow) int {
			return cmp.Or(cmp.Compare(a.Project, b.Project), cmp.Compare(a.Bin, b.Bin), cmp.Compare(a.Spell, b.Spell))
		})
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.Project + "/" + r.Bin + "/" + r.Spell
		}
		return out
	}
	want := []string{"console/node/bundler", "console/node/typescript", "docs/node/typescript"}
	require.Equal(t, want, sortRows([]toolRow{
		{Project: "docs", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "bundler"},
	}))
	// Same set, different input order: a total comparator gives the same answer.
	assert.Equal(t, want, sortRows([]toolRow{
		{Project: "console", Bin: "node", Spell: "bundler"},
		{Project: "docs", Bin: "node", Spell: "typescript"},
		{Project: "console", Bin: "node", Spell: "typescript"},
	}))
}
