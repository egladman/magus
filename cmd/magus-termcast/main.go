// Command magus-termcast turns a recorded magus session into the animated SVG
// the README leads with.
//
// It splits the job the GIF could not: RECORDING is a real session and cannot
// be reproduced byte for byte, while RENDERING is arithmetic and can. So the two
// live at different layers, and only the deterministic half is gated:
//
//	tapes/core-loop.session.sh   what to run          (source, hand-written)
//	tapes/core-loop.capture      what it printed      (recorded, refreshed by a human)
//	assets/gen/core-loop.svg     what a reader sees   (generated, drift-gated)
//
// The capture is the raw byte stream off a pseudo-terminal, so the commands, the
// numbers, the durations and the colours in the picture are the ones magus
// actually produced - nothing here writes a "[pass]" line. What is staged is the
// reading pace between frames, exactly as core-loop.session.sh already stages the
// pauses between commands.
//
// Why not keep recording a GIF: tapes/README.md notes that a screen recording
// "is not byte-reproducible, so it would fail every CI run by construction", and
// so the GIF was excluded from `generate` and drifted unnoticed - the README's
// own alt text described a project count magus had stopped reporting. Rendering
// from a committed capture keeps the recording honest AND puts the rendered
// artifact under the same drift gate as every other generated file, at about a
// thousandth of the bytes.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/rogpeppe/go-internal/txtar"

	"github.com/egladman/magus/internal/interactive/screen"
	"github.com/egladman/magus/internal/proc/run"
)

const (
	// The geometry the session is recorded AND rendered at. Fixed rather than
	// inherited: the tools lay their output out to the width they are told, so an
	// inherited size would make the capture a function of whoever recorded it.
	cols = 100
	// Taller than the longest single command's output needs, because the rows a
	// reserved band occupies are not available to scroll into: at 26 the last
	// command's own 21 lines did not fit, and the line naming the narrowed
	// affected set - the entire point of that act - had scrolled off the top
	// before the frame was taken. The unused rows are cropped back off at render
	// time rather than paid for in the picture.
	rows = 34

	capturePath = "tapes/core-loop.capture"
	sessionPath = "tapes/core-loop.session.sh"
	fixturePath = "tapes/demo.txtar"
	svgPath     = "assets/gen/core-loop.svg"

	// prompt is what core-loop.session.sh's say() echoes before each command. It
	// is the only reliable frame boundary in the stream: magus's own output has no
	// marker for "a command finished", and splitting on blank lines or summaries
	// would break the moment either is reworded.
	prompt = "\x1b[38;5;73m$\x1b[0m "
)

