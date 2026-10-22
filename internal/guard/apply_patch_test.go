package guard

import (
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const multiFilePatch = "*** Begin Patch\n" +
	"*** Add File: internal/guard/new.go\n" +
	"+package guard\n" +
	"+\n" +
	"+const x = 1\n" +
	"*** Update File: internal/guard/old.go\n" +
	"*** Move to: internal/guard/renamed.go\n" +
	"@@ func f()\n" +
	"-\treturn 1\n" +
	"+\treturn 2\n" +
	"*** Delete File: internal/guard/gone.go\n" +
	"*** End Patch\n"

func TestApplyPatchWritePaths(t *testing.T) {
	t.Parallel()

	t.Run("every section is a path, a move names both ends", func(t *testing.T) {
		assert.Equal(t, []patchFile{
			{Path: "internal/guard/new.go", Op: patchOpAdd, Added: []string{"package guard", "", "const x = 1"}},
			{Path: "internal/guard/old.go", Op: patchOpUpdate},
			{Path: "internal/guard/renamed.go", Op: patchOpMove},
			{Path: "internal/guard/gone.go", Op: patchOpDelete},
		}, parsePatch(multiFilePatch))
	})
	t.Run("a path named twice counts once", func(t *testing.T) {
		files := parsePatch("*** Begin Patch\n*** Update File: a.go\n*** Update File: a.go\n*** End Patch\n")
		assert.Equal(t, []patchFile{{Path: "a.go", Op: patchOpUpdate}}, files)
	})
	t.Run("CRLF line ends", func(t *testing.T) {
		files := parsePatch("*** Begin Patch\r\n*** Add File: a.txt\r\n+one\r\n*** End Patch\r\n")
		assert.Equal(t, []patchFile{{Path: "a.txt", Op: patchOpAdd, Added: []string{"one"}}}, files)
		assert.Equal(t, "one\n", patchContent(files))
	})
	t.Run("only a patch is a patch", func(t *testing.T) {
		for _, input := range []map[string]any{
			{"command": "git status"},
			{"command": "echo '*** Begin Patch'"},
			{"patchText": "no marker"},
		} {
			files, ok := envelopePatch(input)
			assert.False(t, ok, input)
			assert.Nil(t, files, input)
		}
		files, ok := envelopePatch(map[string]any{"patchText": "*** Begin Patch\n*** End Patch\n"})
		assert.True(t, ok, "a patch that names no file is still a patch")
		assert.Empty(t, files)
	})
	t.Run("content is read only from a patch that creates one file", func(t *testing.T) {
		assert.Equal(t, "package guard\n\nconst x = 1\n", patchContent(parsePatch(
			"*** Begin Patch\n*** Add File: new.go\n+package guard\n+\n+const x = 1\n*** End Patch\n")))
		assert.Empty(t, patchContent(parsePatch(multiFilePatch)))
		assert.Empty(t, patchContent(parsePatch("*** Begin Patch\n*** Update File: a.go\n+x\n*** End Patch\n")))
	})
}

func TestDecodeHookEnvelopeApplyPatch(t *testing.T) {
	t.Parallel()

	// Codex sends the patch as tool_input.command; OpenCode, on a gpt model, as patchText.
	for name, field := range map[string]string{"command": "command", "patchText": "patchText"} {
		t.Run(name, func(t *testing.T) {
			body, err := json.Marshal(map[string]any{
				"session_id": "019e4c21", "cwd": "/Users/dev/repo", "hook_event_name": "PreToolUse",
				"tool_name": "apply_patch", "tool_input": map[string]any{field: multiFilePatch},
			})
			require.NoError(t, err)
			req, ok := decodeTestEnvelope(t, string(body))
			require.True(t, ok)
			assert.True(t, req.IsPath, "a patch is a write, not a shell line")
			assert.Equal(t, "internal/guard/new.go", req.Value)
			assert.Empty(t, req.Write.Content, "a multi-file patch carries no single file's content")
			assert.False(t, req.NothingToJudge)
		})
	}

	t.Run("a patch that creates one file hands its text to the content rules", func(t *testing.T) {
		req, ok := decodeTestEnvelope(t, `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"apply_patch",`+
			`"tool_input":{"command":"*** Begin Patch\n*** Add File: hello.txt\n+hello\n*** End Patch\n"}}`)
		require.True(t, ok)
		assert.True(t, req.IsPath)
		assert.Equal(t, "hello.txt", req.Value)
		assert.Equal(t, "hello\n", req.Write.Content)
	})
	t.Run("a patch naming no file has nothing to judge", func(t *testing.T) {
		req, ok := decodeTestEnvelope(t, `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"apply_patch",`+
			`"tool_input":{"command":"*** Begin Patch\n*** End Patch\n"}}`)
		require.True(t, ok)
		assert.True(t, req.NothingToJudge)
		assert.False(t, req.IsPath)
	})
	t.Run("a denied command quoted in the patch is not judged as a shell line", func(t *testing.T) {
		req, ok := decodeTestEnvelope(t, `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"apply_patch",`+
			`"tool_input":{"command":"*** Begin Patch\n*** Add File: notes.md\n+never run git stash\n*** End Patch\n"}}`)
		require.True(t, ok)
		assert.True(t, req.IsPath)
		assert.Equal(t, "notes.md", req.Value)
	})
	t.Run("envelopeWritePath answers with the first file", func(t *testing.T) {
		assert.Equal(t, "internal/guard/new.go", envelopeWritePath(map[string]any{"patchText": multiFilePatch}))
	})
}

func TestPatchMarkerInBashIsShell(t *testing.T) {
	t.Parallel()
	line := "*** Begin Patch\ngit push origin main"
	body, err := json.Marshal(map[string]any{
		"session_id": "s", "cwd": "/Users/dev/repo", "hook_event_name": "PreToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"command": line},
	})
	require.NoError(t, err)
	req, ok := decodeTestEnvelope(t, string(body))
	require.True(t, ok)
	assert.Equal(t, hookRequest{
		Cwd:   "/Users/dev/repo",
		Who:   hookAttribution{Session: "s", Event: "PreToolUse"},
		Value: line,
	}, req, "only a tool whose row says it writes carries a patch")
}
