package types

import (
	"errors"
	"strings"
	"testing"
)

func TestUnregisteredDepError_Error(t *testing.T) {
	err := &UnregisteredDepError{
		Missing: []UnregisteredDep{
			{Consumer: "api/", Dep: "shared/", DidYouMean: "common/"},
			{Consumer: "gateway/", Dep: "missing/"},
		},
	}
	msg := err.Error()
	if !strings.Contains(msg, "2 unresolved") {
		t.Errorf("Error() = %q, want '2 unresolved'", msg)
	}
	if !strings.Contains(msg, "did you mean: common/") {
		t.Errorf("Error() missing did-you-mean hint, got: %q", msg)
	}
	if !strings.Contains(msg, "gateway/") {
		t.Errorf("Error() missing second dep, got: %q", msg)
	}
}

func TestUnregisteredDepError_Is(t *testing.T) {
	err := &UnregisteredDepError{}
	if !errors.Is(err, ErrUnregisteredDep) {
		t.Error("errors.Is(UnregisteredDepError, ErrUnregisteredDep) = false, want true")
	}
}

func TestSpellErrors_Error(t *testing.T) {
	err := &SpellErrors{
		Project: "api/",
		Target:  "build",
		Failed: []SpellError{
			{Spell: "go", Err: errors.New("exit 1")},
		},
	}
	msg := err.Error()
	if !strings.Contains(msg, "build") {
		t.Errorf("SpellErrors.Error() missing target, got: %q", msg)
	}
	if !strings.Contains(msg, "go") {
		t.Errorf("SpellErrors.Error() missing spell name, got: %q", msg)
	}
	if !strings.Contains(msg, "exit 1") {
		t.Errorf("SpellErrors.Error() missing underlying error, got: %q", msg)
	}
}

func TestSpellErrors_Unwrap(t *testing.T) {
	inner := errors.New("exit 2")
	err := &SpellErrors{
		Failed: []SpellError{{Spell: "go", Err: inner}},
	}
	if !errors.Is(err, inner) {
		t.Error("errors.Is via Unwrap failed")
	}
}

func TestDiagnosticCode_URL(t *testing.T) {
	cases := []struct {
		code    DiagnosticCode
		wantSub string
	}{
		{PathReadDenied, "MGS2001"},
		{RaceDetected, "MGS4001"},
		{NoCITarget, "MGS1001"},
	}
	for _, tc := range cases {
		url := tc.code.URL()
		if !strings.Contains(url, tc.wantSub) {
			t.Errorf("URL() = %q, want to contain %q", url, tc.wantSub)
		}
		if !strings.HasSuffix(url, ".md") {
			t.Errorf("URL() = %q, want .md suffix", url)
		}
	}
	// MGS1xxx routes to the magusfile docs dir, not the sandbox/race bases.
	if url := NoCITarget.URL(); !strings.Contains(url, "/docs/codes/magusfile/") {
		t.Errorf("NoCITarget.URL() = %q, want to route to /docs/codes/magusfile/", url)
	}
}

func TestDiagnosticError_Is(t *testing.T) {
	err := DiagnosticErrorf(PathReadDenied, "test")
	if !errors.Is(err, ErrDiag) {
		t.Error("DiagnosticError should match ErrDiag")
	}
	same := DiagnosticErrorf(PathReadDenied, "other")
	if !errors.Is(err, same) {
		t.Error("DiagnosticError should match same-code DiagnosticError")
	}
	other := DiagnosticErrorf(PathWriteDenied, "test")
	if errors.Is(err, other) {
		t.Error("DiagnosticError should not match different-code DiagnosticError")
	}
}

func TestDiagnosticErrorf(t *testing.T) {
	err := DiagnosticErrorf(EnvStripped, "var %s was stripped", "HOME")
	msg := err.Error()
	if !strings.Contains(msg, "MGS2003") {
		t.Errorf("Error() = %q, want MGS2003", msg)
	}
	if !strings.Contains(msg, "HOME") {
		t.Errorf("Error() = %q, want 'HOME'", msg)
	}
}
