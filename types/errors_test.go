package types

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnregisteredDepError_Error(t *testing.T) {
	err := &UnregisteredDepError{
		Missing: []UnregisteredDep{
			{Consumer: "api/", Dep: "shared/", DidYouMean: "common/"},
			{Consumer: "gateway/", Dep: "missing/"},
		},
	}
	msg := err.Error()
	assert.Contains(t, msg, "2 unresolved")
	assert.Contains(t, msg, "did you mean: common/")
	assert.Contains(t, msg, "gateway/")
}

func TestUnregisteredDepError_Is(t *testing.T) {
	err := &UnregisteredDepError{}
	assert.ErrorIs(t, err, ErrUnregisteredDep)
}

func TestSpellErrors_Error(t *testing.T) {
	err := &SpellErrors{
		Project: "api/",
		Target:  "build",
		Failed: []SpellFailure{
			{Spell: "go", Err: errors.New("exit 1")},
		},
	}
	msg := err.Error()
	assert.Contains(t, msg, "build", "missing target")
	assert.Contains(t, msg, "go", "missing spell name")
	assert.Contains(t, msg, "exit 1", "missing underlying error")
}

func TestSpellErrors_Unwrap(t *testing.T) {
	inner := errors.New("exit 2")
	err := &SpellErrors{
		Failed: []SpellFailure{{Spell: "go", Err: inner}},
	}
	assert.ErrorIs(t, err, inner)
}

func TestDiagnosticCode_URL(t *testing.T) {
	assert.Contains(t, PathReadDenied.URL(), "MGS2001")
	assert.Contains(t, RaceDetected.URL(), "MGS4001")
	assert.Contains(t, NoCITarget.URL(), "MGS1001")

	for _, code := range []DiagnosticCode{PathReadDenied, RaceDetected, NoCITarget} {
		assert.Truef(t, strings.HasSuffix(code.URL(), ".md"), "URL() = %q, want .md suffix", code.URL())
	}
	// MGS1xxx routes to the magusfile docs dir, not the sandbox/race bases.
	assert.Contains(t, NoCITarget.URL(), "/docs/codes/magusfile/")
}

func TestDiagnosticError_Is(t *testing.T) {
	err := DiagnosticErrorf(PathReadDenied, "test")
	assert.ErrorIs(t, err, ErrDiag, "DiagnosticError should match ErrDiag")

	same := DiagnosticErrorf(PathReadDenied, "other")
	assert.ErrorIs(t, err, same, "DiagnosticError should match same-code DiagnosticError")

	other := DiagnosticErrorf(PathWriteDenied, "test")
	assert.NotErrorIs(t, err, other, "DiagnosticError should not match different-code DiagnosticError")
}

func TestDiagnosticErrorf(t *testing.T) {
	err := DiagnosticErrorf(EnvStripped, "var %s was stripped", "HOME")
	msg := err.Error()
	assert.Contains(t, msg, "MGS2003")
	assert.Contains(t, msg, "HOME")
}
