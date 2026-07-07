package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// frame encodes v as one LSP base-protocol frame (Content-Length header + body).
func frame(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// readFrames parses every Content-Length framed message out of the server output.
func readFrames(t *testing.T, out string) []map[string]any {
	t.Helper()
	r := bufio.NewReader(strings.NewReader(out))
	var msgs []map[string]any
	for {
		body, err := readMessage(r)
		if err != nil {
			break
		}
		var m map[string]any
		require.NoError(t, json.Unmarshal(body, &m))
		msgs = append(msgs, m)
	}
	return msgs
}

func TestLSPServerSession(t *testing.T) {
	src := "import \"fs\";\nfs.glob(\"x\");\n"
	uri := "file:///w/magusfile.buzz"

	var in strings.Builder
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{}}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "initialized", "params": map[string]any{}}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": src},
	}}))
	// completion right after "fs." on line 1.
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/completion", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 3},
	}}))
	// hover on the "fs" module token on line 1.
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "textDocument/hover", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 1, "character": 1},
	}}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 4, "method": "shutdown"}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "exit"}))

	var out bytes.Buffer
	srv := &lspServer{docs: map[string]string{}}
	require.NoError(t, srv.serve(strings.NewReader(in.String()), &out))

	msgs := readFrames(t, out.String())
	require.Len(t, msgs, 4, "one response per request (notifications produce none)")

	// initialize: capabilities advertised.
	caps, ok := msgs[0]["result"].(map[string]any)["capabilities"].(map[string]any)
	require.True(t, ok, "initialize result has capabilities")
	assert.Equal(t, true, caps["hoverProvider"])
	assert.NotNil(t, caps["completionProvider"])

	// completion: fs.* members returned.
	comp := msgs[1]["result"].(map[string]any)
	items := comp["items"].([]any)
	assert.NotEmpty(t, items, "fs. yields member completions")
	labels := map[string]bool{}
	for _, it := range items {
		labels[it.(map[string]any)["label"].(string)] = true
	}
	assert.True(t, labels["glob"], "fs completions include glob; got %v", labels)

	// hover: on the fs module token.
	hov, ok := msgs[2]["result"].(map[string]any)
	require.True(t, ok, "hover returns contents")
	value := hov["contents"].(map[string]any)["value"].(string)
	assert.Contains(t, value, "fs", "hover mentions the fs module")

	// shutdown: null result.
	assert.Nil(t, msgs[3]["result"])
}

func TestLSPCompletionReplaceRange(t *testing.T) {
	// A partial builtin token carries a Replace, which must become a textEdit whose
	// range covers the already-typed prefix so accepting overwrites it.
	uri := "file:///w/m.buzz"
	var in strings.Builder
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 1, "method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri, "text": "pri"},
	}}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "textDocument/completion", "params": map[string]any{
		"textDocument": map[string]any{"uri": uri},
		"position":     map[string]any{"line": 0, "character": 3},
	}}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "id": 3, "method": "shutdown"}))
	in.WriteString(frame(t, map[string]any{"jsonrpc": "2.0", "method": "exit"}))

	var out bytes.Buffer
	srv := &lspServer{docs: map[string]string{}}
	require.NoError(t, srv.serve(strings.NewReader(in.String()), &out))

	// didOpen carries no reply; completion and shutdown each do.
	msgs := readFrames(t, out.String())
	require.Len(t, msgs, 2)
	items := msgs[0]["result"].(map[string]any)["items"].([]any)
	require.NotEmpty(t, items)
	edit := items[0].(map[string]any)["textEdit"].(map[string]any)
	rng := edit["range"].(map[string]any)
	start := rng["start"].(map[string]any)
	end := rng["end"].(map[string]any)
	assert.EqualValues(t, 0, start["character"], "replace range starts at column 0")
	assert.EqualValues(t, 3, end["character"], "replace range ends at the cursor")
}

func TestPositionOffsetRoundTrip(t *testing.T) {
	text := "ab\ncde\nf"
	cases := []struct{ line, char int }{{0, 0}, {0, 2}, {1, 1}, {2, 1}}
	for _, c := range cases {
		off := positionToOffset(text, c.line, c.char)
		gl, gc := offsetToPosition(text, off)
		assert.Equal(t, c.line, gl, "line round-trips for %+v", c)
		assert.Equal(t, c.char, gc, "char round-trips for %+v", c)
	}
}
