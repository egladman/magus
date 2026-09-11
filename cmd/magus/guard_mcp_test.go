package main

import (
	"bytes"
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPJudgedParamsCoverEveryMergedField holds the guard's view of a ledger put to the
// ledger's own. A field ledger.ParseMerge applies and the renderer drops reaches the row with no
// rule having read it, and the rebind rule then clears a rewrite of it as a plain shrink.
//
// The accepted set is PROBED rather than restated: ledger.ParseMerge exports no key list, and a
// second hand-written one is forgotten in the same direction as the first.
func TestMCPJudgedParamsCoverEveryMergedField(t *testing.T) {
	merged := 0
	for _, field := range leaseJSONFields() {
		if !ledgerMergeApplies(field) {
			continue
		}
		merged++
		assert.Contains(t, mcpJudgedParams, field,
			"ledger.ParseMerge applies %q, so a call carrying it has to be judged", field)
	}
	require.NotZero(t, merged, "the probe found no merged field at all, so it is measuring nothing")

	for _, key := range mcpJudgedParams {
		if key == "op" || key == "id" || slices.Contains(mcpRenamedParams, key) {
			continue
		}
		assert.True(t, ledgerMergeApplies(key), "%q is judged but no ledger put applies it", key)
	}
}

// leaseJSONFields are the row's wire names, which is the vocabulary both ledger doors speak.
func leaseJSONFields() []string {
	t := reflect.TypeFor[types.Lease]()
	out := make([]string, 0, t.NumField())
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			out = append(out, name)
		}
	}
	return out
}

// ledgerMergeApplies reports whether a put naming key changes the row. Several values are
// tried because the merge is typed: a list, a boolean, a string that is also a valid
// state, and a rendered run line cover every shape it accepts, and a key it ignores leaves
// the row untouched under all four.
func ledgerMergeApplies(key string) bool {
	for _, value := range []any{"declared", "magus run test .", []any{"x"}, true} {
		apply, err := ledger.ParseMerge(map[string]any{key: value})
		if err != nil {
			continue
		}
		var row types.Lease
		apply(&row)
		if !reflect.DeepEqual(row, types.Lease{}) {
			return true
		}
	}
	return false
}

// TestMagusToolCallMatchesOnlyMagusTools: the match used to be a SUFFIX, so any other
// server's tool whose name happened to end in one of magus's decoded as a magus call, was
// rendered into magus's activity trail, and could be denied by magus's rules.
func TestMagusToolCallMatchesOnlyMagusTools(t *testing.T) {
	assert.Equal(t, "magus_ledger", magusToolCall("mcp__magus__magus_ledger"))
	assert.Equal(t, "magus_ledger", magusToolCall("magus_ledger"), "a host that does not prefix is still talking to magus")

	for _, name := range []string{
		"mcp__other__magus_ledger",
		"mcp__mcp__magus__magus_ledger",
		"filesystem__write_file",
		"mcp__magus__magus_nonexistent",
		"",
	} {
		assert.Empty(t, magusToolCall(name), "%q is not a call to magus", name)
	}
}

// TestEveryMagusToolRendersSomethingJudgeable is the structural half of the MCP surface:
// the decode arm keys on the TOOL NAME, so every tool magus declares has to render to a
// line the rules can read. Requiring an `op` left nineteen of the twenty-one reaching no
// rule at all while the coverage declaration said deny=model.
func TestEveryMagusToolRendersSomethingJudgeable(t *testing.T) {
	for _, tool := range hint.AllToolNames {
		rendered := renderMCPCall(tool.String(), map[string]any{"op": "list"})
		require.NotEmpty(t, rendered, "%s renders nothing", tool)

		cmds, ok := parseGuardCommands(rendered)
		require.True(t, ok, "%s renders %q, which does not parse", tool, rendered)
		require.Len(t, cmds, 1, "%s renders %q, which is not one command", tool, rendered)

		if _, hasCLI := mcpCLIEquivalents[tool]; hasCLI || tool == hint.ToolMemory {
			assert.Equal(t, "magus", path.Base(cmds[0].Name),
				"%s has a CLI equivalent, so it must render as that argv: %q", tool, rendered)
			continue
		}
		assert.Equal(t, tool.String(), cmds[0].Name,
			"%s has no CLI equivalent, so it renders its own name and is recorded rather than judged", tool)
	}
}

// TestRenderMCPCallSpellsTheWorkTheToolDoes pins the renderings a rule keys on, so a
// report about the gate cannot render as a run of it.
func TestRenderMCPCallSpellsTheWorkTheToolDoes(t *testing.T) {
	for name, tc := range map[string]struct {
		tool  hint.ToolName
		input map[string]any
		want  string
	}{
		"a target run":     {hint.ToolRunTarget, map[string]any{"target": "ci", "projects": "."}, "magus run ci ."},
		"an affected run":  {hint.ToolRunAffected, map[string]any{"target": "ci"}, "magus affected ci"},
		"a shard plan":     {hint.ToolAffectedPlan, map[string]any{"target": "ci"}, "magus affected --plan ci"},
		"a checkpoint":     {hint.ToolVCSCheckpoint, nil, "magus vcs checkpoint"},
		"a memory put":     {hint.ToolMemory, map[string]any{"op": "put", "name": "a-decision"}, "magus memory put a-decision"},
		"a memory read":    {hint.ToolMemory, map[string]any{"op": "list"}, "magus memory ls"},
		"a graph query":    {hint.ToolQuery, map[string]any{"query": "guard rules"}, `magus query "guard rules"`},
		"no CLI door":      {hint.ToolInsight, map[string]any{"lens": "hotspots"}, "magus_insight"},
		"the ledger tool":  {hint.ToolLedger, map[string]any{"op": "put", "id": "a/b"}, "magus_ledger op=put id=a/b"},
		"an elided value":  {hint.ToolLedger, map[string]any{"op": "put", "goal": "ship the thing"}, "magus_ledger op=put goal=..."},
		"a missing target": {hint.ToolRunAffected, map[string]any{}, "magus affected"},
	} {
		assert.Equal(t, tc.want, renderMCPCall(tc.tool.String(), tc.input), name)
	}
}

// TestHookCmdDeniesTheGateThroughTheMCPDoor is the hole the tool-name decode closes: the
// same work the lease-scoped gate rule refuses on the command surface, asked for through
// the tool that does it. It passed unjudged while the coverage line said deny=model.
func TestHookCmdDeniesTheGateThroughTheMCPDoor(t *testing.T) {
	global = globalFlags{}
	t.Setenv(trail.EnvBaggage, "")
	lease := narrowLease()
	ctx, _ := fleetFixture(t, lease)

	for name, toolCall := range map[string]string{
		"the affected gate": `"tool_name":"mcp__magus__magus_run_affected","tool_input":{"target":"ci"}`,
		"the gate target":   `"tool_name":"mcp__magus__magus_run_target","tool_input":{"target":"ci","projects":"."}`,
	} {
		envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-gate","` + toolCall[1:] + `}`
		var out bytes.Buffer
		err := hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"})
		require.Error(t, err, name)
		assert.Equal(t, "deny\n", out.String(), name)
	}

	// The report about the gate is not a run of it, so it still passes.
	envelope := `{"hook_event_name":"PreToolUse","session_id":"mcp-plan",` +
		`"tool_name":"mcp__magus__magus_affected_plan","tool_input":{"target":"ci"}}`
	var out bytes.Buffer
	require.NoError(t, hookCmd(ctx, strings.NewReader(envelope), &out, []string{"--lease", lease.ID, "-o", "name"}))
	assert.Equal(t, "pass\n", out.String())
}
