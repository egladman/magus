package hint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFollowUp pins the exact line each tool+outcome earns, byte for byte:
// these strings are appended verbatim to MCP results, so a drifted word here
// is a drifted agent surface.
func TestFollowUp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tool      ToolName
		isError   bool
		mintedRef string
		want      string
	}{
		{
			name:    "run_target error points at describe",
			tool:    ToolRunTarget,
			isError: true,
			want:    "next: list valid targets with magus_describe (kind=targets)",
		},
		{
			name:    "run_affected error points at describe",
			tool:    ToolRunAffected,
			isError: true,
			want:    "next: list valid targets with magus_describe (kind=targets)",
		},
		{
			name:    "where error points at describe projects",
			tool:    ToolWhere,
			isError: true,
			want:    "next: list projects with magus_describe (kind=projects)",
		},
		{
			name:    "output error explains where refs come from",
			tool:    ToolOutput,
			isError: true,
			want:    "next: output refs come from magus_run_target or magus_tail_log",
		},
		{
			name:    "explain error recovers via query",
			tool:    ToolExplain,
			isError: true,
			want:    "next: locate a node with magus_query, then explain it",
		},
		{
			name:    "path error recovers via query",
			tool:    ToolPath,
			isError: true,
			want:    "next: locate the endpoints with magus_query",
		},
		{
			name:    "refs error recovers via query",
			tool:    ToolRefs,
			isError: true,
			want:    "next: locate a symbol with magus_query",
		},
		{
			name:    "unmapped tool error gets no hint",
			tool:    ToolStats,
			isError: true,
			want:    "",
		},
		{
			name: "affected_plan success chains into run_affected",
			tool: ToolAffectedPlan,
			want: "next: run the affected set with magus_run_affected",
		},
		{
			name:      "run success carrying a ref chains into output naming the ref",
			tool:      ToolRunTarget,
			mintedRef: "out1a2b3c4d",
			want:      "next: fetch the captured output with magus_output (ref=out1a2b3c4d)",
		},
		{
			name: "run success with no ref gets no chain hint",
			tool: ToolRunAffected,
			want: "",
		},
		{
			name:      "ref on a non-minting tool earns nothing",
			tool:      ToolQuery,
			mintedRef: "out1a2b3c4d",
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FollowUp(tt.tool.String(), tt.isError, tt.mintedRef))
		})
	}

	// A plain success from a read tool earns nothing - output bytes are the
	// agent's context cost, so silent successes stay lean.
	for _, tool := range []ToolName{ToolQuery, ToolExplain, ToolStats, ToolDescribe, ToolWhere} {
		assert.Empty(t, FollowUp(tool.String(), false, ""), "no follow-up for a plain %s success", tool)
	}
}

func TestMintsRef(t *testing.T) {
	t.Parallel()

	assert.True(t, MintsRef(ToolRunTarget.String()))
	assert.True(t, MintsRef(ToolRunAffected.String()))
	assert.False(t, MintsRef(ToolQuery.String()))
	assert.False(t, MintsRef(ToolAffectedPlan.String()))
}
