package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClassifyAttention pins the host-vocabulary mapping.
//
// The table is substring-and-case-insensitive on purpose: every host names these
// events differently and renames them between releases, so an exact-match switch
// would be wrong within a month. The important property is the LAST case - an
// unrecognized kind still classifies and still notifies, because the failure this
// command exists to prevent is a human never being told.
func TestClassifyAttention(t *testing.T) {
	for raw, want := range map[string]string{
		"Notification":         "waiting",
		"notification":         "waiting",
		"Stop":                 "stopped",
		"SubagentStop":         "stopped",
		"permission-request":   "permission",
		"needs approval":       "permission",
		"awaiting-input":       "waiting",
		"idle":                 "waiting",
		"agent.finished":       "stopped",
		"":                     "other",
		"something-unheard-of": "other",
	} {
		t.Run(raw, func(t *testing.T) {
			assert.Equal(t, want, classifyAttention(raw))
		})
	}
}

// TestAgentNotifyCmd covers the three input forms and the fail-open contract.
func TestAgentNotifyCmd(t *testing.T) {
	run := func(stdin string, args ...string) string {
		var out bytes.Buffer
		global = globalFlags{}
		require.NoError(t, agentNotifyCmd(context.Background(), strings.NewReader(stdin), &out, args))
		return out.String()
	}

	assert.Contains(t, run("", "--kind", "Notification", "needs you"), "waiting for you")
	assert.Contains(t, run("", "--kind", "Notification", "needs you"), "needs you")
	assert.Equal(t, "stopped\n", run("", "--kind", "Stop", "-o", "name"))
	assert.Equal(t, "permission\n", run("", "--kind", "permission-request", "-o", "name"))

	event := `{"hook_event_name":"Notification","message":"waiting on you","session_id":"abc123"}`
	got := run(event, "--kind-from-json", "hook_event_name", "--from-json", "message", "-o", "json")
	assert.Contains(t, got, `"kind": "waiting"`)
	assert.Contains(t, got, `"session": "abc123"`)
	assert.Contains(t, got, `waiting on you`)

	// FAIL OPEN. A notifier that errors on junk input becomes a hook that breaks
	// the session it was meant to watch, so unreadable input still produces a
	// record rather than an error.
	assert.Equal(t, "other\n", run("not json at all", "--from-json", "message", "-o", "name"))
	assert.Contains(t, run("", "--kind", "Notification", "-o", "name"), "waiting")
}

// TestRenderAttentionAlwaysHasABody guards the notifier contract: macOS silently
// drops a notification with an empty body, so a host that supplies no message
// must still produce one.
func TestRenderAttentionAlwaysHasABody(t *testing.T) {
	title, body := renderAttention(attentionEvent{Kind: "waiting"})
	assert.NotEmpty(t, title)
	assert.NotEmpty(t, body)
}

// TestOsaQuoteEscapes covers the AppleScript injection seam: a message with a
// quote or a backslash must not break out of the script literal.
func TestOsaQuoteEscapes(t *testing.T) {
	assert.Equal(t, `"plain"`, osaQuote("plain"))
	assert.Equal(t, `"say \"hi\""`, osaQuote(`say "hi"`))
	assert.Equal(t, `"back\\slash"`, osaQuote(`back\slash`))
}
