package cmdline

import (
	"flag"
	"fmt"
	"time"
)

// Command is one node of a command tree. The root names the program; its Flags are
// the global flags, and its Env and Files fill the root man page.
type Command struct {
	Name  string // command word
	Short string // one-line summary for SYNOPSIS, help and cross-references
	Long  string // DESCRIPTION body; plain text, paragraphs split on blank lines
	Usage string // SYNOPSIS line, e.g. "magus run <target> [flags] [project...]"

	// Description is the docs page's meta description; empty falls back to Short.
	Description string
	// Tags are the docs page's search keywords; empty falls back to "cli, <program>
	// <name>, <name>".
	Tags []string

	// Group is the heading top-level help files this command under.
	Group string

	// Flags are declared as data rather than as a closure that registers them, so a
	// generator can read the names ahead of time and emit them as constants.
	Flags []Flag

	Examples []Example
	Children []Command
	Operands []Operand // the words the command dispatches, such as `run`'s targets

	// ExitStatus documents codes a reader would otherwise guess wrong. Empty renders
	// no section: a command that follows the 0/1/2 contract needs none.
	ExitStatus []ExitCode

	Env   []Env  // ENVIRONMENT section of the root page
	Files []File // FILES section of the root page
}

// ExitCode is one documented exit status. One code may carry two meanings; say both
// in Meaning rather than listing the code twice.
type ExitCode struct {
	Code    int
	Meaning string // plain text
}

// Kind is the value type a flag binds, and so which flag.FlagSet method registers it.
type Kind string

const (
	Bool     Kind = "bool"
	String   Kind = "string"
	Int      Kind = "int"
	Duration Kind = "duration"

	// Custom is a flag declared here and bound by hand: a repeatable flag.Value, or
	// a mode selector read from argv before any FlagSet exists. The generator emits
	// only its name constant, since a second binding would panic as redefined.
	Custom Kind = "custom"
)

// Flag is one command-specific flag.
type Flag struct {
	Name string // without leading dashes, e.g. "no-cache"
	Kind Kind
	Doc  string // one-line help; a `quoted` word names the value in the man page

	// Default is the value the flag takes when unset, of the Go type its Kind binds:
	// bool, string, int or time.Duration. nil is the kind's zero value.
	Default any

	// DefaultAtBind means the real default is a runtime value, such as a config
	// setting, and Default is only what the docs show. The generated binder takes a
	// <Command>Defaults argument, so the caller cannot forget to supply it.
	DefaultAtBind bool

	// Modes lists the sub-modes that accept this flag; empty means the command's
	// base invocation. The generator emits one binder per mode, so a flag only one
	// mode honors is a parse error in the others rather than ignored.
	Modes []string

	// AliasOf names the flag this one is a second spelling of (-t for --test). Both
	// keep their help line, and the generated binder binds them to one variable.
	AliasOf string
}

// Operand is a word a command dispatches on, such as a target name.
type Operand struct {
	Name  string
	Short string
}

// Example is one entry of a man page's EXAMPLES section.
type Example struct {
	Comment string // e.g. "Build all Go projects"
	Command string // e.g. "magus run build"
}

// Env documents one environment variable the program reads.
type Env struct {
	Name    string
	Key     string // the config file key it mirrors; empty when it has none
	Default string // human-readable; empty when unset
	Doc     string
}

// File documents one file or directory the program reads or writes.
type File struct {
	Name string
	Doc  string
}

// Walk calls visit for every descendant of root, depth first, with its path below
// root ("config cache prune").
func Walk(root Command, visit func(path string, c Command)) {
	for _, c := range root.Children {
		walk(c.Name, c, visit)
	}
}

func walk(path string, c Command, visit func(string, Command)) {
	visit(path, c)
	for _, child := range c.Children {
		walk(path+" "+child.Name, child, visit)
	}
}

// BindFlags registers c's flags on fs to render or inspect them; the program parses
// with its generated binders. A Flag with an unknown Kind, or a Default whose type
// does not match its Kind, panics: the declaration is static data, and a wrong one
// would document a flag the program does not have.
func (c Command) BindFlags(fs *flag.FlagSet) {
	for _, f := range c.Flags {
		switch f.Kind {
		case Bool:
			fs.Bool(f.Name, DefaultOf[bool](f), f.Doc)
		case String, Custom:
			fs.String(f.Name, DefaultOf[string](f), f.Doc)
		case Int:
			fs.Int(f.Name, DefaultOf[int](f), f.Doc)
		case Duration:
			fs.Duration(f.Name, DefaultOf[time.Duration](f), f.Doc)
		default:
			panic("cmdline: flag --" + f.Name + " has unknown kind " + string(f.Kind))
		}
	}
}

// DefaultOf returns f.Default as T, or T's zero value when it is nil. A Default of
// another type panics; see BindFlags.
func DefaultOf[T any](f Flag) T {
	var zero T
	if f.Default == nil {
		return zero
	}
	v, ok := f.Default.(T)
	if !ok {
		panic(fmt.Sprintf("cmdline: flag --%s is %s but its default %v is a %T", f.Name, f.Kind, f.Default, f.Default))
	}
	return v
}
