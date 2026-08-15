package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive/screen"
)

// TestShotsUpToDate is the drift gate every generated file in this repo carries.
//
// These render magus's own interactive surfaces, so a change to how the zone
// lays out a band, or to what the failure prompt says, silently makes the
// documentation show something the terminal no longer does. Committed output
// with no gate is committed output that rots - which is exactly what happened
// to the langservice manifest before it grew one.
func TestShotsUpToDate(t *testing.T) {
	t.Parallel()
	want, err := render()
	require.NoError(t, err)

	for name, svg := range want {
		got, err := os.ReadFile(filepath.FromSlash("../../assets/gen/" + name))
		require.NoError(t, err, "read the committed shot")
		assert.Equal(t, svg, string(got),
			"%s is out of date; regenerate with: go run ./cmd/magus-termshots", name)
	}
}

// TestShotsAreDeterministic is what makes the gate above meaningful: a renderer
// that varied between runs would fail the drift test at random and teach
// everyone to regenerate without reading the diff.
func TestShotsAreDeterministic(t *testing.T) {
	t.Parallel()
	first, err := render()
	require.NoError(t, err)
	for range 3 {
		again, err := render()
		require.NoError(t, err)
		assert.Equal(t, first, again)
	}
}

// TestShotsAreNotBlank guards the failure mode a picture cannot show you: every
// surface here stands down without a terminal, so a probe that stopped
// answering would render empty frames and the gate would happily pin them.
func TestShotsAreNotBlank(t *testing.T) {
	t.Parallel()
	shots, err := render()
	require.NoError(t, err)
	// Four surfaces, each in both palettes.
	require.Len(t, shots, 8)

	for name, svg := range shots {
		assert.Contains(t, svg, "<text ", "%s drew no text at all", name)
	}
	assert.Contains(t, shots["terminal-run-band.svg"], "pool 6/8 running")
	assert.Contains(t, shots["terminal-lock-waiting.svg"], "waiting on the workspace lock")
	assert.Contains(t, shots["terminal-failure-prompt.svg"], "[esc] done")
	assert.Contains(t, shots["terminal-picker.svg"], "libs/gopherbuzz")
}

// TestFailurePromptShotShowsTheRealHint is the assertion the drift gate cannot
// make on its own.
//
// TestShotsUpToDate compares the renderer against itself, so it proves the
// picture is STABLE, not that it is TRUE. These strings were hand-copied
// literals of unexported constants in cmd/magus, which meant editing the prompt
// left the SVG byte-identical, the gate green, and the documentation showing a
// row the terminal had stopped printing.
//
// Asserting against cache.FailureHint rather than a literal here is the point:
// a copy of the text in the test would reintroduce exactly what it is guarding.
func TestFailurePromptShotShowsTheRealHint(t *testing.T) {
	svg, err := failurePrompt(screen.DarkTheme)
	if err != nil {
		t.Fatalf("failurePrompt: %v", err)
	}
	for _, span := range cache.FailureHint()[0].Spans {
		if strings.TrimSpace(span.Text) == "" {
			continue
		}
		if !strings.Contains(svg, span.Text) {
			t.Errorf("the rendered prompt does not show %q, which the terminal prints", span.Text)
		}
	}
}
