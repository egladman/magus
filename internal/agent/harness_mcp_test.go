package agent

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testMCPURL = "http://127.0.0.1:7391/mcp"

func stdioServer(hint string) *types.McpServer {
	return &types.McpServer{Transport: types.McpStdio, Command: []string{"./magus", "mcp"}, Hint: hint}
}

func TestHarnessMCPHintRendersGuidanceOnly(t *testing.T) {
	hint, err := harnessMCPHint(HarnessDescriptor{
		ID: "cursor",
		MCP: &types.McpServer{
			Transport: types.McpHTTP,
			TokenRef:  DefaultHarnessMCPTokenRef,
			URL:       testMCPURL,
			Hint:      "Wire Magus MCP in Cursor Settings -> Tools & MCP at __URL__; export __TOKEN_REF__ first. Magus does not write mcp.json.",
			Docs:      "docs/guides/integrations/mcp.md",
		},
	})
	require.NoError(t, err)
	assert.Contains(t, hint, testMCPURL)
	assert.Contains(t, hint, DefaultHarnessMCPTokenRef)
	assert.Contains(t, hint, "docs/guides/integrations/mcp.md")
	assert.NotContains(t, hint, "__TOKEN_REF__")
}

func TestHarnessMCPHintRegisterSketch(t *testing.T) {
	hint, err := harnessMCPHint(HarnessDescriptor{
		ID: "claude-code",
		MCP: &types.McpServer{
			Transport: types.McpHTTP,
			URL:       testMCPURL,
			Hint:      "Resolve __TOKEN_REF__, then run the host CLI.",
			Register:  []string{"claude", "mcp", "add", "--transport", "http", "magus", "__URL__"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, hint, "claude mcp add")
	assert.Contains(t, hint, testMCPURL)
	assert.Contains(t, hint, "command sketch (not executed)")
}

func TestHarnessMCPPlanRendersTheStdioCommandTheSpellNames(t *testing.T) {
	registerHarnessSpell(t, "stdio-host", `{
  "schema_version": 2,
  "id": "stdio-host",
  "display": {"name": "Stdio Host"},
  "skills": {"paths": [".agents/skills"], "form": "short"},
  "mcp": {
    "transport": "stdio",
    "command": ["./magus", "mcp"],
    "hint": "register __COMMAND__ with project scope",
    "register": ["host", "mcp", "add", "magus", "--", "./magus", "mcp"],
    "docs": "docs/guides/integrations/mcp.md"
  }
}`)
	plan, err := PlanHarness(context.Background(), t.TempDir(), "stdio-host")
	require.NoError(t, err)
	assert.Equal(t, types.HarnessPlan{
		ID:    "stdio-host",
		Wired: []types.HarnessWired{},
		MCPHint: "transport: stdio, ./magus mcp started per session in this checkout, so it serves this tree at this build\n" +
			"register ./magus mcp with project scope\n" +
			"command sketch (not executed): host mcp add magus -- ./magus mcp\n" +
			"docs: docs/guides/integrations/mcp.md",
	}, plan)
}

func TestHarnessMCPHintLeadsWithTheDeclaredTransport(t *testing.T) {
	hint, err := harnessMCPHint(HarnessDescriptor{ID: "remote", MCP: &types.McpServer{Transport: types.McpHTTP, URL: testMCPURL, Hint: "see docs"}})
	require.NoError(t, err)
	assert.Equal(t, "transport: http, "+testMCPURL+"; one shared server answers from the checkout and build that started it\nsee docs", hint)

	hint, err = harnessMCPHint(HarnessDescriptor{ID: "local", MCP: stdioServer("see docs")})
	require.NoError(t, err)
	assert.Equal(t, "transport: stdio, ./magus mcp started per session in this checkout, so it serves this tree at this build\nsee docs", hint)

	hint, err = harnessMCPHint(HarnessDescriptor{ID: "none"})
	require.NoError(t, err)
	assert.Empty(t, hint, "a harness that does not export mgs_getMcp declares no guidance")
}

// No transport is inferred: each one names what the host starts or reaches, and one that
// names neither is refused rather than read as a default.
func TestValidateHarnessMCPHoldsEachTransportToWhatItReaches(t *testing.T) {
	for name, tc := range map[string]struct {
		server types.McpServer
		want   string
	}{
		"stdio without a command": {types.McpServer{Transport: types.McpStdio, Hint: "h"}, "a stdio server names the command"},
		"http without a url":      {types.McpServer{Transport: types.McpHTTP, Hint: "h"}, "an http server names the url"},
		"no transport":            {types.McpServer{Command: []string{"./magus", "mcp"}, Hint: "h"}, "transport unset is none of stdio, http"},
	} {
		err := validateHarnessMCP(&tc.server)
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), tc.want, name)
	}
}

func TestValidateHarnessMCPRejectsEmbeddedToken(t *testing.T) {
	m := stdioServer("Authorization: Bearer mgs_deadbeef")
	m.TokenRef = DefaultHarnessMCPTokenRef
	err := validateHarnessMCP(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolved bearer token")
}

func TestValidateHarnessMCPRejectsTokenRefThatLooksLikeSecret(t *testing.T) {
	m := stdioServer("export __TOKEN_REF__")
	m.TokenRef = "mgs_deadbeef"
	err := validateHarnessMCP(m)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolved token")
}

func TestValidateHarnessMCPRejectsBareMgsInHint(t *testing.T) {
	require.Error(t, validateHarnessMCP(stdioServer("paste mgs_deadbeef into the header")))
}

func TestValidateHarnessMCPRequiresGuidance(t *testing.T) {
	err := validateHarnessMCP(stdioServer(""))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hint")
}

func TestVerifyHarnessMCPDoesNotInspectHost(t *testing.T) {
	result := HarnessVerification{}
	m := stdioServer("see docs")
	m.Docs = "docs/guides/integrations/mcp.md"
	verifyHarnessMCP(HarnessDescriptor{ID: "cursor", MCP: m}, &result)
	assert.Equal(t, HarnessVerified, result.MCP.Status)
	assert.Contains(t, result.MCP.Reason, "user owns")
}
