package magus

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/ci/forecast"
)

// A graph build starts one sub-run per project, each loading the same history. The
// verdict on a history a newer magus wrote is said once, and every wait still returns
// the typed error.
func TestLoadHistorySaysAnUnsupportedVersionOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.jsonl")
	require.NoError(t, os.WriteFile(path, []byte(`{"workspace":{"version":99}}`+"\n"), 0o644))

	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
	historyUnreadable = sync.Once{}
	t.Cleanup(func() {
		slog.SetDefault(prev)
		historyUnreadable = sync.Once{}
	})

	m := &Magus{}
	m.cfg.Volatility.Enabled = true
	m.cfg.HistoryPath = path

	for range 3 {
		_, err := m.loadHistory(t.Context())()
		var skew *forecast.UnsupportedVersionError
		require.ErrorAs(t, err, &skew)
		assert.Equal(t, 99, skew.Have)
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "run history is unreadable"))
	assert.Contains(t, logs.String(), "version 99")
	assert.Contains(t, logs.String(), "left alone")
}
