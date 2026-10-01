package agent

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHarnessMCPHintRendersGuidanceOnly(t *testing.T) {
	hint, err := harnessMCPHint(HarnessDescriptor{
		ID: "cursor",
		MCP: &HarnessMCP{
			TokenRef: DefaultHarnessMCPTokenRef,
			URL:      DefaultHarnessMCPURL,
			Hint:     "Wire Magus MCP in Cursor Settings -> Tools & MCP at __URL__; export __TOKEN_REF__ first. Magus does not write mcp.json.",
			Docs:     "docs/guides/integrations/mcp.md",
		},
	})
	require.NoError(t, err)
	assert.Contains(t, hint, DefaultHarnessMCPURL)
	assert.Contains(t, hint, DefaultHarnessMCPTokenRef)
	assert.Contains(t, hint, "docs/guides/integrations/mcp.md")
	assert.NotContains(t, hint, "__TOKEN_REF__")
}

func TestHarnessMCPHintRegisterSketch(t *testing.T) {
	hint, err := harnessMCPHint(HarnessDescriptor{
		ID: "claude-code",
		MCP: &HarnessMCP{
			TokenRef: DefaultHarnessMCPTokenRef,
			URL:      DefaultHarnessMCPURL,
			Hint:     "Resolve __TOKEN_REF__, then run the host CLI.",
			Register: []string{"claude", "mcp", "add", "--transport", "http", "magus", "__URL__"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, hint, "claude mcp add")
	assert.Contains(t, hint, DefaultHarnessMCPURL)
	assert.Contains(t, hint, "command sketch (not executed)")
}

func TestHarnessMCPPlanDefaultsToStdioInTheCheckout(t *testing.T) {
	registerHarnessSpell(t, "stdio-host", `{
  "schema_version": 2,
  "id": "stdio-host",
  "display": {"name": "Stdio Host"},
  "skills": {"paths": [".agents/skills"], "form": "short"},
  "mcp": {
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

func TestHarnessMCPTransportIsHTTPOnlyWhenTheSpellNamesAURL(t *testing.T) {
	assert.Equal(t, HarnessMCPStdio, (*HarnessMCP)(nil).Transport())
	assert.Equal(t, HarnessMCPStdio, (&HarnessMCP{Hint: "see docs"}).Transport())
	assert.Equal(t, HarnessMCPHTTP, (&HarnessMCP{URL: DefaultHarnessMCPURL}).Transport())

	hint, err := harnessMCPHint(HarnessDescriptor{ID: "remote", MCP: &HarnessMCP{URL: DefaultHarnessMCPURL, Hint: "see docs"}})
	require.NoError(t, err)
	assert.Equal(t, "transport: http, "+DefaultHarnessMCPURL+"; one shared server answers from the checkout and build that started it\nsee docs", hint)
}

func TestValidateHarnessMCPRejectsEmbeddedToken(t *testing.T) {
	err := validateHarnessMCP(&HarnessMCP{
		TokenRef: DefaultHarnessMCPTokenRef,
		Hint:     "Authorization: Bearer mgs_deadbeef",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolved bearer token")
}

func TestValidateHarnessMCPRejectsTokenRefThatLooksLikeSecret(t *testing.T) {
	err := validateHarnessMCP(&HarnessMCP{
		TokenRef: "mgs_deadbeef",
		Hint:     "export __TOKEN_REF__",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resolved token")
}

func TestValidateHarnessMCPRejectsBareMgsInHint(t *testing.T) {
	err := validateHarnessMCP(&HarnessMCP{
		TokenRef: DefaultHarnessMCPTokenRef,
		Hint:     "paste mgs_deadbeef into the header",
	})
	require.Error(t, err)
}

func TestValidateHarnessMCPRequiresGuidance(t *testing.T) {
	err := validateHarnessMCP(&HarnessMCP{TokenRef: DefaultHarnessMCPTokenRef})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "hint")
}

func TestVerifyHarnessMCPDoesNotInspectHost(t *testing.T) {
	result := HarnessVerification{}
	verifyHarnessMCP(HarnessDescriptor{
		ID: "cursor",
		MCP: &HarnessMCP{
			Hint: "see docs",
			Docs: "docs/guides/integrations/mcp.md",
		},
	}, &result)
	assert.Equal(t, HarnessVerified, result.MCPStatus)
	assert.Contains(t, result.MCPReason, "user owns")
}
