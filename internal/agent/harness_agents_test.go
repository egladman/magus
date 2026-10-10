package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const agentFileBody = "---\ndescription: \"x\"\n---\n\nbody\n"

func writeAgentHarness(t *testing.T) {
	t.Helper()
	registerHarnessSpell(t, "scribe", `{
  "schema_version": 2,
  "id": "scribe",
  "display": {"name": "Scribe"},
  "skills": {"paths": [".agents/skills"], "form": "both"},
  "agents": [
    {"path": ".scribe/agents/magus-scout.md", "content": `+strconvQuote(agentFileBody)+`}
  ]
}`)
}

// TestPlanHarnessWritesAgentFilesOnceAndThenNothing pins that a subagent file is a whole file
// the descriptor owns: written when the bytes differ, left alone when they match.
func TestPlanHarnessWritesAgentFilesOnceAndThenNothing(t *testing.T) {
	root := t.TempDir()
	writeAgentHarness(t)

	before, err := VerifyHarness(untimedProbes(), root, "scribe")
	require.NoError(t, err)
	assert.Equal(t, HarnessUncovered, before.AgentStatus)
	assert.Contains(t, before.AgentReason, ".scribe/agents/magus-scout.md")

	plan := mergeHarness(t, root, "scribe")
	assert.Equal(t, map[string]types.HarnessFile{
		".scribe/agents/magus-scout.md": {Content: agentFileBody, Changes: []types.HarnessChange{{Op: types.HarnessWrite}}},
	}, plan.Files)

	written, err := os.ReadFile(filepath.Join(root, ".scribe/agents/magus-scout.md"))
	require.NoError(t, err)
	assert.Equal(t, agentFileBody, string(written))
	requireCurrent(t, root, "scribe")

	after, err := VerifyHarness(untimedProbes(), root, "scribe")
	require.NoError(t, err)
	assert.Equal(t, HarnessVerified, after.AgentStatus)
	assert.Empty(t, after.AgentReason)
}

// TestPlanHarnessRewritesAnEditedAgentFile pins that the descriptor owns the whole file, so a
// local edit is replaced rather than merged.
func TestPlanHarnessRewritesAnEditedAgentFile(t *testing.T) {
	root := t.TempDir()
	writeAgentHarness(t)
	mergeHarness(t, root, "scribe")
	path := filepath.Join(root, ".scribe/agents/magus-scout.md")
	require.NoError(t, os.WriteFile(path, []byte("edited\n"), 0o644))

	plan, err := PlanHarness(context.Background(), root, "scribe")
	require.NoError(t, err)
	file := plan.Files[".scribe/agents/magus-scout.md"]
	assert.True(t, file.Exists)
	assert.Equal(t, agentFileBody, file.Content)
}

func TestHarnessDescriptorWithoutAgentsPlansAndVerifiesNone(t *testing.T) {
	root := t.TempDir()
	registerHarnessSpell(t, "plain", `{
  "schema_version": 2,
  "id": "plain",
  "display": {"name": "Plain"},
  "skills": {"paths": [".agents/skills"], "form": "both"}
}`)
	requireCurrent(t, root, "plain")
	result, err := VerifyHarness(untimedProbes(), root, "plain")
	require.NoError(t, err)
	assert.Empty(t, result.AgentStatus)
}

func TestHarnessDescriptorRejectsAMalformedAgentFile(t *testing.T) {
	base := HarnessDescriptor{SchemaVersion: harnessSchemaVersion, ID: "p", Display: HarnessDisplay{Name: "P"}, Skills: HarnessSkills{Form: "both"}}
	for name, file := range map[string]HarnessAgentFile{
		"no path":      {Content: "x"},
		"absolute":     {Path: "/etc/agents/a.md", Content: "x"},
		"escapes root": {Path: "../a.md", Content: "x"},
		"no content":   {Path: ".a/agents/a.md"},
	} {
		t.Run(name, func(t *testing.T) {
			d := base
			d.Agents = []HarnessAgentFile{file}
			assert.Error(t, validateHarnessDescriptor(d))
		})
	}
	ok := base
	ok.Agents = []HarnessAgentFile{{Path: ".a/agents/a.md", Content: "x"}}
	assert.NoError(t, validateHarnessDescriptor(ok))
}
