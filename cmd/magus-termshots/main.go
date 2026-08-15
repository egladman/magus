// Command magus-termshots renders magus's interactive terminal surfaces to SVG
// for the documentation.
//
// It is the deterministic sibling of magus-termcast, which records a real
// session through a pseudo-terminal. Both are right, for different subjects. The
// core loop is a sequence of commands anyone can type, so recording it for real
// is the truthful thing to do. The surfaces here are conditions - a run stalled
// on another process's lock, a target that failed, a prompt waiting on a
// choice - and staging those in a real shell reliably enough to record is
// painful.
//
// So these are rendered instead of recorded: the frames come from driving the
// REAL Zone, Notifier and picker through the terminal emulator, so the escape
// stream and the layout are exactly what a terminal would receive. What is
// staged is the scenario, not the rendering.
//
// The output is deterministic - no timestamps, no randomness - which is what
// lets it be committed and checked for drift like every other generated file.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/egladman/magus/internal/interactive/tty"
)

func main() {
	outDir := flag.String("out", filepath.Join("assets", "gen"), "directory to write the SVGs into")
	flag.Parse()

	shots, err := render()
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus-termshots: %v\n", err)
		os.Exit(1)
	}
	for name, svg := range shots {
		path := filepath.Join(*outDir, name)
		if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "magus-termshots: write %s: %v\n", path, err)
			os.Exit(1)
		}
	}
	fmt.Fprintf(os.Stderr, "magus-termshots: wrote %d shots to %s\n", len(shots), *outDir)
}

// render produces every shot, keyed by filename. Split from main so the drift
// test can compare without writing.
func render() (map[string]string, error) {
	out := map[string]string{}
	for _, s := range []struct {
		name string
		fn   func(screen.Theme) (string, error)
	}{
		{"terminal-run-band.svg", runBand},
		{"terminal-lock-waiting.svg", lockWaiting},
		{"terminal-failure-prompt.svg", failurePrompt},
		{"terminal-picker.svg", picker},
	} {
		// Every surface ships in both palettes: an SVG referenced by <img> is its
		// own document and cannot read the page's theme, so the page picks the file
		// rather than the picture adapting itself. The unsuffixed name stays the
		// dark one, which is what every existing reference already points at.
		for _, v := range []struct {
			suffix string
			theme  screen.Theme
		}{
			{"", screen.DarkTheme},
			{"-light", screen.LightTheme},
		} {
			svg, err := s.fn(v.theme)
			if err != nil {
				return nil, fmt.Errorf("%s%s: %w", s.name, v.suffix, err)
			}
			out[strings.TrimSuffix(s.name, ".svg")+v.suffix+".svg"] = svg
		}
	}
	return out, nil
}

const (
	// Wide enough for the failure prompt's instruction row to sit clear of the
	// right edge. At 84 the row is 83 columns and "[o] output" touched
	// "[esc] done", which reads as a rendering fault rather than as the tight
	// right-alignment it is. 100 also matches the core-loop recording, so the
	// documentation's pictures share one terminal size.
	cols = 132
	// Tall enough that the command producing the scene stays on screen. The real
	// handler prints four lines per failure, which at 15 scrolled "$ magus
	// affected ci" away and left a picture that could not say what made it.
	rows = 21
)

// newTerminal returns a screen and a zone that draws on it.
func newTerminal() (*screen.Screen, *tty.Zone) {
	s := screen.New(cols, rows)
	return s, tty.NewZone(s, tty.FixedProbe(cols, rows))
}

// runBand: ordinary output scrolling past a band that does not move. The
// property the whole design rests on, and the one a still picture shows best.
func runBand(t screen.Theme) (string, error) {
	s, z := newTerminal()
	band := z.Acquire(5)

	fmt.Fprint(s, "$ magus affected ci\n")
	for _, p := range []string{"internal/cache", "internal/interp", "internal/spell", "std", "types", "cmd/magus"} {
		fmt.Fprintf(s, "[pass] build %s (cached, 0.0s)\n", p)
	}
	if _, err := band.Set([]tty.Line{
		{Text: "pool 6/8 running   6 ok", Style: tty.SGRDim},
	}); err != nil {
		return "", err
	}
	return s.SVG(screen.SVGOptions{Theme: t}), nil
}

// lockWaiting: the one run event that earns a notification. A pinned condition,
// not a toast - it stays until the lock clears, because the wait is unbounded.
func lockWaiting(t screen.Theme) (string, error) {
	s := screen.New(cols, rows)
	z := tty.NewZone(s, tty.FixedProbe(cols, rows))
	n := tty.NewNotifier(z, 2)

	fmt.Fprint(s, "$ magus run build api\n")
	fmt.Fprint(s, "projects: api (vcs)\n")
	// The status goes in the box's title, exactly as a run puts it there.
	if _, err := z.SetTitle(cache.PoolGauge(0, 8)+"  0 ok", "12.4s"); err != nil {
		return "", err
	}
	if err := n.Pin("lock", "waiting on the workspace lock held by pid 4211 - wait, or stop it", tty.SGRYellow); err != nil {
		return "", err
	}
	return s.SVG(screen.SVGOptions{Theme: t}), nil
}

