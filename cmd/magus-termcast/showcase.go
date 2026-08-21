package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/egladman/magus/internal/proc/run"
)

// The interactive showcase: a real session, driven by real keystrokes, that
// walks the surfaces a reader cannot see in a transcript.
//
// core-loop.capture shows what magus PRINTS. This shows what it DRAWS - the
// pinned band, the failure tree beside its captured output, the picker
// searching the knowledge graph - none of which appear in a piped log, and all
// of which are the reason the terminal work exists.
//
// The frames are marked as they are taken rather than inferred afterwards.
// There is no prompt to split on inside an interactive surface, so the recorder
// says "here" at the moment it has finished typing and the screen has settled;
// frameMark is a byte no terminal tool emits, so it cannot collide with output.
const (
	frameMark = "\x00FRAME\x00"

	// Larger than the core loop's terminal, because these surfaces are the
	// subject rather than the backdrop: the two-view band needs width for both
	// columns, and the tree needs height to show grouping.
	showCols = 132
	showRows = 34

	showCapture = "tapes/showcase.capture"
	showSVG     = "assets/gen/showcase.svg"
)

// showcaseStep is one beat of the demo: what to type, and how long to let the
// screen settle before the frame is taken.
type showcaseStep struct {
	keys   string
	settle time.Duration
	// frame records whether this beat is worth a picture. Some beats only set
	// up the next one.
	frame bool
}

// recordShowcase drives the session and writes the marked capture.
func recordShowcase(dir string) error {
	s, err := run.StartTTY(context.Background(), "bash", []string{"--noprofile", "--norc"}, dir, showCols, showRows)
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}

	at := 0
	var out []byte
	take := func() {
		b := s.Output()
		out = append(out, b[at:]...)
		out = append(out, frameMark...)
		at = len(b)
	}

	for _, st := range showcaseScript() {
		if err := s.Type(st.keys); err != nil {
			return fmt.Errorf("type %q: %w", st.keys, err)
		}
		time.Sleep(st.settle)
		if st.frame {
			take()
		}
	}
	if err := s.Close(); err != nil {
		return fmt.Errorf("session: %w", err)
	}
	out = append(out, s.Output()[at:]...)

	if err := checkNoise(string(out), true); err != nil {
		return err
	}
	return os.WriteFile(showCapture, out, 0o644)
}

// showcaseScript is the demo, as keystrokes.
//
// Written as data rather than a shell script because the interesting beats are
// INSIDE magus - selecting a failure, tabbing focus, typing into the picker -
// where a shell has no way to reach.
func showcaseScript() []showcaseStep {
	const (
		enter = "\r"
		esc   = "\x1b"
		down  = "\x1b[B"
		tab   = "\t"
	)
	return []showcaseStep{
		{keys: "PS1='$ '\n", settle: 300 * time.Millisecond},
		{keys: "clear\n", settle: 300 * time.Millisecond},

		// 1. Cold. Real work, so the band is drawn against a run that is
		//    actually doing something.
		{keys: "magus run ci" + enter, settle: 4 * time.Second, frame: true},

		// 2. Warm. The same command, and the whole reason the cache exists.
		{keys: "magus run ci" + enter, settle: 2 * time.Second, frame: true},

		// 3. Failures pinned, and the prompt that makes them actionable. This
		//    is the surface a transcript cannot show: the tree grouped by
		//    project, the selected failure's captured output beside it.
		{keys: "magus run audit" + enter, settle: 3 * time.Second, frame: true},

		// 4. Move the selection: the preview follows it.
		{keys: down, settle: 900 * time.Millisecond, frame: true},

		// 5. Focus swaps which view is large - the golden ratio in motion.
		{keys: tab, settle: 900 * time.Millisecond, frame: true},
		{keys: tab, settle: 700 * time.Millisecond, frame: true},

		// 6. [o] prints the whole captured output into the TRANSCRIPT, where it
		//    is ordinary scrollback: no box, no padding, no divider, so it can
		//    be selected and copied the way any other terminal text can. That
		//    is the answer to "the band's two columns share rows" - the band is
		//    for reading, the transcript is for taking.
		{keys: "o", settle: 1400 * time.Millisecond, frame: true},

		// 7. [enter] reruns just that target with stepping on, pausing before
		//    each command so a reader can see what is about to happen.
		{keys: enter, settle: 1800 * time.Millisecond, frame: true},
		{keys: "s", settle: 1400 * time.Millisecond, frame: true},

		// 8. Leave the way the row says to.
		{keys: esc, settle: 900 * time.Millisecond, frame: true},

		// 9. The picker, searching the knowledge graph as it is typed.
		{keys: "magus x" + enter, settle: 1500 * time.Millisecond, frame: true},
		{keys: "auth", settle: 1200 * time.Millisecond, frame: true},
		{keys: esc, settle: 600 * time.Millisecond},

		{keys: "exit\n", settle: 400 * time.Millisecond},
	}
}

// showcaseFrames splits a marked capture into the byte runs behind each frame.
func showcaseFrames(capture string) []string {
	parts := strings.Split(capture, frameMark)
	if len(parts) > 1 {
		parts = parts[:len(parts)-1] // the tail after the last mark is teardown
	}
	return parts
}

// renderShowcase replays each marked frame and animates them.
//
// Cumulative, like the core loop: every frame is written into ONE screen, so
// what a reader sees is a terminal being used rather than a slideshow of
// unrelated stills. The band holding its rows while output scrolls past is the
// property the whole design rests on, and it is only visible if the frames
// share a screen.
func renderShowcase(capture string, theme screen.Theme) (string, error) {
	frames := showcaseFrames(capture)
	if len(frames) < 2 {
		return "", fmt.Errorf("showcase capture has %d frames; expected the script's several", len(frames))
	}
	s := screen.New(showCols, showRows)
	shots := make([]*screen.Screen, 0, len(frames))
	for i, f := range frames {
		if _, err := s.Write([]byte(f)); err != nil {
			return "", fmt.Errorf("replay frame %d: %w", i, err)
		}
		shots = append(shots, s.Snapshot())
	}
	used := 0
	for _, sh := range shots {
		if u := sh.LastUsedRow(); u > used {
			used = u
		}
	}
	for i, sh := range shots {
		shots[i] = sh.Crop(used)
	}
	return screen.Animate(shots, showcasePace.holds(shots), screen.SVGOptions{Theme: theme})
}

// recordShowcaseSession materializes a demo workspace and records the script
// against it.
//
// The SAME fixture the core loop uses, so both recordings show one product
// rather than two invented ones - and so a change to the demo projects is
// visible in both.
func recordShowcaseSession() error {
	dir, err := os.MkdirTemp("", "magus-showcase.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := materialize(dir); err != nil {
		return err
	}
	// The magus under test is this checkout's, not whatever is on PATH, so a
	// recording shows the behavior of the commit that produced it.
	repo, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Setenv("PATH", repo+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		return err
	}
	return recordShowcase(dir)
}
