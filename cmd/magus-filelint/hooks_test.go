package main

import (
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hookConfigs is both shipped configs, each wiring command on PreToolUse.
func hookConfigs(command string) fstest.MapFS {
	body := []byte(`{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Bash", "hooks": [{"type": "command", "command": "` + command + `"}]}
    ]
  }
}
`)
	return fstest.MapFS{
		shippedHookConfigs["claude-code"]: {Data: body},
		shippedHookConfigs["codex"]:       {Data: []byte(`{"hooks": {}}`)},
	}
}

func TestBuzzGlueIsWiredAsPlainArgv(t *testing.T) {
	assert.Empty(t, buzzGlueIsWiredAsPlainArgv(repoFS(t, shippedHookConfigs["claude-code"], shippedHookConfigs["codex"])))
	for _, command := range []string{
		"magus buzz -s docs/guides/integrations/agents/magus-command.buzz -- --advise",
		"MAGUS_GUARD_ADVISE=1 sh docs/guides/integrations/agents/magus-command.sh",
	} {
		assert.Empty(t, buzzGlueIsWiredAsPlainArgv(hookConfigs(command)), command)
	}

	got := buzzGlueIsWiredAsPlainArgv(hookConfigs("MAGUS_GUARD_ADVISE=1 magus buzz -s docs/guides/integrations/agents/magus-command.buzz"))
	require.Len(t, got, 1)
	assert.Equal(t, ".claude/settings.json", got[0].path)
	assert.Equal(t, 4, got[0].line)
	assert.Equal(t, `wires the Buzz glue on PreToolUse "Bash" behind a MAGUS_GUARD_ADVISE= prefix`, got[0].problem)
}

func TestIsEnvName(t *testing.T) {
	for s, want := range map[string]bool{"": false, "A": true, "_x1": true, "1A": false, "A-B": false, "--flag": false} {
		assert.Equal(t, want, isEnvName(s), s)
	}
}
