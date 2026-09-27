package agent

import (
	"testing"

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
