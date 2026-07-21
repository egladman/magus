package types

import (
	"context"
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
	assert.Contains(t, CodeURL(PathReadDenied), "MGS2001")
	assert.Contains(t, CodeURL(RaceDetected), "MGS4001")
	assert.Contains(t, CodeURL(NoCITarget), "MGS1001")

	for _, code := range []DiagnosticCode{PathReadDenied, RaceDetected, NoCITarget} {
		assert.Truef(t, strings.HasSuffix(CodeURL(code), ".md"), "URL() = %q, want .md suffix", CodeURL(code))
	}
	// MGS1xxx routes to the magusfile docs dir, not the sandbox/race bases.
	assert.Contains(t, CodeURL(NoCITarget), "/docs/codes/magusfile/")
	// MGS5xxx routes to the services docs dir.
	assert.Contains(t, CodeURL(NearDuplicateServices), "MGS5001")
	assert.Contains(t, CodeURL(NearDuplicateServices), "/docs/codes/services/")
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

// A DiagnosticCode is itself an errors.Is sentinel (the idiomatic match form), alongside the
// same-code-error form.
func TestDiagnosticCodeSentinelMatch(t *testing.T) {
	err := DiagnosticErrorf(ExecDenied, "denied")
	assert.ErrorIs(t, err, ExecDenied, "a code is an errors.Is sentinel")
	assert.NotErrorIs(t, err, PathReadDenied, "a different code must not match")
}

// WrapDiagnostic carries a code AND unwraps to its cause, so a pre-existing sentinel keeps matching via
// errors.Is (the property that lets us add MGS1006 to the unknown-target error without breaking the
// ErrUnknownTarget fan-out suppression).
func TestWrapDiagnostic(t *testing.T) {
	sentinel := errors.New("magusfile: unknown target")
	err := WrapDiagnostic(UnknownTarget, sentinel, "unknown target %q (registered: %s)", "buld", "build, test")
	assert.ErrorIs(t, err, UnknownTarget, "carries the code")
	assert.ErrorIs(t, err, sentinel, "still unwraps to the wrapped sentinel")
	assert.Contains(t, err.Error(), "MGS1006")
	assert.Contains(t, err.Error(), "buld")
}

func TestFormatDiagnostic(t *testing.T) {
	got := FormatDiagnostic(NoCITarget, "no ci target")
	assert.Contains(t, got, "MGS1001")
	assert.Contains(t, got, "no ci target")
	assert.Contains(t, got, "see ") // the slog one-liner carries the doc URL inline
}

// captureSink records emitted diagnostics for the sink-plumbing test.
type captureSink struct{ events []DiagnosticEvent }

func (c *captureSink) Record(e DiagnosticEvent) { c.events = append(c.events, e) }

func TestDiagnosticSinkPlumbing(t *testing.T) {
	// No sink installed: EmitDiagnostic is a silent no-op.
	EmitDiagnostic(context.Background(), DiagnosticEvent{Code: ExecDenied})

	sink := &captureSink{}
	ctx := WithDiagnosticSink(context.Background(), sink)
	EmitDiagnostic(ctx, DiagnosticEvent{Code: ExecDenied, Unit: "a:build"})
	assert.Equal(t, []DiagnosticEvent{{Code: ExecDenied, Unit: "a:build"}}, sink.events)
}
