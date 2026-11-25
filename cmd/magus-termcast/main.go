// Command magus-termcast renders magus's terminal output to SVG, in two modes:
//
//	magus-termcast [-record] [-showcase] [-materialize dir] [-out path]
//	magus-termcast shots [-out dir]
//
// With no subcommand it turns a recorded magus session into the animated SVG
// the README leads with. The shots subcommand renders the interactive views
// as stills for the documentation; shots.go says why those are staged rather
// than recorded.
//
// The recorded mode splits the job the GIF could not: RECORDING is a real session and cannot
// be reproduced byte for byte, while RENDERING is arithmetic and can. So the two
// live at different layers, and only the deterministic half is gated:
//
//	tapes/core-loop.session.sh   what to run          (source, hand-written)
//	tapes/core-loop.capture      what it printed      (recorded, refreshed by a human)
//	assets/gen/core-loop.svg     what a reader sees   (generated, drift-gated)
//
// The capture is the raw byte stream off a pseudo-terminal, so the commands, the
// numbers, the durations and the colors in the picture are the ones magus
// actually produced; nothing here writes a "[pass]" line. What is staged is the
// reading pace between frames, exactly as core-loop.session.sh already stages the
// pauses between commands.
//
// Why not keep recording a GIF: tapes/README.md notes that a screen recording
// "is not byte-reproducible, so it would fail every CI run by construction", and
// so the GIF was excluded from `generate` and drifted unnoticed: the README's
// own alt text described a project count magus had stopped reporting. Rendering
// from a committed capture keeps the recording honest AND puts the rendered
// artifact under the same drift gate as every other generated file, at about a
// thousandth of the bytes.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/termkit/recorder"
	"github.com/egladman/magus/libs/termkit/screen"
	"github.com/rogpeppe/go-internal/txtar"
)

// background is the context the recorder runs and renders under. Redaction refuses a
// context with no resolver, and this tool reads no secret, so an empty one masks all
// there is.
func background() (context.Context, error) {
	r, err := secrets.NewResolver(secrets.Config{})
	if err != nil {
		return nil, err
	}
	return secrets.WithResolver(context.Background(), r), nil
}

const (
	// The geometry the session is recorded AND rendered at. Fixed rather than
	// inherited: the tools lay their output out to the width they are told, so an
	// inherited size would make the capture a function of whoever recorded it.
	cols = 100
	// Taller than the longest single command's output needs, because the rows a
	// reserved band occupies are not available to scroll into: at 26 the last
	// command's own 21 lines did not fit, and the line naming the narrowed
	// affected set (the entire point of that act) had scrolled off the top
	// before the frame was taken. The unused rows are cropped back off at render
	// time rather than paid for in the picture.
	//
	// Raised from 34 when the gate-inheritance notice began explaining the delta
	// since the last green gate, one line per changed file, which is unbounded in
	// a way the rest of the act is not.
	rows = 48

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

func main() { command.Main(root()) }

// root declares the cast, the mode with no subcommand, and shots. It builds
// fresh flag targets on each call, so tests can invoke it repeatedly.
func root() command.Command {
	var record, showcase bool
	var out, materializeTo string
	return command.Command{
		Name:  "magus-termcast",
		Short: "Record or render the core loop and the showcase.",
		Usage: "magus-termcast [flags]\n       magus-termcast shots [-out dir]",
		Flags: []command.Flag{
			{Name: "record", Kind: command.Bool, Default: false, Doc: "re-record " + capturePath + " by running " + sessionPath + " for real", Target: &record},
			{Name: "out", Kind: command.String, Default: svgPath, Doc: "path to write the rendered SVG to", Target: &out},
			{Name: "materialize", Kind: command.String, Doc: "write the demo workspace from " + fixturePath + " into this directory and exit", Target: &materializeTo},
			{Name: "showcase", Kind: command.Bool, Default: false, Doc: "re-record " + showCapture + " by driving a real interactive session", Target: &showcase},
		},
		Children: []command.Command{shotsCommand()},
		Run: func(_ context.Context, inv command.Invocation) error {
			// Without this a mistyped subcommand would be ignored and the default
			// render would run in its place.
			if len(inv.Args) > 0 {
				return exit.Usagef("unknown subcommand %q, the only one is shots", inv.Args[0])
			}
			return cast(record, showcase, out, materializeTo)
		},
	}
}

// cast records or renders the core loop and the showcase.
func cast(record, showcase bool, out, materializeTo string) error {
	if showcase {
		if err := recordShowcaseSession(); err != nil {
			return err
		}
	}

	if materializeTo != "" {
		return materialize(materializeTo)
	}

	if record {
		if err := recordSession(); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "magus-termcast: recorded %s\n", capturePath)
	}

	if b, err := os.ReadFile(showCapture); err == nil {
		for _, v := range screen.ThemeVariants {
			svg, err := renderShowcase(string(b), v.Theme)
			if err != nil {
				return err
			}
			if err := write(screen.VariantPath(showSVG, v.Suffix), svg); err != nil {
				return err
			}
		}
	}

	for _, v := range screen.ThemeVariants {
		svg, err := renderFile(capturePath, v.Theme)
		if err != nil {
			return err
		}
		if err := write(screen.VariantPath(out, v.Suffix), svg); err != nil {
			return err
		}
	}
	return nil
}

