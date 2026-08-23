// Package clispec is the declarative specification of the magus CLI: every
// subcommand, its flags, usage, examples and prose, as data.
//
// It is the single source those facts have. The CLI binds its flags from it (via
// the generated binders in cmd/magus/gen), cmd/magus-manpage renders it to roff
// and Markdown, cmd/magus-utils emits the flag-name constants and the api.lock
// from it, the shell completions are generated from it, and the browser terminal
// reads it to complete and explain commands it cannot run.
//
// It was called "manpage" while the man pages were its only consumer. The name
// outlived that by five consumers: a package a WASM terminal imports to learn the
// CLI should not be named after one of its renderings. The roff rendering lives
// here still, as one output among several rather than as the package's purpose.
package clispec

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

	// ExitStatus documents the codes this command exits with, in ascending order.
	// Empty renders no section at all, which is the right answer for a command that
	// only ever follows the CLI-wide contract (0 success, 1 failure, 2 misuse) - a
	// section repeating that on ninety pages teaches nobody anything.
	//
	// Populate it where a reader would otherwise guess WRONG: a code carrying a
	// command-specific meaning, or a code a neighbouring tool has trained them to
	// expect and this one does not use.
	ExitStatus []ExitCode
}

// ExitCode is one documented exit status.
//
// Codes are not unique across a command's list only because they are unique in
// what they mean: `magus session hook` exits 2 both for a denied command and for input it
// could not parse. Where one code carries two meanings, say both in Meaning rather
// than listing the code twice, which reads as a bug in the table.
type ExitCode struct {
	Code    int    // the process exit status
	Meaning string // what it tells the caller; plain text, no roff or Markdown markup
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

	// FlagCustom is a flag this package declares but does not bind: a repeatable
	// flag.Value like `magus watch --ignore`, or a MODE SELECTOR like `magus
	// affected --plan`, which is read straight from argv before any FlagSet
	// exists because it chooses which FlagSet to build.
	//
	// DECLARED here, BOUND by hand (or by nothing). The command keeps its fs.Var call, and the
	// generator emits only the name constant - no struct field and no second
	// binding, which would be a "flag redefined" panic. What the declaration buys
	// is documentation: --ignore, --ref and --reference were each bound by a
	// command and absent from every man page, because a flag the registry could
	// not express was simply left out of it.
	FlagCustom FlagKind = "custom"
)

// Flag is one command-specific flag.
type Flag struct {
	Name string   // without leading dashes, e.g. "no-cache"
	Kind FlagKind // which FlagSet method binds it
	Doc  string   // one-line help text

	// Default is the zero value the flag takes when unset: bool, string, int, or
	// time.Duration, matching Kind. A nil Default means the kind's zero value.
	Default any

	// DefaultAtBind means the real default is a runtime value the caller supplies,
	// and Default is only what the docs should show.
	//
	// Some defaults are not literals: --max-shards falls back to the configured
	// ci.max_shards, --budget to a package constant. Written as data, those became
	// hardcoded numbers in the generated binder that DISAGREED with the live
	// binding, silently dropping config support for anything that adopted it. A
	// generated binder for such a command takes a <Command>Defaults argument, so
	// the caller cannot forget to supply the value - leaving it out is a compile
	// error rather than a wrong default nobody notices.
	//
	// Default should still carry the documented value (usually the config default),
	// because the man page has to print something and printing 0 would be a lie.
	DefaultAtBind bool

	// Modes lists the sub-modes that accept this flag; empty means the command's
	// base invocation.
	//
	// `magus affected` is one command with four parses: the run itself, --plan,
	// --impact and --bisect. They are not a base set plus extras - each binds its
	// own set, overlapping the others (--base is accepted by three of them,
	// --max-shards only by --plan). Declared as one merged list, a generated
	// binder for the base parse would accept --max-shards and ignore it, turning
	// a caught mistake into a silent one.
	//
	// A flag accepted by several modes lists them, so it is still DECLARED once.
	// The generator emits one binder per mode.
	Modes []string

	// AliasOf names the flag this one is a second spelling of: --test is AliasOf
	// "t", -b is AliasOf "base". Both names stay real flags and both keep their own
	// help line; what the alias does NOT get is its own value.
	//
	// This is the difference between documenting a shorthand and binding one. The
	// hand-written CLI binds a pair to ONE variable, so -t and --test are the same
	// switch. Modeled as two independent flags, a generated binder gave them two
	// destinations, and setting one left the other false - the shorthand parsed and
	// then did nothing. Every generated struct field is a GROUP: the primary plus
	// its aliases, bound to a single address.
	AliasOf string

	// No Enum field, deliberately. Every closed-set value in this CLI belongs to
	// the GLOBAL -o flag, which this registry does not model (see Flags above), and
	// no per-command flag has one - the string flags here take refs, paths, commit
	// SHAs and queries. The -o list is enumerated once at its real source, the
	// CommonFormats slice in cmd/magus/output.go; an unused Enum here would be a
	// second place to declare a set nothing in this package owns.
}

// Target is a named unit of work a project spell implements (e.g. "build", "test", "lint").
//
// No Flags: a target is dispatched by the command it hangs off, and never carried
// its own flag list - every Target literal in the registry sets Name and Short only.
type Target struct {
	Name  string
	Short string
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

// HasFlags reports whether anything would be registered, so a caller can skip
// building a FlagSet it would not use.
func (c Command) HasFlags() bool { return len(c.Flags) > 0 }

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
		case FlagCustom:
			// Registered as a string for RENDERING only: this FlagSet is the one the
			// man-page and docs generators walk, never the one the command parses
			// with, so this decides how the flag is printed and nothing else.
			fs.String(f.Name, defaultOf[string](f.Default), f.Doc)
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
