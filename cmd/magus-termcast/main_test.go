package main

import (
	"os"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interactive/screen"
)

// repoFile reads a path relative to the repo root. The tests run in the command's
// own directory, and every artifact this command owns lives two levels up.
func repoFile(t *testing.T, path string) string {
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
// agree. Both palettes are checked - a variant nothing gates is a variant that
// goes stale, which is the failure this gate exists to prevent.
func TestCoreLoopUpToDate(t *testing.T) {
	for _, v := range themeVariants {
		path := variantPath(svgPath, v.suffix)
		want, err := render(repoFile(t, capturePath), v.theme)
		if err != nil {
			t.Fatalf("render %s: %v", path, err)
		}
		if got := repoFile(t, path); got != want {
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
	if err := checkClean(repoFile(t, capturePath)); err != nil {
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
	capture := repoFile(t, capturePath)
	segments := split(capture)
	if len(segments) < 4 {
		t.Fatalf("split gave %d segments, want the session's several", len(segments))
	}

	for _, want := range []struct{ what, text string }{
		{"cold run does all the work", "0 cached, 5 ran"},
		{"warm run does none of it", "5 cached, 0 ran"},
		{"affected narrows to what changed", "2 cached, 1 ran"},
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
// showed the summary of a run whose whole point - that it only did the work the
// edit reached - was no longer on screen.
//
// Cheap to assert and impossible to notice by eye once the animation is looping,
// so it is asserted.
func TestFinalFrameShowsTheNarrowing(t *testing.T) {
	frames, err := replay(repoFile(t, capturePath))
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	last := frames[len(frames)-1]
	for _, want := range []string{
		"$ magus affected ci",       // the command, so the frame is self-explaining
		"libs/authkit",              // the narrowed set
		"git diff vs origin/main",   // computed, not fallen back to
		"2 cached, 1 ran, 0 failed", // the payoff
	} {
		if last.FindRow(want) == 0 {
			t.Errorf("the final frame does not show %q; it has scrolled off the "+
				"visible screen even though the capture contains it", want)
		}
	}
}

// TestRenderIsDeterministic guards the property the drift gate depends on.
func TestRenderIsDeterministic(t *testing.T) {
	capture := repoFile(t, capturePath)
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