func main() {
	record := flag.Bool("record", false, "re-record "+capturePath+" by running "+sessionPath+" for real")
	out := flag.String("out", svgPath, "path to write the rendered SVG to")
	materializeTo := flag.String("materialize", "", "write the demo workspace from "+fixturePath+" into this directory and exit")
	showcase := flag.Bool("showcase", false, "re-record "+showCapture+" by driving a real interactive session")
	flag.Parse()

	if *showcase {
		if err := recordShowcaseSession(); err != nil {
			fmt.Fprintf(os.Stderr, "magus-termcast: %v\n", err)
			os.Exit(1)
		}
	}

	if *materializeTo != "" {
		if err := materialize(*materializeTo); err != nil {
			fmt.Fprintf(os.Stderr, "magus-termcast: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if *record {
		if err := recordSession(); err != nil {
			fmt.Fprintf(os.Stderr, "magus-termcast: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "magus-termcast: recorded %s\n", capturePath)
	}

	if b, err := os.ReadFile(showCapture); err == nil {
		svg, rerr := renderShowcase(string(b))
		if rerr != nil {
			fmt.Fprintf(os.Stderr, "magus-termcast: %v\n", rerr)
			os.Exit(1)
		}
		if werr := os.WriteFile(showSVG, []byte(svg), 0o644); werr != nil {
			fmt.Fprintf(os.Stderr, "magus-termcast: write %s: %v\n", showSVG, werr)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "magus-termcast: wrote %s (%d bytes)\n", showSVG, len(svg))
	}

	svg, err := renderFile(capturePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus-termcast: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(svg), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "magus-termcast: write %s: %v\n", *out, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "magus-termcast: wrote %s (%d bytes)\n", *out, len(svg))
}

// materialize writes the demo workspace out of the committed txtar and makes it
// a git repository the recording can compute an affected set against.
//
// The fixture is one txtar rather than a tree of real files because magus
// discovers a project from ANY magusfile.buzz below the workspace root: as
// files they would be projects of the magus repo itself (MGS1002), and as one
// archive they are data. It is not bash heredocs any more because ~280 lines of
// Go and Buzz inside shell strings are invisible to gofmt, the compiler, the
// linter and every editor - which is how the two bugs fixed below lived here
// unnoticed.
func materialize(dir string) error {
	archive, err := txtar.ParseFile(fixturePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", fixturePath, err)
	}
	if len(archive.Files) == 0 {
		return fmt.Errorf("%s contains no files", fixturePath)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := txtar.Write(archive, dir); err != nil {
		return fmt.Errorf("write %s into %s: %w", fixturePath, dir, err)
	}

	// -b main is not cosmetic and neither is the origin ref. magus resolves the
	// affected set against a base REF, and the git driver's default is
	// "origin/main" - a REMOTE-TRACKING name a fixture with no remote does not
	// have. Without both, `git merge-base origin/main HEAD` exits 128, magus
	// falls back to "every project", and the recording's third act silently
	// stops demonstrating the affected set: every project is selected and the
	// untouched ones are merely cache hits, which reads on screen exactly like
	// the narrowing that did not happen.
	for _, args := range [][]string{
		{"init", "-q", "-b", "main", "."},
		{"add", "--", "."},
		{"-c", "user.email=demo@example.com", "-c", "user.name=magus", "commit", "-qm", "demo workspace"},
		{"update-ref", "refs/remotes/origin/main", "HEAD"},
	} {
		res, err := run.Exec(context.Background(), "git", args, run.ExecOptions{Dir: dir, Quiet: true, Capture: true})
		if err != nil {
			return fmt.Errorf("git %s: %w", args[0], err)
		}
		if res.Code != 0 {
			return fmt.Errorf("git %s: exit status %d: %s", args[0], res.Code, strings.TrimSpace(res.Stdout+res.Stderr))
		}
	}
	return nil
}

// recordSession runs the session script on a real pseudo-terminal and saves what
// it printed.
//
// Opt-in: it needs a provisioned toolchain and a couple of minutes, and it is the
// half of this command that cannot be reproduced. CI renders from the committed
// capture and never runs this.
func recordSession() error {
	res, err := run.Exec(context.Background(), "bash", []string{sessionPath}, run.ExecOptions{
		TTY:     true,
		TTYCols: cols,
		TTYRows: rows,
		Capture: true,
		Quiet:   true,
	})
	if err != nil {
		return fmt.Errorf("record %s: %w", sessionPath, err)
	}
	if res.Code != 0 {
		return fmt.Errorf("record %s: exit status %d", sessionPath, res.Code)
	}
	if err := checkClean(res.Stdout); err != nil {
		return err
	}
	return os.WriteFile(capturePath, []byte(res.Stdout), 0o644)
}

// checkClean refuses a capture that shows magus complaining.
//
// The recording is the first thing a reader sees, and a warning in it reads as
// the tool being broken rather than the recording machine being underprovisioned
// - which is what it has always meant so far. Failing here costs a re-record;
// not failing costs a wrong first impression that nothing else in the pipeline
// would catch, because a warning renders exactly as cleanly as a pass.
func checkClean(capture string) error { return checkNoise(capture, false) }

// checkNoise refuses a capture that shows magus complaining.
//
// allowFailures separates the two kinds of recording. In the core loop a
// "[fail]" means the recording machine is broken; in the showcase the failures
// ARE the subject - it exists to demonstrate the surfaces that only appear when
// something breaks. A "[warn]" is an environment problem in both.
func checkNoise(capture string, allowFailures bool) error {
	var bad []string
	for _, line := range strings.Split(capture, "\n") {
		if strings.Contains(line, "[fail]") && allowFailures {
			continue
		}
		if strings.Contains(line, "[warn]") || strings.Contains(line, "[fail]") {
			bad = append(bad, strings.TrimSpace(stripSGR(line)))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	msg := fmt.Sprintf("the recorded session is not clean (%d warning or failure lines); fix the recording environment and re-record", len(bad))
	for i, b := range bad {
		if i == 3 {
			msg += fmt.Sprintf("\n  ... and %d more", len(bad)-3)
			break
		}
		msg += "\n  " + b
	}
	return fmt.Errorf("%s", msg)
}

// stripSGR removes CSI escape sequences so a diagnostic quoting a captured line
// is readable in a log that is not a terminal.
//
// The parameter bytes have to be skipped explicitly rather than by scanning for
// the first byte in the final-byte range: '[' is 0x5B, which is itself inside
// that range, so the obvious loop terminates on the introducer and leaves the
// parameters behind as text ("33m[warn]0m").
func stripSGR(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) || s[i] != '[' {
			continue // not CSI; the introducer alone is enough to drop
		}
		for i++; i < len(s); i++ {
			// Parameter bytes are 0x30-0x3F and intermediates 0x20-0x2F; the
			// first byte outside both ends the sequence.
			if c := s[i]; c < 0x20 || c > 0x3F {
				break
			}
		}
	}
	return b.String()
}

func renderFile(path string) (string, error) {
	capture, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read capture: %w (re-record with -record)", err)
	}
	return render(string(capture))
}

// render replays the capture through the terminal emulator, snapshotting the
// screen after each command, and animates the snapshots.
func render(capture string) (string, error) {
	frames, err := replay(capture)
	if err != nil {
		return "", err
	}
	return screen.Animate(frames, corePace.holds(frames), screen.SVGOptions{})
}

// replay produces one frame per command: the terminal as it stood when that
// command finished. Split out from render so a test can assert what a frame
// SHOWS, which is not the same question as what the capture contains - a line
// present in the bytes may have scrolled off the top before the frame was taken.
func replay(capture string) ([]*screen.Screen, error) {
	segments := split(capture)
	if len(segments) < 2 {
		return nil, fmt.Errorf("capture has %d command segments; expected the session's several", len(segments))
	}

	s := screen.New(cols, rows)
	frames := make([]*screen.Screen, 0, len(segments))
	for i, seg := range segments {
		if _, err := s.Write([]byte(seg)); err != nil {
			return nil, fmt.Errorf("replay segment %d: %w", i, err)
		}
		frames = append(frames, s.Snapshot())
	}

	// Every frame is snapshotted after its command finished, so the band has been
	// released and its rows are blank on all of them. Cropping to the tallest one
	// keeps them the same size - which Animate requires - while spending no image
	// on rows the session never wrote to.
	used := 0
	for _, f := range frames {
		if u := f.LastUsedRow(); u > used {
			used = u
		}
	}
	for i, f := range frames {
		frames[i] = f.Crop(used)
	}
	return frames, nil
}

// split cuts the capture into one segment per command: the prompt line and
// everything the command printed before the next prompt.
func split(capture string) []string {
	parts := strings.Split(capture, prompt)
	if len(parts) < 2 {
		return nil
	}
	// parts[0] is whatever preceded the first prompt - on a pty that is the
	// terminal's own startup noise, which belongs on the first frame rather than
	// in a frame of its own.
	segs := make([]string, 0, len(parts)-1)
	for i, p := range parts[1:] {
		if i == 0 {
			p = parts[0] + prompt + p
		} else {
			p = prompt + p
		}
		segs = append(segs, p)
	}
	return segs
}

// Frame pacing, in seconds. A reading pace, not a measurement: the capture is a
// byte stream with no timings in it, and a frame-accurate replay of a run that
// finishes in 281ms would be unreadable.
//
// Not a flat hold, which is the obvious version and reads as a slideshow: every
// beat identical leaves the eye no rhythm to ride. A frame adding two lines is
// taken in at a glance, and holding it as long as one adding twenty stalls the
// sequence, so the dwell scales with what the frame actually adds.
type pace struct {
	base    float64 // a frame that adds nothing still needs a beat
	perLine float64 // each added line of output buys a little dwell
	max     float64 // past this a dense frame stalls the loop, which repeats anyway
	last    float64 // the payoff frame; a loop that snaps back the instant the point lands reads as a glitch
}

var (
	corePace = pace{base: 0.28, perLine: 0.028, max: 0.85, last: 1.3}

	// A higher floor, because the showcase's interactive beats move a highlight
	// rather than adding output: line count understates what they ask the reader
	// to find, and locating a cursor takes longer than reading one more line.
	showcasePace = pace{base: 0.42, perLine: 0.028, max: 0.95, last: 1.5}
)

func (p pace) hold(i, n, added int) float64 {
	if i == n-1 {
		return p.last
	}
	return min(p.base+p.perLine*float64(added), p.max)
}

// holds paces a whole recording, scaling each frame by what it adds over its
// predecessor. The first frame is measured against an empty screen.
func (p pace) holds(frames []*screen.Screen) []float64 {
	out := make([]float64, len(frames))
	for i := range frames {
		grew := frames[i].LastUsedRow() + 1
		if i > 0 {
			grew = added(frames[i-1], frames[i])
		}
		out[i] = p.hold(i, len(frames), grew)
	}
	return out
}

// added reports how many lines of output cur shows that prev did not, counting
// what scrolled off the top as well as what appeared at the bottom - a frame
// that filled the screen and pushed the earlier output away added all of it.
func added(prev, cur *screen.Screen) int {
	n := (cur.LastUsedRow() - prev.LastUsedRow()) + (cur.Scrolled() - prev.Scrolled())
	return max(n, 0)
}
