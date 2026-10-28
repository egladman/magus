package cmdline

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
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
	// bool, string, int or time.Duration. nil is the kind's zero value, except for a
	// Bool, which must declare true or false.
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
		walk(fmt.Sprintf("%s %s", path, child.Name), child, visit)
	}
}

// BindFlags registers c's flags on fs to render or inspect them; the program parses
// with its generated binders. A Flag with an unknown Kind, or a Default whose type
// does not match its Kind, panics: the declaration is static data, and a wrong one
// would document a flag the program does not have. So does a bool whose name starts
// with no- or that declares no Default; see Check.
//
// Each bool also gets its --no-<name> form, as the generated binders do.
func (c Command) BindFlags(fs *flag.FlagSet) {
	if err := checkFlags(c.Name, c.Flags); err != nil {
		panic(err.Error())
	}
	for _, f := range c.Flags {
		switch f.Kind {
		case Bool:
			p := fs.Bool(f.Name, TypedDefault[bool](f), f.Doc)
			if f.AliasOf == "" {
				NegatedBoolVar(fs, p, f.Name)
			}
		case String, Custom:
			fs.String(f.Name, TypedDefault[string](f), f.Doc)
		case Int:
			fs.Int(f.Name, TypedDefault[int](f), f.Doc)
		case Duration:
			fs.Duration(f.Name, TypedDefault[time.Duration](f), f.Doc)
		default:
			panic(fmt.Sprintf("cmdline: flag --%s has unknown kind %s", f.Name, string(f.Kind)))
		}
	}
}

// Check reports every flag declaration in root's tree the generators refuse: a bool
// whose name starts with no- (declare the positive name; the generator adds the
// negation), a bool with no Default, and a bool whose --no-<name> form another flag
// of the same command already takes.
func Check(root Command) error {
	errs := checkFlags(root.Name, root.Flags)
	Walk(root, func(path string, c Command) {
		if err := checkFlags(path, c.Flags); err != nil {
			errs = errors.Join(errs, err)
		}
	})
	return errs
}

func checkFlags(command string, flags []Flag) error {
	declared := make(map[string]bool, len(flags))
	for _, f := range flags {
		declared[f.Name] = true
	}
	var errs error
	for _, f := range flags {
		if f.Kind != Bool {
			continue
		}
		if strings.HasPrefix(f.Name, NegationPrefix) {
			errs = errors.Join(errs, fmt.Errorf("cmdline: %s --%s is a bool named with %q: declare the positive name and let the generator emit the negation", command, f.Name, NegationPrefix))
		}
		if f.Default == nil && f.AliasOf == "" && negatable(f.Name) {
			errs = errors.Join(errs, fmt.Errorf("cmdline: %s --%s is a bool with no Default: declare true or false", command, f.Name))
		}
		if f.AliasOf == "" && negatable(f.Name) && declared[NegationPrefix+f.Name] {
			errs = errors.Join(errs, fmt.Errorf("cmdline: %s declares --%s, which is the negation of bool --%s", command, NegationPrefix+f.Name, f.Name))
		}
	}
	return errs
}

// NegationPrefix turns a bool's positive name into its off spelling.
const NegationPrefix = "no-"

// negatable reports whether a bool named name has a --no- form. A one-character
// name is a short flag (-v, -q); --no-v reads as nothing.
func negatable(name string) bool { return len(name) > 1 }

// NegatedBoolVar registers --no-<name> on fs so that it clears *p. It takes no
// value: --no-<name>=false is refused, because "not not" is a second way to say
// --<name> and the two readings disagree about which the caller meant.
// A one-character name registers nothing.
func NegatedBoolVar(fs *flag.FlagSet, p *bool, name string) {
	if !negatable(name) {
		return
	}
	fs.Var(&negation{dst: p, name: name}, NegationPrefix+name, "Turn off --"+name)
}

type negation struct {
	dst  *bool
	name string
}

func (n *negation) IsBoolFlag() bool { return true }

// String is the value --no-<name> would leave in the flag, which flag reads on a
// zero negation to decide whether to print a default.
func (n *negation) String() string {
	if n == nil || n.dst == nil {
		return ""
	}
	return strconv.FormatBool(!*n.dst)
}

func (n *negation) Set(s string) error {
	if s != "true" {
		return fmt.Errorf("--%s%s takes no value: write --%s to turn it on", NegationPrefix, n.name, n.name)
	}
	*n.dst = false
	return nil
}

// Help is one flag as help, man and markdown print it.
type Help struct {
	Name    string // as typed, such as "--[no-]cache" or "-c"
	Value   string // the value's placeholder; empty for a bool
	Usage   string
	Default string // "on" or "off" for a negatable bool; the declared value otherwise; empty when zero
}

// FlagHelp describes the flags on fs in name order, skipping those skip names and
// folding each --no-<name> into its positive flag as "--[no-]<name>".
func FlagHelp(fs *flag.FlagSet, skip func(name string) bool) []Help {
	var out []Help
	fs.VisitAll(func(f *flag.Flag) {
		if _, isNegation := f.Value.(*negation); isNegation || (skip != nil && skip(f.Name)) {
			return
		}
		h := Help{Name: dashes(f.Name) + f.Name}
		if isBool(f) {
			h.Usage = strings.ReplaceAll(f.Usage, "`", "")
			if g := fs.Lookup(NegationPrefix + f.Name); g != nil {
				if _, ok := g.Value.(*negation); ok {
					h.Name = "--[no-]" + f.Name
					h.Default = "off"
					if f.DefValue == "true" {
						h.Default = "on"
					}
				}
			}
		} else {
			h.Value, h.Usage = flag.UnquoteUsage(f)
			switch f.DefValue {
			case "", "0", "0s", "false":
			default:
				h.Default = f.DefValue
			}
		}
		out = append(out, h)
	})
	return out
}

func dashes(name string) string {
	if len(name) == 1 {
		return "-"
	}
	return "--"
}

func isBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// TypedDefault returns f.Default as T, or T's zero value when it is nil. A Default of
// another type panics; see BindFlags.
func TypedDefault[T any](f Flag) T {
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
