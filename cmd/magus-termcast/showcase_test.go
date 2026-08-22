package main

import (
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interactive/screen"
)

// TestShowcaseUpToDate is the same drift gate the core loop gets, for the same
// reason: the picture is a pure function of a committed capture, so CI can
// assert the two agree. Both palettes are checked - only the dark one is a
// declared output of termcast-generate, which makes the light one exactly the
// kind of variant that goes stale unnoticed.
func TestShowcaseUpToDate(t *testing.T) {
	capture := repoFile(t, showCapture)
	for _, v := range themeVariants {
		path := variantPath(showSVG, v.suffix)
		want, err := renderShowcase(capture, v.theme)
		if err != nil {
			t.Fatalf("render %s: %v", path, err)
		}
		if got := repoFile(t, path); got != want {
			t.Errorf("%s is stale (%d bytes on disk, %d rendered); run `magus run termcast-generate .`",
				path, len(got), len(want))
		}
	}
}

// TestShowcaseCaptureIsClean guards the committed capture the way
// TestCaptureIsClean guards the core loop's, with the one difference the
// showcase exists for: its failures ARE the subject, so only warnings are noise.
func TestShowcaseCaptureIsClean(t *testing.T) {
	if err := checkNoise(repoFile(t, showCapture), true); err != nil {
		t.Error(err)
	}
}

// TestShowcaseFramesDropTheTeardown pins where a frame ends. The recorder marks
// each frame as it is taken, so the bytes after the LAST mark are the session
// exiting - not a frame, and rendering them would end the animation on a shell
// prompt instead of on the surface the beat was about.
func TestShowcaseFramesDropTheTeardown(t *testing.T) {
	frames := showcaseFrames("first" + frameMark + "second" + frameMark + "exit\n")
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if frames[0] != "first" || frames[1] != "second" {
		t.Errorf("frames = %q", frames)
	}

	// An unmarked capture is one run of bytes, and there is no teardown tail to
	// trim off it.
	if frames := showcaseFrames("unmarked"); len(frames) != 1 || frames[0] != "unmarked" {
		t.Errorf("unmarked capture gave %q", frames)
	}
}

func TestRenderShowcaseRejectsAnUnmarkedCapture(t *testing.T) {
	if _, err := renderShowcase("no marks at all", screen.DarkTheme); err == nil {
		t.Error("renderShowcase accepted a capture with no frame marks")
	}
}

// TestRenderShowcaseIsDeterministic guards the property TestShowcaseUpToDate
// depends on: the same capture must render byte for byte the same, or the drift
// gate above reports a stale artifact on every run.
func TestRenderShowcaseIsDeterministic(t *testing.T) {
	capture := "first line\r\n" + frameMark + "second line\r\n" + frameMark + "exit\r\n"
	svg, err := renderShowcase(capture, screen.DarkTheme)
	if err != nil {
		t.Fatalf("renderShowcase: %v", err)
	}
	if !strings.Contains(svg, "<svg") {
		t.Fatalf("rendered output is not an SVG: %.80q", svg)
	}

	again, err := renderShowcase(capture, screen.DarkTheme)
	if err != nil {
		t.Fatalf("renderShowcase again: %v", err)
	}
	if svg != again {
		t.Error("two renders of one capture disagree; the drift gate cannot hold")
	}
}

// TestShowcaseScriptTakesEnoughFrames guards the script against an edit that
// stops taking pictures: the beats are data, and a step whose frame flag is
// dropped is invisible until someone looks at the animation.
func TestShowcaseScriptTakesEnoughFrames(t *testing.T) {
	steps := showcaseScript()
	if len(steps) == 0 {
		t.Fatal("the showcase script is empty")
	}
	framed := 0
	for i, st := range steps {
		if st.keys == "" {
			t.Errorf("step %d types nothing", i)
		}
		if st.settle <= 0 {
			t.Errorf("step %d does not let the screen settle", i)
		}
		if st.frame {
			framed++
		}
	}
	if framed < 2 {
		t.Errorf("the script takes %d frames; renderShowcase needs at least 2", framed)
	}
}
