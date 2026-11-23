package main

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/proc/spawn"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/termkit/recorder"
	"github.com/egladman/magus/libs/termkit/screen"
)

// termcast runs the command line args name, returning its error.
func termcast(args []string) error {
	_, err := command.Invoke(context.Background(), root(), args,
		command.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	return err
}

// repositoryFile reads a path relative to the repo root. The tests run in the command's
// own directory, and every artifact this command owns lives two levels up.
func repositoryFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestCoreLoopUpToDate is the drift gate rendering buys.
//
// It is the whole reason for rendering rather than recording: the artifact a
// reader sees is a pure function of a committed capture, so CI can assert the two
// agree. Both palettes are checked: a variant nothing gates is a variant that
// goes stale, which is the failure this gate exists to prevent.
func TestCoreLoopUpToDate(t *testing.T) {
	for _, v := range screen.ThemeVariants {
		path := screen.VariantPath(svgPath, v.Suffix)
		want, err := render(repositoryFile(t, capturePath), v.Theme)
		if err != nil {
			t.Fatalf("render %s: %v", path, err)
		}
		if got := repositoryFile(t, path); got != want {
			t.Errorf("%s is stale (%d bytes on disk, %d rendered); run `magus run termcast-generate .`",
				path, len(got), len(want))
		}
	}
}

// TestCaptureIsClean guards the committed capture, not just a fresh recording.
//
// checkClean runs at record time, but the capture is a committed file: one landing
// from a branch that predates the check, or edited by hand, would sail past it.
// The recording is the first thing a reader sees, so a warning in it is a wrong
// first impression no other gate would catch.
func TestCaptureIsClean(t *testing.T) {
	if err := checkClean(repositoryFile(t, capturePath)); err != nil {
		t.Error(err)
	}
}

// TestFramesTellTheStory pins the three numbers the recording exists to show.
//
// Rendering correctly is not the same as showing the right thing. The cache story
// is cold -> warm -> narrowed, and each step is a specific claim the README makes
// in prose beside the picture; a capture that still renders beautifully while
// reporting "5 ran" three times would be a silent lie.
func TestFramesTellTheStory(t *testing.T) {
	capture := repositoryFile(t, capturePath)
	segments := recorder.Commands(capture, prompt)
	if len(segments) < 4 {
		t.Fatalf("split gave %d segments, want the session's several", len(segments))
	}

	for _, want := range []struct{ what, text string }{
		{"cold run does all the work", "0 cached, 5 ran"},
		{"warm run does none of it", "5 cached, 0 ran"},
		{"affected narrows to what changed", "0 cached, 2 ran"},
		{"the affected set is computed, not fallen back to", "git diff vs origin/main"},
	} {
		if !strings.Contains(capture, want.text) {
			t.Errorf("capture does not show that %s: no %q", want.what, want.text)
		}
	}

	// The fallback prints the affected set as a diagnostic instead of narrowing,
	// and still ends in a believable "N cached, 1 ran" because the untouched
	// projects are cache hits. That made a broken third act indistinguishable from
	// a working one by eye, so it is asserted rather than watched for.
	if strings.Contains(capture, "cannot compute affected set") {
		t.Error("capture shows the affected set falling back to every project; " +
			"the fixture needs a resolvable base ref (see tapes/demo-init.sh)")
	}
}

// TestFinalFrameShowsTheNarrowing asserts what the last frame DISPLAYS.
//
// Distinct from TestFramesTellTheStory, which only proves the bytes are in the
// capture. They were, and the frame still did not show them: the terminal
// reserves rows for the pinned band, so the scroll region is smaller than the
// screen, and at the size this was first recorded at the line naming the
// narrowed set had scrolled off the top before the frame was taken. The picture
// showed the summary of a run whose whole point (that it only did the work the
// edit reached) was no longer on screen.
//
// Cheap to assert and impossible to notice by eye once the animation is looping,
// so it is asserted.
func TestFinalFrameShowsTheNarrowing(t *testing.T) {
	svg, err := render(repositoryFile(t, capturePath), screen.DarkTheme)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var animation struct {
		Frames []struct {
			Text []string `xml:"text"`
		} `xml:"g"`
	}
	if err := xml.Unmarshal([]byte(svg), &animation); err != nil {
		t.Fatalf("parse SVG: %v", err)
	}
	if len(animation.Frames) == 0 {
		t.Fatal("animation has no frames")
	}
	last := strings.Join(animation.Frames[len(animation.Frames)-1].Text, "")
	for _, want := range []string{
		"$ magus affected ci",       // the command, so the frame is self-explaining
		"libs/authkit",              // the edited project
		"apps/admin",                // the one that depends on it, which is the narrowing
		"git diff vs origin/main",   // computed, not fallen back to
		"0 cached, 2 ran, 0 failed", // the payoff
	} {
		if !strings.Contains(last, want) {
			t.Errorf("the final frame does not show %q; it has scrolled off the "+
				"visible screen even though the capture contains it", want)
		}
	}
}

// TestRenderIsDeterministic guards the property the drift gate depends on.
func TestRenderIsDeterministic(t *testing.T) {
	capture := repositoryFile(t, capturePath)
	first, err := render(capture, screen.DarkTheme)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	second, err := render(capture, screen.DarkTheme)
	if err != nil {
		t.Fatalf("render again: %v", err)
	}
	if first != second {
		t.Error("two renders of one capture disagree; the drift gate cannot hold")
	}
}

func TestStripSGR(t *testing.T) {
	// The bug this exists for: '[' is 0x5B, inside the CSI final-byte range, so a
	// scan for the first byte in that range stops on the introducer and leaves the
	// parameters as text.
	got := stripSGR("\x1b[33m[warn]\x1b[0m probe failed\x1b[2m spell=go\x1b[0m")
	if want := "[warn] probe failed spell=go"; got != want {
		t.Errorf("stripSGR = %q, want %q", got, want)
	}
}

// TestStripSGRDropsATruncatedSequence pins what happens at the end of the input.
// A capture is cut at a frame boundary, so the last line can end mid-sequence,
// and the loop has to stop rather than emit the introducer as text.
func TestStripSGRDropsATruncatedSequence(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"done\x1b", "done"},
		{"done\x1b[", "done"},
		{"done\x1b[38;5;73", "done"},
		{"a\x1bXb", "ab"}, // an escape that is not CSI: the introducer alone goes
	} {
		if got := stripSGR(tc.in); got != tc.want {
			t.Errorf("stripSGR(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestCheckNoiseSeparatesTheTwoRecordings guards the one difference between the
// core loop and the showcase: a "[fail]" is a broken recording machine in the
// first and the entire subject in the second. A "[warn]" is an environment
// problem in both, so allowFailures must not wave it through.
func TestCheckNoiseSeparatesTheTwoRecordings(t *testing.T) {
	failure := "$ magus run ci\n\x1b[31m[fail]\x1b[0m api build\n"
	if err := checkNoise(failure, false); err == nil {
		t.Error("a [fail] in the core loop must be refused")
	}
	if err := checkNoise(failure, true); err != nil {
		t.Errorf("a [fail] is the showcase's subject, not an error: %v", err)
	}

	warning := "$ magus run ci\n\x1b[33m[warn]\x1b[0m probe failed\n"
	for _, allowFailures := range []bool{false, true} {
		if err := checkNoise(warning, allowFailures); err == nil {
			t.Errorf("a [warn] must be refused with allowFailures=%v", allowFailures)
		}
	}
}

// TestCheckNoiseQuotesThreeLinesThenCounts keeps a broken recording's diagnostic
// readable: the escape sequences are stripped so a log that is not a terminal
// shows the text, and a capture full of warnings does not paste the whole run
// into one error.
func TestCheckNoiseQuotesThreeLinesThenCounts(t *testing.T) {
	var capture strings.Builder
	for i := range 5 {
		fmt.Fprintf(&capture, "\x1b[33m[warn]\x1b[0m probe %d failed\n", i)
	}
	err := checkNoise(capture.String(), false)
	if err == nil {
		t.Fatal("five warning lines must be refused")
	}
	msg := err.Error()
	for _, want := range []string{"(5 warning or failure lines)", "[warn] probe 0 failed", ", and 2 more"} {
		if !strings.Contains(msg, want) {
			t.Errorf("diagnostic does not contain %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "\x1b") {
		t.Error("the diagnostic still carries escape sequences; it is unreadable in a log")
	}
	if strings.Contains(msg, "probe 3 failed") {
		t.Error("more than three lines were quoted")
	}
}

// TestSplitNeedsAPrompt pins the frame boundary: the prompt is the only reliable
// one in the stream, so a capture without it yields no segments rather than one
// segment holding the whole session.
func TestSplitNeedsAPrompt(t *testing.T) {
	if segs := recorder.Commands("plain output with no prompt\n", prompt); segs != nil {
		t.Errorf("split found %d segments in a promptless capture", len(segs))
	}

	segs := recorder.Commands("noise\n"+prompt+"one\n"+prompt+"two\n", prompt)
	if len(segs) != 2 {
		t.Fatalf("split gave %d segments, want 2", len(segs))
	}
	if !strings.HasPrefix(segs[0], "noise\n"+prompt) {
		t.Errorf("the pty's startup noise belongs on the first frame, not in one of its own: %q", segs[0])
	}
	if !strings.HasPrefix(segs[1], prompt) {
		t.Errorf("every later segment opens with the prompt line: %q", segs[1])
	}
}

func TestRenderRejectsACaptureWithNoCommands(t *testing.T) {
	if _, err := render("nothing that looks like a session", screen.DarkTheme); err == nil {
		t.Error("render accepted a capture with no command segments")
	}
}

func TestRenderFileNamesTheRemedyForAMissingCapture(t *testing.T) {
	_, err := renderFile(filepath.Join(t.TempDir(), "absent.capture"), screen.DarkTheme)
	if err == nil {
		t.Fatal("renderFile accepted a missing capture")
	}
	if !strings.Contains(err.Error(), "-record") {
		t.Errorf("the error does not say how to produce the capture: %v", err)
	}
}

// TestPaceScalesWithWhatAFrameAdds pins the pacing rule, which is the one part
// of the picture that is staged rather than recorded: a dense frame holds longer
// than a sparse one, up to a cap, and the payoff frame overrides both.
func TestPaceScalesWithWhatAFrameAdds(t *testing.T) {
	p := corePace
	for _, tc := range []struct {
		what        string
		i, n, added int
		want        float64
	}{
		{"a frame that adds nothing still gets a beat", 0, 4, 0, 0.28},
		{"a shorter frame still gets the minimum beat", 0, 4, -1, 0.28},
		{"each added line buys dwell", 1, 4, 5, 0.28 + 5*0.028},
		{"a dense frame is capped", 2, 4, 100, 0.85},
		{"the payoff frame overrides the scale", 3, 4, 0, 1.3},
	} {
		// A delta, not equality: the want column is written as arithmetic
		// (0.28 + 5*0.028) and float addition does not round-trip exactly.
		if got := p.Hold(tc.i, tc.n, tc.added); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("%s: hold(%d, %d, %d) = %v, want %v", tc.what, tc.i, tc.n, tc.added, got, tc.want)
		}
	}
}

func TestWriteWritesTheRenderedSVG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.svg")
	if err := write(path, "<svg/>"); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "<svg/>" {
		t.Errorf("wrote %q", got)
	}
}

func TestMaterializeReportsAMissingFixture(t *testing.T) {
	// The tests run in the command's own directory, where tapes/ does not exist.
	if err := materialize(t.TempDir()); err == nil {
		t.Error("materialize accepted a missing fixture")
	} else if !strings.Contains(err.Error(), fixturePath) {
		t.Errorf("the error does not name the fixture it could not read: %v", err)
	}
}

// TestMaterializeLeavesAResolvableBaseRef is the check the third act of the
// recording depends on. magus resolves the affected set against origin/main, a
// remote-tracking name a fresh fixture does not have; without it `git merge-base`
// exits 128, magus falls back to every project, and the narrowing the recording
// exists to show silently stops happening while still looking plausible.
func TestMaterializeLeavesAResolvableBaseRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Chdir("../..") // fixturePath is relative to the repo root

	dir := t.TempDir()
	if err := materialize(dir); err != nil {
		t.Fatalf("materialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err != nil {
		t.Errorf("the demo workspace has no root magusfile: %v", err)
	}
	ctx, err := background()
	if err != nil {
		t.Fatalf("background: %v", err)
	}
	res, err := spawn.Exec(ctx, "git",
		[]string{"merge-base", "origin/main", "HEAD"},
		spawn.ExecOptions{Dir: dir, Quiet: true, Capture: true})
	if err != nil {
		t.Fatalf("git merge-base: %v", err)
	}
	if res.Code != 0 {
		t.Errorf("origin/main does not resolve in the materialized fixture (exit %d): %s",
			res.Code, strings.TrimSpace(res.Stdout+res.Stderr))
	}
}
