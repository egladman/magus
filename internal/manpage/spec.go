// Package manpage defines the Command and Target types used by both the
// magus CLI and the man-page generator (cmd/magus-manpage).
package manpage

import (
	"flag"
	"time"
)

// Command is one node in the recursive magus CLI command tree (e.g. "run", "config", "ci github").
type Command struct {
	Name  string // command word
	Short string // one-line summary for SYNOPSIS and cross-references
	Long  string // multi-paragraph DESCRIPTION body; plain text, no roff markup
	Usage string // SYNOPSIS line, e.g. "magus run <target> [flags] [project...]"

	// Description is the ~120-155 char meta-description emitted into the
	// generated docs page's YAML frontmatter (used for search index + <meta
	// name="description">). Falls back to Short if empty.
	Description string
	// Tags list keywords for the docs page's YAML frontmatter (search index).
	// Auto-augmented with the canonical "cli, magus <name>, <name>" if empty.
	Tags []string

	// Flags are the command-specific flags, as DATA rather than as a closure that
	// registers them.
	//
	// The closure this replaced could only ever be REPLAYED into a FlagSet, so the
	// only question anything could ask of it was "what did you just register" - the
	// answer arrived at runtime, one FlagSet at a time. Nothing could ask for the
	// names ahead of time, which is why a flag name reached the rest of the binary
	// as a string literal, and why the man page's copy of the list could disagree
	// with the CLI's until a test noticed. A slice can be read at generate time, so
	// the names become constants and the two lists become one.
	//
	// Do NOT declare global flags here (--output, -v, --concurrency, --root, --config).
	Flags []Flag

	Examples []Example // EXAMPLES section entries
	Children []Command // navigational subcommands (e.g. "github" under "ci")
	Targets  []Target  // project-scoped targets dispatched by this command
}

// FlagKind is the value type a flag binds, and so which flag.FlagSet method
// registers it.
//
// A defined STRING rather than an iota int, matching the boundary enums elsewhere
// in this module. An iota kind needs a generated String() before it can be printed,
// read or diffed, and until it has one every error about a flag renders its type as
// a bare integer. Spelling the value is the whole of what a Stringer would have
// added, so the type is its own representation and there is nothing to keep in sync.
type FlagKind string

const (
	FlagBool     FlagKind = "bool"
	FlagString   FlagKind = "string"
	FlagInt      FlagKind = "int"
	FlagDuration FlagKind = "duration"
)

// Flag is one command-specific flag.
type Flag struct {
	Name string   // without leading dashes, e.g. "no-cache"
	Kind FlagKind // which FlagSet method binds it
	Doc  string   // one-line help text

	// Default is the zero value the flag takes when unset: bool, string, int, or
	// time.Duration, matching Kind. A nil Default means the kind's zero value.
	Default any

	// AliasOf names the flag this one is a second spelling of: --test is AliasOf
	// "t", -b is AliasOf "base". Both names stay real flags and both keep their own
	// help line; what the alias does NOT get is its own value.
	//
	// This is the difference between documenting a shorthand and binding one. The
	// hand-written CLI binds a pair to ONE variable, so -t and --test are the same
	// switch. Modelled as two independent flags, a generated binder gave them two
	// destinations, and setting one left the other false - the shorthand parsed and
	// then did nothing. Every generated struct field is a GROUP: the primary plus
	// its aliases, bound to a single address.
	AliasOf string

	// No Enum field, deliberately. Every closed-set value in this CLI belongs to
	// the GLOBAL -o flag, which this registry does not model (see Flags above), and
	// no per-command flag has one - the string flags here take refs, paths, commit
	// SHAs and queries. The -o list is enumerated once at its real source
	// (main.CommonFormats, rendered by main.FormatChoices); an unused Enum here
	// would be a second place to declare a set nothing in this package owns.
}

// Target is a named unit of work a project spell implements (e.g. "build", "test", "lint").
type Target struct {
	Name  string
	Short string
	Flags []Flag
}

// Example is a single usage example in the man-page EXAMPLES section.
type Example struct {
	Comment string // e.g. "Build all Go projects"
	Command string // e.g. "magus run build"
}

// BindFlags registers c's flags on fs.
//
// This is the one place a declared Flag becomes a bound flag, so the man page,
// the docs, the completions and the CLI all bind the same list by construction
// rather than by a test comparing two hand-written copies.
func (c Command) BindFlags(fs *flag.FlagSet) { bindFlags(fs, c.Flags) }

// BindFlags registers t's flags on fs.
func (t Target) BindFlags(fs *flag.FlagSet) { bindFlags(fs, t.Flags) }

// HasFlags reports whether anything would be registered, so a caller can skip
// building a FlagSet it would not use.
func (c Command) HasFlags() bool { return len(c.Flags) > 0 }

// HasFlags reports whether anything would be registered.
func (t Target) HasFlags() bool { return len(t.Flags) > 0 }

func bindFlags(fs *flag.FlagSet, flags []Flag) {
	for _, f := range flags {
		switch f.Kind {
		case FlagBool:
			fs.Bool(f.Name, defaultOf[bool](f.Default), f.Doc)
		case FlagString:
			fs.String(f.Name, defaultOf[string](f.Default), f.Doc)
		case FlagInt:
			fs.Int(f.Name, defaultOf[int](f.Default), f.Doc)
		case FlagDuration:
			fs.Duration(f.Name, defaultOf[time.Duration](f.Default), f.Doc)
		default:
			// No silent fallback. An unset or misspelled Kind used to land on the
			// string case, which binds a flag that parses but never means what it
			// says; this is static in-repo data bound by every man-page and drift
			// test, so a panic here fails generation rather than shipping.
			panic("manpage: flag --" + f.Name + " has unknown kind " + string(f.Kind))
		}
	}
}

// defaultOf reads a Flag.Default of the expected type, falling back to the zero
// value. A mismatched type is treated as unset rather than panicking: a wrong
// default is a documentation bug, and failing the whole CLI over one would be a
// worse outcome than binding the zero the field would have had anyway.
func defaultOf[T any](v any) T {
	if t, ok := v.(T); ok {
		return t
	}
	var zero T
	return zero
}

// Names returns the declared flag names in order, for callers that need to
// reference a flag without spelling it as a literal.
func Names(flags []Flag) []string {
	out := make([]string, 0, len(flags))
	for _, f := range flags {
		out = append(out, f.Name)
	}
	return out
}
