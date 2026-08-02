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
	// Error() is the standalone sentence: an SDK consumer holding nothing but this
	// error still learns which target, in which project, and what broke.
	assert.Equal(t, "build failed in api/: exit 1", err.Error())
}

// TestSpellErrorsErrorPrefersTheProjectLabel guards the readability fix: a root
// project's Path is ".", and building the message from it produced "magus lint .:",
// where the dot reads as punctuation rather than as the project it names.
func TestSpellErrorsErrorPrefersTheProjectLabel(t *testing.T) {
	err := &SpellErrors{
		Project:      ".",
		ProjectLabel: "magus",
		Target:       "lint",
		Failed:       []SpellFailure{{Spell: "magusfile", Err: errors.New("markdownlint exited 1")}},
	}
	assert.Equal(t, "lint failed in magus: markdownlint exited 1", err.Error())
	assert.NotContains(t, err.Error(), ".:", "the bare-dot path must not reach the message")
}

// TestSpellErrorsCauseDropsWhatTheHeadingAlreadySaid pins the concise form the CLI
// logs. The project and target are printed in the heading directly above it, so
// repeating them there buried the one fact the line adds: the failing tool.
func TestSpellErrorsCauseDropsWhatTheHeadingAlreadySaid(t *testing.T) {
	err := &SpellErrors{
		Project: ".", ProjectLabel: "magus", Target: "lint",
		Failed: []SpellFailure{{Spell: "magusfile", Err: errors.New("markdownlint exited 1")}},
	}
	assert.Equal(t, "markdownlint exited 1", err.Cause())
	assert.Equal(t, "markdownlint exited 1", CauseText(err), "CauseText must prefer Cause")
}

// TestSpellErrorsCauseKeepsSpellsWhenSeveralFail is the other half of that rule: a
// lone failure needs no spell attribution, but across a fan-out the spell name is
// what tells the failures apart.
func TestSpellErrorsCauseKeepsSpellsWhenSeveralFail(t *testing.T) {
	err := &SpellErrors{
		Project: "web", Target: "ci",
		Failed: []SpellFailure{
			{Spell: "typescript", Err: errors.New("tsc exited 2")},
			{Spell: "markdown", Err: errors.New("markdownlint exited 1")},
		},
	}
	cause := err.Cause()
	assert.Contains(t, cause, "2 spells failed")
	assert.Contains(t, cause, "[typescript] tsc exited 2")
	assert.Contains(t, cause, "[markdown] markdownlint exited 1")
	assert.NotContains(t, cause, "spell(s)", "pluralization is real, not parenthesized")
}

// TestCauseTextFallsBackToTheFullMessage keeps CauseText total: an ordinary error
// has no concise form to prefer.
func TestCauseTextFallsBackToTheFullMessage(t *testing.T) {
	assert.Equal(t, "boom", CauseText(errors.New("boom")))
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

	// The URL addresses the rendered docs site, which serves each code as a directory,
	// not the Markdown source on the repo host. A reader clicks these while stuck, so
	// they should land on a styled, navigable page rather than a raw file view.
	for _, code := range []DiagnosticCode{PathReadDenied, RaceDetected, NoCITarget} {
		assert.Truef(t, strings.HasSuffix(CodeURL(code), "/"), "URL() = %q, want a directory URL", CodeURL(code))
		assert.NotContainsf(t, CodeURL(code), ".md", "URL() = %q, want no source extension", CodeURL(code))
	}
	// MGS1xxx routes to the magusfile docs dir, not the sandbox/race bases.
	assert.Contains(t, CodeURL(NoCITarget), "/reference/codes/magusfile/")
	// MGS5xxx routes to the services docs dir.
	assert.Contains(t, CodeURL(NearDuplicateServices), "MGS5001")
	assert.Contains(t, CodeURL(NearDuplicateServices), "/reference/codes/services/")
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
