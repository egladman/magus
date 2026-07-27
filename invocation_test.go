package magus

import (
	"bytes"
	"context"
	"errors"
	json "github.com/egladman/magus/internal/codec"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBeginInvocationWritesLifecycle(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	m, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	workspace := m.(*Magus)

	ctx, finish := workspace.BeginInvocation(context.Background(), journal.Command{
		Arguments: []string{"test"},
		Cwd:       root,
		Trigger:   journal.TriggerRun,
	}, "v0.test")
	id := journal.InvocationIDFromContext(ctx)
	require.NotEmpty(t, id)
	finish(errors.New("failed"))

	body, err := os.ReadFile(filepath.Join(workspace.CacheDir(), "runs", id+".jsonl"))
	require.NoError(t, err)
	decoder := json.NewDecoder(bytes.NewReader(body))
	var events []journal.Event
	for {
		var event journal.Event
		err := decoder.Decode(&event)
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		event.Ts = 0
		event.Inv = ""
		events = append(events, event)
	}
	assert.Equal(t, []journal.Event{
		{Kind: journal.KindStarted, Command: &journal.Command{Arguments: []string{"test"}, Cwd: root, Trigger: journal.TriggerRun}, MagusVersion: "v0.test"},
		{Kind: journal.KindFinished, Status: journal.StatusFail},
	}, events)
}
