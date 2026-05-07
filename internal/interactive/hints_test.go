package interactive

import (
	"bytes"
	"strings"
	"testing"
)

func TestSuggestNearest_Exact(t *testing.T) {
	got := SuggestNearest("run", []string{"run", "list", "doctor"})
	if got != "run" {
		t.Errorf("SuggestNearest = %q; want %q", got, "run")
	}
}

func TestSuggestNearest_Typo(t *testing.T) {
	// One transposition / substitution away.
	got := SuggestNearest("rnu", []string{"run", "list", "doctor"})
	if got != "run" {
		t.Errorf("SuggestNearest(%q) = %q; want %q", "rnu", got, "run")
	}
}

func TestSuggestNearest_TooFar(t *testing.T) {
	// "zzzz" is too far from any real subcommand to suggest.
	got := SuggestNearest("zzzz", []string{"run", "list", "doctor"})
	if got != "" {
		t.Errorf("SuggestNearest(%q) = %q; want %q", "zzzz", got, "")
	}
}

func TestSuggestNearest_LongerThreshold(t *testing.T) {
	// "selfupdate" is 1 deletion away from "self-update" (8+ chars
	// allows threshold 3, but distance is 1).
	got := SuggestNearest("selfupdate", []string{"self-update", "doctor", "run"})
	if got != "self-update" {
		t.Errorf("SuggestNearest(%q) = %q; want %q", "selfupdate", got, "self-update")
	}
}

func TestEmit_DefaultOn(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	if !strings.HasPrefix(buf.String(), "hint: ") {
		t.Errorf("Emit output = %q; want prefix %q", buf.String(), "hint: ")
	}
}

func TestEmit_SetEnabledFalse(t *testing.T) {
	SetEnabled(false)
	t.Cleanup(func() { SetEnabled(true) })
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	if buf.Len() != 0 {
		t.Errorf("Emit wrote %q with SetEnabled(false); want nothing", buf.String())
	}
}

func TestEmit_SetEnabledTrue(t *testing.T) {
	SetEnabled(true)
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	if !strings.HasPrefix(buf.String(), "hint: ") {
		t.Errorf("Emit output = %q; want prefix %q", buf.String(), "hint: ")
	}
}
