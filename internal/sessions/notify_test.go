package sessions

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// captureWarnings installs a slog handler for the duration of fn and returns what it
// logged. A producer reports a request it could not use through slog rather than an
// error (a non-zero exit from an agent hook interrupts the very session the notification
// exists to help), so the default logger is the only place to observe it.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

func TestClassifyOutcome(t *testing.T) {
	for raw, want := range map[string]types.EventOutcome{
		"Notification":       types.OutcomeWaiting,
		"SubagentStop":       types.OutcomeFinished,
		"permission-request": types.OutcomePermission,
		"build failed":       types.OutcomeFailed,
		"diagnostic":         types.OutcomeDiagnostic,
		"available update":   types.OutcomeUpdate,
		"":                   types.OutcomeOther,
		"something new":      types.OutcomeOther,
	} {
		t.Run(raw, func(t *testing.T) {
			assert.Equal(t, want, ClassifyOutcome(raw))
		})
	}
}

func TestEventFromStdin(t *testing.T) {
	canonical := `{"api_version":"magus.event/v1alpha1","outcome":"permission","severity":"critical","source":{"kind":"agent","sub":"host","id":"abc"},"message":"needs approval"}`
	got := EventFromStdin(t.Context(), []byte(canonical))
	assert.Equal(t, types.Event{
		APIVersion: types.EventAPIVersion,
		Outcome:    types.OutcomePermission,
		Severity:   types.SeverityCritical,
		Source:     types.EventSource{Kind: "agent", Sub: "host", ID: "abc"},
		Message:    "needs approval",
	}, got)

	assert.Equal(t, types.Event{Message: `{"outcome":"waiting"}`}, EventFromStdin(t.Context(), []byte(`{"outcome":"waiting"}`)))
	assert.Equal(t, types.Event{Message: "build failed"}, EventFromStdin(t.Context(), []byte("build failed")))
}

// A body that LOOKS like an envelope and fails still demotes to prose (the notification
// has to fire either way), but silently it was undiagnosable: the producer saw a working
// notification, no attention request, and nothing anywhere saying why.
func TestEventFromStdinWarnsOnAnUnusableEnvelope(t *testing.T) {
	for name, tc := range map[string]struct {
		body  string
		wants []string
	}{
		"missing every required field": {
			`{"severity":"warning"}`,
			[]string{"not a complete event envelope", "message", "outcome", "source.kind"},
		},
		"missing only the source": {
			`{"outcome":"waiting","message":"needs a decision"}`,
			[]string{"not a complete event envelope", "source.kind"},
		},
		"unparseable json object": {
			`{"outcome":"waiting",`,
			[]string{"did not parse"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			var ev types.Event
			logged := captureWarnings(t, func() { ev = EventFromStdin(t.Context(), []byte(tc.body)) })

			assert.Equal(t, tc.body, ev.Message, "the body is still delivered as prose")
			for _, want := range tc.wants {
				assert.Contains(t, logged, want)
			}
		})
	}
}

// Prose is the documented input, not a degraded envelope, so it must warn about nothing.
func TestEventFromStdinIsSilentForPlainText(t *testing.T) {
	logged := captureWarnings(t, func() { EventFromStdin(t.Context(), []byte("build failed")) })
	assert.Empty(t, logged)
}

func TestRenderNotificationAlwaysHasABody(t *testing.T) {
	title, body := RenderNotification(types.Event{Outcome: types.OutcomeWaiting})
	assert.NotEmpty(t, title)
	assert.NotEmpty(t, body)
}
