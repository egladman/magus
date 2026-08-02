package types

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is the canonical "miss" sentinel for I/O-backed lookups.
var ErrNotFound = errors.New("magus: not found")

// ErrSpellNotRegistered is returned by magus.WithSpell when the named spell is not registered.
var ErrSpellNotRegistered = errors.New("magus: spell not registered")

// ErrSpellNameRequired is returned by magus.WithSpell when called with an empty name.
var ErrSpellNameRequired = errors.New("magus: spell name required")

// ErrUnregisteredDep is returned by (*Workspace).Graph when a declared
// dependency path has not been registered.
var ErrUnregisteredDep = errors.New("magus: dependency not registered")

// UnregisteredDep is one missing-dep observation found while building the graph.
type UnregisteredDep struct {
	Consumer   string // project path that declared the dep
	Dep        string // dep path that did not resolve
	DidYouMean string // nearest registered path, or ""
}

// UnregisteredDepError aggregates every UnregisteredDep found during a Graph call.
type UnregisteredDepError struct {
	Missing []UnregisteredDep
}

// Error returns an end-user-readable description of every missing dependency.
func (e *UnregisteredDepError) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "magus: dependency not registered (%d unresolved)\n", len(e.Missing))
	for _, m := range e.Missing {
		if m.DidYouMean != "" {
			fmt.Fprintf(&sb, "  - %s -> %s   (did you mean: %s)\n", m.Consumer, m.Dep, m.DidYouMean)
		} else {
			fmt.Fprintf(&sb, "  - %s -> %s\n", m.Consumer, m.Dep)
		}
	}
	sb.WriteString("\nfix: configure the missing project(s) with magus.project(\"<path>\", {...})\n")
	sb.WriteString("     in a magusfile, or correct the path passed to magus.WithDependsOn\n")
	sb.WriteString("     in the consuming magusfile.")
	return sb.String()
}

// Is returns true for ErrUnregisteredDep.
func (*UnregisteredDepError) Is(target error) bool {
	return target == ErrUnregisteredDep
}

// SpellFailure records the error from a single spell during multi-spell fan-out.
type SpellFailure struct {
	Spell string
	Err   error
}

// SpellErrors aggregates failures across multiple spells running the same target.
type SpellErrors struct {
	Project string
	// ProjectLabel is the human name for Project, set from ProjectDisplayName where
	// the whole project is in hand. Project is the workspace-relative path, and for
	// a root project that path is ".", which rendered as "magus lint .:" - a bare
	// dot against a colon, which reads as punctuation rather than as the project it
	// actually names. Empty falls back to Project.
	ProjectLabel string
	Target       string
	Failed       []SpellFailure
}

func (e *SpellErrors) Error() string {
	return fmt.Sprintf("%s failed in %s: %s", e.Target, e.label(), e.Cause())
}

// Cause is the failure WITHOUT the project and target restated: which tool broke,
// and how.
//
// It exists because those two facts reach a reader twice. The CLI prints the
// project and target in the heading immediately above the cause line, so a cause
// that opened by repeating them ("magus lint .: 1 spell(s) failed [magusfile]
// magusfile: target lint: ...") spent its first two thirds on what the previous
// line already said, and buried the one thing it alone knew - the failing tool -
// at the end. Error() keeps the full sentence for an SDK consumer holding nothing
// but the error; the CLI logs this.
//
// A lone failure needs no spell attribution: the tool name is what a reader acts
// on, and which spell dispatched it is not. Fan-out across several spells keeps the
// bracketed list, where the spell is what tells the failures apart.
func (e *SpellErrors) Cause() string {
	if len(e.Failed) == 1 {
		return e.Failed[0].Err.Error()
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d spells failed", len(e.Failed))
	for _, f := range e.Failed {
		fmt.Fprintf(&sb, "\n  [%s] %s", f.Spell, f.Err)
	}
	return sb.String()
}

func (e *SpellErrors) label() string {
	if e.ProjectLabel != "" {
		return e.ProjectLabel
	}
	return e.Project
}

// CauseText is what a failure record carries as its cause: the concise form when
// the error has one, else the full message. Callers that have already printed the
// project and target use this so the cause adds information instead of echoing.
func CauseText(err error) string {
	var se *SpellErrors
	if errors.As(err, &se) {
		return se.Cause()
	}
	return err.Error()
}

// Unwrap satisfies errors.Is/As.
func (e *SpellErrors) Unwrap() []error {
	errs := make([]error, len(e.Failed))
	for i, f := range e.Failed {
		errs[i] = f.Err
	}
	return errs
}
