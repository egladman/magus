package main

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withOutputFlags sets the package-level display flags for one test and restores
// them, since every command reads them off `global` rather than taking them as
// arguments.
func withOutputFlags(t *testing.T, format, tee string) {
	t.Helper()
	prevOut, prevTee := global.output, global.tee
	global.output, global.tee = format, tee
	t.Cleanup(func() { global.output, global.tee = prevOut, prevTee })
}

// The seed goes to a secret store over a pipe, so stdout must carry the seed and
// NOTHING else - no prose, and no trailing newline. `gh secret set` stores stdin
// verbatim, so one stray byte becomes part of the secret and the base64 decode on
// the other end fails at signing time, a long way from the cause.
func TestCacheKeyGenerateTemplateEmitsOnlyTheSeed(t *testing.T) {
	withOutputFlags(t, "template={{.seed}}", "")

	var err error
	out := captureStdout(t, func() {
		_ = captureStderr(t, func() { err = configCacheKeyGenerate(nil) })
	})
	require.NoError(t, err)

	assert.NotContains(t, out, "\n", "stdout must be the bare seed; a newline would be stored as part of the secret")
	assert.NotContains(t, out, "SECRET", "the warning banner belongs on stderr, not in the piped value")
	raw, decErr := base64.StdEncoding.DecodeString(out)
	require.NoError(t, decErr, "stdout must decode as the base64 seed exactly as emitted")
	assert.Len(t, raw, 32, "an Ed25519 seed is 32 bytes")
}

// The keyid and public key still have to reach a human who is piping the seed
// away unseen - they are what you paste into magus.yaml - so they move to stderr
// rather than being suppressed.
func TestCacheKeyGenerateTemplateKeepsPublicHalfOnStderr(t *testing.T) {
	withOutputFlags(t, "template={{.keyid}}", "")

	var err error
	errOut := captureStderr(t, func() {
		_ = captureStdout(t, func() { err = configCacheKeyGenerate(nil) })
	})
	require.NoError(t, err)

	assert.Contains(t, errOut, "keyid:")
	assert.Contains(t, errOut, "trusted_keys")
}

// --tee mirrors structured output into a FILE. Every other command wants that;
// here it would write a signing key to disk, which is the one thing this command
// is built never to do. Refused rather than ignored, so nobody believes they have
// a backup copy they do not have.
func TestCacheKeyGenerateRefusesTee(t *testing.T) {
	withOutputFlags(t, "json", "keys.json")

	err := configCacheKeyGenerate(nil)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not come to rest on disk")
}

// seedOf pulls the base64 seed back out of the rendered banner: it is the one line
// that decodes to 32 bytes.
func seedOf(t *testing.T, out string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if raw, err := base64.StdEncoding.DecodeString(line); err == nil && len(raw) == 32 {
			return line
		}
	}
	t.Fatal("no 32-byte base64 line in the output; the seed is not on a line of its own")
	return ""
}

// The default is a human who will read the seed once and paste it into a password
// manager, so the prose banner stays. Every assertion here is a property someone
// copying from a terminal depends on, not a spelling.
func TestCacheKeyGenerateDefaultStaysHumanReadable(t *testing.T) {
	withOutputFlags(t, "", "")

	var err error
	out := captureStdout(t, func() { err = configCacheKeyGenerate(nil) })
	require.NoError(t, err)

	assert.Contains(t, out, "keyid  ")
	// The seed sits BARE on its own line - not after "VAR=", which is awkward to
	// select and invites pasting it into an export, straight into shell history.
	assert.Contains(t, out, "\n\n"+seedOf(t, out)+"\n\n", "the seed must be alone on its line")
	assert.NotContains(t, out, signingKeyEnv+"=", "the seed must not be rendered as an assignment")
	// Column zero, or the snippet does not paste into magus.yaml.
	assert.Contains(t, out, "\ncache:\n  remote:\n    trusted_keys:\n")
	assert.Contains(t, out, "gh secret set "+signingKeyEnv, "the next command is spelled out")
	// Plain ASCII: the old layout drew a box, which this repo's own rule forbids in
	// user-facing strings and which renders badly in a plain terminal.
	for _, r := range out {
		assert.Less(t, r, rune(128), "user-facing output must be ASCII, found %q", r)
	}
}
