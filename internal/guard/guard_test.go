package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus"
	// Blank-imported so its init installs the spell registry's ensure hook, exactly as
	// cmd/magus/packs_interp.go does for the real binary: without it,
	// project.DefaultSpellRegistry().All() in testDeps below runs against a registry
	// nothing ever populated, and every raw-tool test would match against an empty
	// catalog. See internal/interp/bindings/spell.go's init.
	_ "github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testDeps resolves the workspace the way the CLI's hookDeps does, so a rule graded
// here reads the same tree the hook would.
func testDeps() Deps {
	return Deps{
		Inspect: func(ctx context.Context, root string) (types.WorkspaceRepository, error) {
			if root == "" {
				found, err := magus.FindRoot("")
				if err != nil {
					return nil, err
				}
				root = found
			}
			return magus.Inspect(ctx, root)
		},
		CacheDir: func(root string) (string, error) {
			if root == "" {
				found, err := magus.FindRoot("")
				if err != nil {
					return "", err
				}
				root = found
			}
			return magus.ResolveCacheDir(root)
		},
		Spells: project.DefaultSpellRegistry().All,
	}
}

// TestDecodeHookEnvelope_CommandStillWinsOverPrompt guards the ordering that makes the spawn
// branch purely additive: a payload carrying both is judged as the command it carries, so no
// envelope the guard already evaluated can start skipping the guard because a prompt appeared
// beside it.
func TestDecodeHookEnvelope_CommandStillWinsOverPrompt(t *testing.T) {
	req, ok := decodeHookEnvelope(`{"tool_input":{"command":"git stash","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "git stash", req.Value)
	assert.False(t, req.IsSpawn)

	req, ok = decodeHookEnvelope(`{"tool_input":{"file_path":"MAGUS.md","prompt":"delegate this"}}`)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath)
	assert.False(t, req.IsSpawn)
}

// TestDecodeHookEnvelope pins reading a host's hook payload directly. Without it, wiring
// the guard means `jq -r .tool_input.command | magus session hook`: an extra dependency on the
// critical path of every tool call, in the one place that must not fail.
func TestDecodeHookEnvelope(t *testing.T) {
	cmdPayload := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Bash",` +
		`"tool_input":{"command":"magus run ci | tail"}}`
	req, ok := decodeHookEnvelope(cmdPayload)
	require.True(t, ok)
	assert.Equal(t, "magus run ci | tail", req.Value)
	assert.False(t, req.IsPath)
	assert.Equal(t, "s1", req.Who.Session)
	assert.Equal(t, "PreToolUse", req.Who.Event)

	// A file_path payload is a WRITE, so the envelope decides the --path question too.
	writePayload := `{"hook_event_name":"PreToolUse","tool_input":{"file_path":"MAGUS.md"}}`
	req, ok = decodeHookEnvelope(writePayload)
	require.True(t, ok)
	assert.Equal(t, "MAGUS.md", req.Value)
	assert.True(t, req.IsPath, "a file_path payload must be judged as a path, not as a command")

	// Anything that is not a usable envelope is left alone for the bare-command form.
	for _, raw := range []string{
		"magus run ci | tail",           // a plain command
		`{"tool_input":{}}`,             // an envelope with nothing to judge
		`{not json`,                     // malformed
		`{"tool_input":{"command":""}}`, // explicitly empty
	} {
		_, ok := decodeHookEnvelope(raw)
		assert.False(t, ok, "must fall through to the literal form: %q", raw)
	}

	// TestHookCmd_EnvelopeWithNothingToJudge (cmd/magus/hook_test.go) exercises the same
	// payload end to end through hookCmd; this pins the decoder's own half of it, which that
	// test cannot reach directly since decodeHookEnvelope is unexported to this package.
	const nothingToJudge = `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"TodoWrite",` +
		`"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`
	req, ok = decodeHookEnvelope(nothingToJudge)
	require.True(t, ok, "a payload naming a host hook event is an envelope")
	assert.True(t, req.NothingToJudge)
	assert.Empty(t, req.Value)

	// The same JSON with no envelope marker on it is still judged as the text it is: only a
	// payload that identifies itself as a host hook may claim the exemption.
	_, ok = decodeHookEnvelope(`{"tool_input":{"todos":[{"content":"never use sed -i here"}]}}`)
	assert.False(t, ok)
}

// TestDecodeHookEnvelopeReadsEveryWritePathSpelling: the path surface used to see
// `file_path` alone, so a host tool naming its target anything else fell through to
// NothingToJudge and every write rule went unrun.
func TestDecodeHookEnvelopeReadsEveryWritePathSpelling(t *testing.T) {
	for name, payload := range map[string]string{
		"the documented spelling": `{"tool_name":"Write","tool_input":{"file_path":"MAGUS.md"}}`,
		"a notebook":              `{"tool_name":"NotebookEdit","tool_input":{"notebook_path":"MAGUS.md"}}`,
		"any other _path key":     `{"tool_name":"Other","tool_input":{"target_path":"MAGUS.md"}}`,
	} {
		req, ok := decodeHookEnvelope(payload)
		require.True(t, ok, name)
		assert.True(t, req.IsPath, name)
		assert.Equal(t, "MAGUS.md", req.Value, name)
	}

	// A field arriving with an unexpected type still reaches the MCP arm. Typed, it
	// failed the unmarshal outright and the raw JSON was judged as a shell line, which is
	// the one outcome the default arm of the decoder exists to prevent.
	req, ok := decodeHookEnvelope(`{"tool_name":"mcp__magus__magus_ledger","tool_input":{"op":123,"id":"a/b"}}`)
	require.True(t, ok)
	assert.Equal(t, "magus_ledger op=123 id=a/b", req.Value)
}
