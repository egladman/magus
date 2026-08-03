package run

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecRedactsSecretsFromEveryCapturedPath is the regression for two leaks found in
// review AFTER the feature was reported working off a terminal-only check:
//
//   - Capture buffers stdout/stderr through a MultiWriter that never reached the tap,
//     and ExecResult is what a magusfile, MCP, and the output store all read.
//   - Quiet replaces the tap with io.Discard, so a captured-and-quiet exec had no
//     redaction on any path at all.
//
// Both variants run here because Quiet is the one that leaves zero other coverage.
func TestExecRedactsSecretsFromEveryCapturedPath(t *testing.T) {
	ctx := secret.ContextWithResolver(t.Context(), secret.New())
	t.Setenv("MAGUS_TEST_LEAK_TOKEN", "ghp_never_capture_me")
	_, err := secret.ResolverFromContext(ctx).Read(ctx, "MAGUS_TEST_LEAK_TOKEN")
	require.NoError(t, err)

	for _, quiet := range []bool{false, true} {
		res, err := Exec(ctx, "sh",
			[]string{"-c", "echo tok=ghp_never_capture_me; echo tok=ghp_never_capture_me >&2"},
			ExecOptions{Dir: ".", Capture: true, Quiet: quiet})
		require.NoError(t, err)

		assert.NotContains(t, res.Stdout, "ghp_never_capture_me", "quiet=%v stdout", quiet)
		assert.NotContains(t, res.Stderr, "ghp_never_capture_me", "quiet=%v stderr", quiet)
		assert.Contains(t, res.Stdout, "tok=***", "quiet=%v", quiet)

		// BuzzObject is the exact value handed back to a magusfile.
		assert.NotContains(t, res.BuzzObject()["stdout"].(string), "ghp_never_capture_me")
	}
}

// TestExecWithoutAResolverIsUnchanged pins that the redaction plumbing is inert on the
// overwhelmingly common path: a run that reads no secret at all.
func TestExecWithoutAResolverIsUnchanged(t *testing.T) {
	res, err := Exec(t.Context(), "sh", []string{"-c", "echo plain output"},
		ExecOptions{Dir: ".", Capture: true, Quiet: true})
	require.NoError(t, err)
	assert.Equal(t, "plain output", strings.TrimSpace(res.Stdout))
}

// TestExecCapturesOutputWithNoTrailingNewline is the regression for a corruption a
// redaction writer introduced: its held-back tail was flushed in a defer that ran AFTER
// the capture buffers were read, so anything past the last newline vanished. It hit every
// run, not only ones touching a secret - `printf` came back empty.
func TestExecCapturesOutputWithNoTrailingNewline(t *testing.T) {
	for _, withResolver := range []bool{false, true} {
		ctx := t.Context()
		if withResolver {
			ctx = secret.ContextWithResolver(ctx, secret.New())
		}
		res, err := Exec(ctx, "printf", []string{"v1.2.3"},
			ExecOptions{Dir: ".", Capture: true, Quiet: true})
		require.NoError(t, err)
		assert.Equal(t, "v1.2.3", res.Stdout, "resolver=%v: unterminated output must survive", withResolver)
	}
}