func write(path, svg string) error {
	if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Fprintf(os.Stderr, "magus-termcast: wrote %s (%d bytes)\n", path, len(svg))
	return nil
}

// materialize writes the demo workspace out of the committed txtar and makes it
// a git repository the recording can compute an affected set against.
//
// The fixture is one txtar rather than a tree of real files because magus
// discovers a project from ANY magusfile.buzz below the workspace root: as
// files they would be projects of the magus repo itself (MGS1002), and as one
// archive they are data. It is not bash heredocs any more because ~280 lines of
// Go and Buzz inside shell strings are invisible to gofmt, the compiler, the
// linter and every editor, which is how the two bugs fixed below lived here
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
	// "origin/main", a REMOTE-TRACKING name a fixture with no remote does not
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
		ctx, err := background()
		if err != nil {
			return err
		}
		res, err := spawn.Exec(ctx, "git", args, spawn.ExecOptions{Dir: dir, Quiet: true, Capture: true})
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
	ctx, err := background()
	if err != nil {
		return err
	}
	res, err := spawn.Exec(ctx, "bash", []string{sessionPath}, spawn.ExecOptions{
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
	if err := checkNoise(res.Stdout, false); err != nil {
		return err
	}
	return os.WriteFile(capturePath, []byte(res.Stdout), 0o644)
}

// checkNoise refuses a capture that shows magus complaining.
//
// allowFailures separates the two kinds of recording. In the core loop a
// "[fail]" means the recording machine is broken; in the showcase the failures
// ARE the subject; it exists to demonstrate the views that only appear when
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
	shown := bad[:min(len(bad), 3)]
	msg := strings.Join(shown, ", ")
	if len(bad) > len(shown) {
		msg += fmt.Sprintf(", and %d more", len(bad)-len(shown))
	}
	return fmt.Errorf("the recorded session is not clean (%d warning or failure lines), fix the recording environment and re-record: %s", len(bad), msg)
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

func renderFile(path string, theme screen.Theme) (string, error) {
	capture, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read capture (re-record with -record): %w", err)
	}
	return render(string(capture), theme)
}

// render replays the capture through the terminal emulator, snapshotting the
// screen after each command, and animates the snapshots.
func render(capture string, theme screen.Theme) (string, error) {
	segments := recorder.Commands(capture, prompt)
	if len(segments) < 2 {
		return "", fmt.Errorf("capture has %d command segments, expected the session's several", len(segments))
	}
	return recorder.Animate(segments, cols, rows, corePace, theme)
}

var (
	corePace = recorder.Pace{Base: 0.28, PerLine: 0.028, Max: 0.85, Last: 1.3}

	// A higher floor, because the showcase's interactive beats move a highlight
	// rather than adding output: line count understates what they ask the reader
	// to find, and locating a cursor takes longer than reading one more line.
	showcasePace = recorder.Pace{Base: 0.42, PerLine: 0.028, Max: 0.95, Last: 1.5}
)
