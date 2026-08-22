package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

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

func TestNotifyCmd(t *testing.T) {
	// A waiting or permission event opens a real attention request, so the store is
	// redirected here; without it these subtests would file requests into the
	// developer's own queue.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
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

func TestRenderNotificationAlwaysHasABody(t *testing.T) {
	title, body := renderNotification(types.Event{Outcome: types.OutcomeWaiting})
	assert.NotEmpty(t, title)
	assert.NotEmpty(t, body)
}
