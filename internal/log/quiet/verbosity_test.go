package quiet

import (
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
)

// TestExpandVerbosityArgsStopsAtSeparator pins the transformer half of the "--"
// guard: ExpandVerbosityArgs builds the master argv startup() parses (main.go's
// startup calls extractVerbosityCount(args), which itself calls this), so a token
// meant verbatim for a forwarded tool (e.g. `-vvv` as a literal positional the tool
// expects) must survive past "--" unchanged rather than being expanded into a run
// of "-v" tokens or dropped.
func TestExpandVerbosityArgsStopsAtSeparator(t *testing.T) {
	got := ExpandVerbosityArgs([]string{"run", "-vv", "build", "--", "-vvv", "--other"})
	want := []string{"run", "-v", "-v", "build", "--", "-vvv", "--other"}
	if len(got) != len(want) {
		t.Fatalf("ExpandVerbosityArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExpandVerbosityArgs = %v, want %v", got, want)
		}
	}
}

// TestExpandVerbosityArgsNoSeparator confirms the ordinary expansion still works
// when there is no "--" at all.
func TestExpandVerbosityArgsNoSeparator(t *testing.T) {
	got := ExpandVerbosityArgs([]string{"run", "-vvv", "build"})
	want := []string{"run", "-v", "-v", "-v", "build"}
	if len(got) != len(want) {
		t.Fatalf("ExpandVerbosityArgs = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ExpandVerbosityArgs = %v, want %v", got, want)
		}
	}
}

func TestEffectiveLevelQuietWinsOverVerbose(t *testing.T) {
	assert.Equal(t, slog.LevelError, EffectiveLevel(0, true))
	assert.Equal(t, slog.LevelError, EffectiveLevel(3, true))
	assert.Equal(t, slog.LevelInfo, EffectiveLevel(0, false))
	assert.Equal(t, slog.LevelDebug, EffectiveLevel(1, false))
	assert.Equal(t, slog.LevelDebug, EffectiveLevel(2, false))
	assert.Equal(t, config.LevelTrace, EffectiveLevel(3, false))
}

// levelName writes back into log.level, so it spells levels the way that setting's
// validator accepts them.
func TestLevelNameSpellsTheConfigVocabulary(t *testing.T) {
	assert.Equal(t, "trace", LevelName(config.LevelTrace))
	assert.Equal(t, "debug", LevelName(slog.LevelDebug))
	assert.Equal(t, "info", LevelName(slog.LevelInfo))
	assert.Equal(t, "error", LevelName(slog.LevelError))
}
