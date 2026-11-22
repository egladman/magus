package guard

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// reportedMainTranscript is the hook's transcript pointer for tests that need a
// reported Claude Code main thread.
func reportedMainTranscript(t testing.TB, session string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	path := filepath.Join(home, "projects", "guard-fixture", session+".jsonl")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	row := fmt.Sprintf(`{"type":"user","sessionId":%q,"isSidechain":false}`, session)
	require.NoError(t, os.WriteFile(path, []byte(row+"\n"), 0o600))
	return path
}

func fixtureMainTranscript(session string) string {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, "projects", "guard-fixture", session+".jsonl")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func TestTranscriptCallerRequiresTheCurrentSession(t *testing.T) {
	main := hookAttribution{Host: "claude-code", Session: "one", Transcript: reportedMainTranscript(t, "one")}
	other := main
	other.Session = "two"
	role, owner := transcriptCaller(other, nil)
	require.Equal(t, types.AgentThreadUnknown, role)
	require.Empty(t, owner)
	unsupported := main
	unsupported.Host = "undeclared"
	role, owner = transcriptCaller(unsupported, nil)
	require.Equal(t, types.AgentThreadUnknown, role)
	require.Empty(t, owner)
	missing := main
	missing.Transcript = filepath.Join(filepath.Dir(main.Transcript), "missing.jsonl")
	role, owner = transcriptCaller(missing, nil)
	require.Equal(t, types.AgentThreadUnknown, role)
	require.Empty(t, owner)

	for _, row := range []string{
		`{"type":"assistant","sessionId":"one"}`,
		`{"type":"assistant","sessionId":"one","isSidechain":true}`,
		`{"type":"assistant","sessionId":"two","isSidechain":false}`,
	} {
		require.NoError(t, os.WriteFile(main.Transcript, []byte(row+"\n"), 0o600))
		role, owner = transcriptCaller(main, nil)
		require.Equal(t, types.AgentThreadUnknown, role)
		require.Empty(t, owner)
	}
}
