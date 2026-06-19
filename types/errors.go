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

// ErrUnregisteredDep is returned by (*Workspace).Graph in Strict mode when a
// declared dependency path has not been registered.
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

// SpellError records the error from a single spell during multi-spell fan-out.
type SpellError struct {
	Spell string
	Err   error
}

// SpellErrors aggregates failures across multiple spells running the same target.
type SpellErrors struct {
	Project string
	Target  string
	Failed  []SpellError
}

func (e *SpellErrors) Error() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "magus %s %s: %d spell(s) failed\n", e.Target, e.Project, len(e.Failed))
	for _, f := range e.Failed {
		fmt.Fprintf(&sb, "  [%s] %s\n", f.Spell, f.Err)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// Unwrap satisfies errors.Is/As.
func (e *SpellErrors) Unwrap() []error {
	errs := make([]error, len(e.Failed))
	for i, f := range e.Failed {
		errs[i] = f.Err
	}
	return errs
}