// failurePrompt: failures held past the summary so they can be acted on, with
// the way out pinned to the right edge where it is the last thing clipped.
//
// The band is drawn by a REAL PrettyHandler fed real records, not composed here.
// Hand-writing its rows meant the picture stated a status line magus never
// emits and missed the selection marker entirely once the band grew one, while
// the drift gate - which compares the renderer to itself - stayed green
// throughout.
func failurePrompt(t screen.Theme) (string, error) {
	s := screen.New(cols, rows)
	h := cache.NewPrettyHandlerFor(s, slog.LevelInfo, tty.FixedProbe(cols, rows), demoClock())

	fmt.Fprint(s, "$ magus affected ci\n")
	fmt.Fprint(s, "[pass] build std (cached, 0.0s)\n")
	fmt.Fprint(s, "  cause: exit status 1\n")
	fmt.Fprint(s, "  inspect: magus query output out7c21\n")

	ctx := context.Background()
	for _, r := range []slog.Record{
		event("cache.pool", slog.Int("capacity", 8), slog.Int("running", 3)),
		event("cache.error", slog.String("project", "internal/sandbox"),
			slog.String("target", "test"), slog.Int64("duration", int64(4100*time.Millisecond)),
			slog.String("error", "exit status 1"), slog.String("ref", "out7c21b4f0")),
		event("cache.error", slog.String("project", "std"),
			slog.String("target", "lint"), slog.Int64("duration", int64(2300*time.Millisecond)),
			slog.String("error", "exit status 1"), slog.String("ref", "out3f9a21ce")),
		// A second failure in the SAME project, so the shot shows what the tree
		// exists for: grouping, rather than the project repeated per row.
		event("cache.error", slog.String("project", "std"),
			slog.String("target", "test"), slog.Int64("duration", int64(1100*time.Millisecond)),
			slog.String("error", "exit status 1"), slog.String("ref", "out8b12cc90")),
	} {
		if err := h.Handle(ctx, r); err != nil {
			return "", err
		}
	}
	h.SetSelection(0)
	// Focus on the OUTPUT, so the golden ratio gives it the major share - this
	// is the picture of a reader reading a failure, not navigating the list.
	h.SetFocus(cache.FocusPreview)
	// The second view: the selected failure's captured output, beside the tree
	// rather than replacing it. Both columns are this handler's own data.
	h.SetPreview([]string{
		"=== RUN   TestSandboxDeniesWrite",
		"    sandbox_test.go:118: expected EPERM,",
		"        got nil writing /etc/hosts",
		"--- FAIL: TestSandboxDeniesWrite (0.31s)",
		"FAIL    magus/internal/sandbox  4.108s",
	})

	// The handler's OWN zone, not a second one over the same screen: two owners
	// each set margins from their own arithmetic and draw over each other.
	hint := h.Zone().Acquire(1)
	// The REAL instruction row, not a copy of its text. Hand-copied literals
	// were the drift this whole pipeline exists to end, one layer up: the gate
	// compared the copy against itself, so editing the prompt left the picture
	// byte-identical and green while the terminal said something else.
	if _, err := hint.Set(cache.FailureHint()); err != nil {
		return "", err
	}
	return s.SVG(screen.SVGOptions{Theme: t}), nil
}

// demoClock is a fixed clock that reports one plausible elapsed time.
//
// Frozen entirely and the status row reads "0ns", which is not what a run in
// progress looks like; live and the committed SVG changes on every render. So
// it answers the first call - which is what the handler records as the run's
// start - and a fixed moment later for every call after it. Deterministic
// because the sequence of calls is.
func demoClock() func() time.Time {
	start := time.Unix(0, 0).UTC()
	first := true
	return func() time.Time {
		if first {
			first = false
			return start
		}
		return start.Add(6400 * time.Millisecond)
	}
}

// event builds one slog record the way the cache emits them, so a shot can feed
// a real handler instead of describing what it would have drawn.
func event(msg string, attrs ...slog.Attr) slog.Record {
	r := slog.NewRecord(time.Time{}, slog.LevelInfo, msg, 0)
	r.AddAttrs(attrs...)
	return r
}

// picker: the interactive chooser, drawn in place with the highlight on the row
// a click or Enter would take.
func picker(t screen.Theme) (string, error) {
	s := screen.New(cols, rows)
	fmt.Fprint(s, "$ magus x\n")

	// The REAL picker composition, not an imitation of it. Hand-writing this
	// block is how the shot came to show a selection marker and a frame the
	// picker had stopped drawing.
	p := tty.FixedProbe(cols, rows)
	view := tty.NewInlineView(s, p)
	frame := tty.RenderPick(s, p,
		[]string{"console", "docs", "libs/gopherbuzz", "std", "types"},
		tty.PickOptions{Prompt: "project"}, "gop", 0)
	if !view.Paint(frame) {
		return "", fmt.Errorf("the picker block does not fit %dx%d", cols, rows)
	}
	return s.SVG(screen.SVGOptions{Theme: t}), nil
}
