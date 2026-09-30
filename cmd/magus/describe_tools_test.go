package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// Only a probe that could not run is "not found"; one that ran and printed nothing
// version-shaped is present and says so.
func TestInstalledCellSeparatesAbsentFromUnreadable(t *testing.T) {
	assert.Equal(t, "v22.14.0", installedCell(types.ToolRow{InstalledVersion: "v22.14.0", Verdict: types.ToolVerdictInside}))
	assert.Equal(t, "not found", installedCell(types.ToolRow{Verdict: types.ToolVerdictUnprobed}))
	assert.Equal(t, "unreadable", installedCell(types.ToolRow{Verdict: types.ToolVerdictUnreadable}))
}

func TestDeclaredByNamesWhicheverSideSetTheWindow(t *testing.T) {
	assert.Equal(t, "spell+ws", declaredByCell(types.ToolRow{SpellBounds: ">= 18", WorkspaceBounds: ">= 22"}))
	assert.Equal(t, "spell", declaredByCell(types.ToolRow{SpellBounds: ">= 18"}))
	assert.Equal(t, "workspace", declaredByCell(types.ToolRow{WorkspaceBounds: ">= 18"}))
	assert.Equal(t, "-", declaredByCell(types.ToolRow{}))
	assert.Equal(t, "unconstrained", windowCell(types.ToolRow{}), "a blank cell would read as missing data")
}

// The table words a verdict the way the console does, while the JSON keeps too_old.
func TestDescribeToolsVerdictCellSpacesTheVerdictAndNamesTheCode(t *testing.T) {
	assert.Equal(t, "too old (MGS3005)", verdictCell(types.ToolRow{Verdict: types.ToolVerdictTooOld, DiagnosticCode: "MGS3005"}))
	assert.Equal(t, "too new (MGS3006)", verdictCell(types.ToolRow{Verdict: types.ToolVerdictTooNew, DiagnosticCode: "MGS3006"}))
	assert.Equal(t, "inside", verdictCell(types.ToolRow{Verdict: types.ToolVerdictInside}))
}

// The header is the network announcement: every state a report can be in names itself,
// and a live one names what was read.
func TestLifecycleHeaderNamesTheStateAndTheReads(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    types.LifecycleStatus
		want string
	}{
		{"unwired", types.LifecycleStatus{State: types.LifecycleUnwired},
			`lifecycle: no provider wired (magus\lifecycle.provider); the lifecycle columns read -`},
		{"live", types.LifecycleStatus{
			Provider: "endoflife-date", State: types.LifecycleLive, AsOf: "2026-09-24T07:44:41Z",
			Sources: []string{"https://endoflife.date/api/v1/products/go", "https://endoflife.date/api/v1/products/nodejs"},
		}, "lifecycle: endoflife-date, GET https://endoflife.date/api/v1/products/{go,nodejs} (as of 2026-09-24T07:44:41Z)"},
		{"offline with nothing stored", types.LifecycleStatus{Provider: "endoflife-date", State: types.LifecycleOffline},
			"lifecycle: endoflife-date, offline and nothing stored; support reads unknown (offline)"},
		{"unreached, replaying", types.LifecycleStatus{
			Provider: "endoflife-date", State: types.LifecycleUnreached, FetchedAt: "2026-09-28T10:00:00Z",
			Sources: []string{"https://endoflife.date/api/v1/products/go"},
		}, "lifecycle: endoflife-date, unreached; replaying the answer fetched 2026-09-28T10:00:00Z from https://endoflife.date/api/v1/products/go"},
		{"nothing to ask", types.LifecycleStatus{Provider: "endoflife-date", State: types.LifecycleLive},
			"lifecycle: endoflife-date, nothing to ask (no spell names a lifecycle product)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, lifecycleHeader(tc.s))
		})
	}
}

func TestBraceJoinFoldsOnlyASharedDirectory(t *testing.T) {
	assert.Equal(t, "https://a/p/{go,nodejs}", braceJoin([]string{"https://a/p/go", "https://a/p/nodejs"}))
	assert.Equal(t, "https://a/p/go, https://b/q/nodejs", braceJoin([]string{"https://a/p/go", "https://b/q/nodejs"}))
	assert.Equal(t, "https://a/p/go", braceJoin([]string{"https://a/p/go"}))
	assert.Empty(t, braceJoin(nil))
}

func TestSupportCellSaysWhyItIsUnknown(t *testing.T) {
	unknown := types.ToolRow{Support: "unknown"}
	assert.Equal(t, "unknown (unreached)", supportCell(unknown, types.LifecycleUnreached))
	assert.Equal(t, "unknown (offline)", supportCell(unknown, types.LifecycleOffline))
	assert.Equal(t, "unknown", supportCell(unknown, types.LifecycleLive), "live and unmatched is a plain unknown")
	assert.Equal(t, "eol", supportCell(types.ToolRow{Support: "eol"}, types.LifecycleLive))
	assert.Equal(t, "-", supportCell(types.ToolRow{}, types.LifecycleUnwired))
}
