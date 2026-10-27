package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/cmdline/exit"
)

func TestCallDirIsWhereTheCallLands(t *testing.T) {
	assert.Equal(t, "/w/b/internal", callDir(`{"cwd":"/w/a","tool_input":{"file_path":"/w/b/internal/x.go"}}`), "a write's file")
	assert.Equal(t, "/w/a/sub", callDir(`{"cwd":"/w/a","tool_input":{"file_path":"sub/x.go"}}`), "relative to the session")
	assert.Equal(t, "/w/b", callDir(`{"cwd":"/w/a","tool_input":{"command":"ls","workdir":"/w/b"}}`), "a shell tool's workdir")
	assert.Equal(t, "/w/a", callDir(`{"cwd":"/w/a","tool_input":{"command":"ls"}}`), "the session")
	assert.Empty(t, callDir("git status"), "a bare command names no place")
}

// checkout is a workspace holding an executable ./magus that prints say and exits code.
func checkout(t *testing.T, say string, code int) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	script := "#!/bin/sh\ncat >/dev/null\nprintf '%s' '" + say + "'\nexit " + string(rune('0'+code)) + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(script), 0o755))
	return root
}

func TestCheckoutBinaryIsAnotherBinaryOnly(t *testing.T) {
	root := checkout(t, "", 0)
	own := filepath.Join(root, "magus")
	assert.Equal(t, own, checkoutBinary(filepath.Join(root, "new", "dir"), "/usr/local/bin/magus"), "a directory not made yet")
	assert.Empty(t, checkoutBinary(root, own), "the running binary judges its own checkout")
	assert.Empty(t, checkoutBinary(t.TempDir(), "/usr/local/bin/magus"), "no workspace")
}

// A call landing in another checkout is judged by that checkout's ./magus, whose verdict
// and exit status are this call's; a call it already judged is not handed on again.
func TestDelegateToCheckoutHandsTheCallToItsOwnBinary(t *testing.T) {
	root := checkout(t, `{"decision":"deny"}`, 2)
	input := `{"cwd":"/elsewhere","tool_input":{"file_path":"` + filepath.Join(root, "x.go") + `"}}`
	var out, errOut bytes.Buffer
	handled, err := delegateToCheckout(context.Background(), input, &out, &errOut)
	assert.True(t, handled)
	assert.Equal(t, 2, exit.Code(err))
	assert.Equal(t, `{"decision":"deny"}`, out.String())

	t.Setenv(delegatedEnv, "/other/magus")
	handled, err = delegateToCheckout(context.Background(), input, &out, &errOut)
	assert.False(t, handled)
	assert.NoError(t, err)
}
