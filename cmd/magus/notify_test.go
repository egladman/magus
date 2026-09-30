package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			assert.Equal(t, want, classifyOutcome(raw))
		})
	}
}

func TestEventFromStdin(t *testing.T) {
	canonical := `{"schema_version":1,"outcome":"permission","severity":"critical","source":{"kind":"agent","sub":"host","id":"abc"},"message":"needs approval"}`
	got := eventFromStdin([]byte(canonical))
	assert.Equal(t, types.Event{
		SchemaVersion: 1,
		Outcome:       types.OutcomePermission,
		Severity:      types.SeverityCritical,
		Source:        types.EventSource{Kind: "agent", Sub: "host", ID: "abc"},
		Message:       "needs approval",
	}, got)

	assert.Equal(t, types.Event{Message: `{"outcome":"waiting"}`}, eventFromStdin([]byte(`{"outcome":"waiting"}`)))
	assert.Equal(t, types.Event{Message: "build failed"}, eventFromStdin([]byte("build failed")))
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
			logged := captureWarnings(t, func() { ev = eventFromStdin([]byte(tc.body)) })

			assert.Equal(t, tc.body, ev.Message, "the body is still delivered as prose")
			for _, want := range tc.wants {
				assert.Contains(t, logged, want)
			}
		})
	}
}

// Prose is the documented input, not a degraded envelope, so it must warn about nothing.
func TestEventFromStdinIsSilentForPlainText(t *testing.T) {
	logged := captureWarnings(t, func() { eventFromStdin([]byte("build failed")) })
	assert.Empty(t, logged)
}

func TestNotifyCmd(t *testing.T) {
	// A waiting or permission event opens a real attention request, so the store is
	// redirected here; without it these subtests would file requests into the
	// developer's own queue.
	testkit.Isolate(t)
	root := t.TempDir()
	run := func(stdin string, args ...string) (string, error) {
		var out bytes.Buffer
		global = globalFlags{}
		err := notifyCmd(context.Background(), root, strings.NewReader(stdin), &out, args)
		return out.String(), err
	}

	t.Run("plain text produces a canonical event", func(t *testing.T) {
		out, err := run("build failed", "--outcome", "failed", "-o", "json")
		require.NoError(t, err)
		assert.Contains(t, out, `"outcome": "failed"`)
		assert.Contains(t, out, `"severity": "critical"`)
		assert.Contains(t, out, `"kind": "magus"`)
		assert.Contains(t, out, `"sub": "notify"`)
	})

	t.Run("canonical envelope preserves producer fields", func(t *testing.T) {
		ev := `{"schema_version":1,"outcome":"permission","severity":"warning","source":{"kind":"agent","sub":"host","id":"abc"},"message":"needs approval"}`
		out, err := run(ev, "-o", "json")
		require.NoError(t, err)
		assert.Contains(t, out, `"outcome": "permission"`)
		assert.Contains(t, out, `"severity": "warning"`)
		assert.Contains(t, out, `"sub": "host"`)
	})

	t.Run("positional input is refused", func(t *testing.T) {
		_, err := run("", "--outcome", "waiting", "needs you", "-o", "name")
		require.ErrorContains(t, err, "no positional arguments")
	})

	t.Run("empty stdin still produces a record", func(t *testing.T) {
		out, err := run("", "-o", "name")
		require.NoError(t, err)
		assert.Equal(t, "other\n", out)
	})
}

func countToasts(t *testing.T) *int {
	t.Helper()
	var toasts int
	prev := raiseDesktop
	raiseDesktop = func(context.Context, types.Event) error {
		toasts++
		return nil
	}
	t.Cleanup(func() { raiseDesktop = prev })
	return &toasts
}

// A block the queue cannot hold (no source id, no repository) has only the toast
// to reach a person, so it raises one every time.
func TestNotifyCmdToastsABlockWithNoQueueRow(t *testing.T) {
	testkit.Isolate(t)
	toasts := countToasts(t)
	run := func(root string) {
		t.Helper()
		global = globalFlags{}
		require.NoError(t, notifyCmd(context.Background(), root, strings.NewReader("needs approval"), io.Discard,
			[]string{"--outcome", "permission", "--desktop"}))
	}

	run(t.TempDir())
	assert.Equal(t, 1, *toasts, "plain text carries no source id, so no row opens and the toast is the only signal")

	t.Chdir(t.TempDir())
	run("")
	assert.Equal(t, 2, *toasts, "outside a repository there is no queue, and the block still notifies")
}

func TestNotifyCmdDoesNotToastARefiredPermission(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()
	toasts := countToasts(t)

	ev := `{"schema_version":1,"outcome":"permission","severity":"critical","source":{"kind":"agent","sub":"host","id":"abc"},"where":{"workspace":{"value":"/repo","is_dir":true},"files":[{"value":"cmd/magus/notify.go"}]},"message":"needs approval"}`
	run := func(stdin string, args ...string) {
		t.Helper()
		global = globalFlags{}
		require.NoError(t, notifyCmd(context.Background(), root, strings.NewReader(stdin), io.Discard, args))
	}
	run(ev, "--desktop", "-o", "name")
	run(ev, "--desktop", "-o", "name")
	assert.Equal(t, 1, *toasts, "the queue already holds the block; a second toast asks for a yes on it")

	failed := `{"schema_version":1,"outcome":"failed","source":{"kind":"agent","sub":"host","id":"abc"},"message":"the gate failed"}`
	run(failed, "--desktop", "-o", "name")
	run(failed, "--desktop", "-o", "name")
	assert.Equal(t, 3, *toasts, "a failure is news, never a queue row, and each one still notifies")
}

func TestRenderNotificationAlwaysHasABody(t *testing.T) {
	title, body := renderNotification(types.Event{Outcome: types.OutcomeWaiting})
	assert.NotEmpty(t, title)
	assert.NotEmpty(t, body)
}
