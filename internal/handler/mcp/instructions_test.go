package mcp

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/hint"
)

// wantServerInstructions is the exact block every MCP client reads at session
// start, pinned verbatim. serverInstructions composes it from the hint.Tool*
// constants; this golden copy is what makes that composition safe to change -
// a constant that renders differently from the prose it replaced fails here
// rather than reaching a client.
const wantServerInstructions = `You are connected to a magus workspace.
magus is a build orchestrator for multi-language monorepos.

Discover:
  magus_describe          - list spells, targets, projects, workspaces, or mcp_tools
  magus_where             - resolve a fuzzy project name to its absolute path
  magus_config_get        - view the resolved workspace config (read-only)

Run:
  magus_run_target        - run build/test/lint/format/generate/ci
  magus_run_affected      - run a target on only VCS-changed projects
  magus_affected_plan     - emit a CI shard plan for the affected set
  magus_affected_explain  - explain why a project is affected by VCS changes

Inspect:
  magus_doctor            - validate the workspace health
  magus_status            - inspect the live concurrency pool
  magus_tail_log          - retrieve the captured build log for a project
  magus_output            - fetch a target-output blob by its reference id
  magus_insight           - VCS history lenses (hotspots, ownership, trend)

Knowledge graph:
  magus_query             - search the target/spell/symbol graph
  magus_explain           - explain a single node and its relationships
  magus_path              - find a path between two graph nodes
  magus_refs              - list files that reference a symbol
  magus_stats             - summarize graph composition

Typical flow:
  Discover first: magus_describe (list spells/targets/projects/workspaces), magus_where (resolve a fuzzy project name to a path).
  Then act: magus_run_target / magus_run_affected; magus_affected_plan (CI shard plan), magus_affected_explain (why a project is affected).
  After a run: magus_output (fetch a target's captured output by its ref), magus_tail_log (latest cache log for a project).
  Understand the graph: magus_query (search) -> magus_explain (a node's edges and provenance) -> magus_path (shortest path); magus_refs (symbol defs and refs); magus_stats (graph shape).
  Health and meta: magus_status, magus_doctor, magus_config_get.

Config mutation is intentionally not exposed. Use the magus CLI for that.`

func TestServerInstructionsRenderUnchanged(t *testing.T) {
	t.Parallel()

	assert.Equal(t, wantServerInstructions, serverInstructions)
}

// TestServerInstructionsToolNamesResolve is the drift half: the golden above
// only pins what is there today, so a tool renamed on both sides would sail
// through it. Every magus_* token in the rendered block must also be a declared
// hint.ToolName bound to a real Registry entry.
func TestServerInstructionsToolNamesResolve(t *testing.T) {
	t.Parallel()

	declared := map[string]bool{}
	for _, tn := range hint.AllToolNames {
		declared[tn.String()] = true
	}
	registered := map[string]bool{}
	for _, d := range Registry {
		registered[d.Name] = true
	}

	tokens := toolTokenRe.FindAllString(serverInstructions, -1)
	assert.NotEmpty(t, tokens, "the instructions name no tools at all, so this test proves nothing")
	for _, tok := range tokens {
		assert.Truef(t, declared[tok], "instructions name %q, which is not a declared hint.ToolName", tok)
		assert.Truef(t, registered[tok], "instructions name %q, which is not a Registry[].Name", tok)
	}
}
