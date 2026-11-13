package command

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/libs/apiversion"
)

// CommandSpec is the spec of a Command record, a program's command tree:
// the Command it embeds is the root, which names the program.
type CommandSpec struct {
	Command `yaml:",inline"`
}

// Kind is Command.
func (CommandSpec) Kind() apiversion.Kind { return "Command" }

// APIVersion is clikit.command/v1alpha1.
func (CommandSpec) APIVersion() apiversion.APIVersion { return "clikit.command/v1alpha1" }

// Command is one node of a command tree. The root names the program; its Flags are
// the global flags, and its Files fill the root man page.
type Command struct {
	Name  string // command word
	Short string // one-line summary for SYNOPSIS, help and cross-references
	Long  string // DESCRIPTION body; plain text, paragraphs split on blank lines
	Usage string // SYNOPSIS line, e.g. "magus run <target> [flags] [project...]"

	// UnstructuredOutput means stdout belongs to a script, protocol, or terminal
	// rather than the -o renderer. Each child declares its own value.
	UnstructuredOutput bool

	// Effect is what running the command does outside its process. Each
	// child declares its own value; a Flag may override it.
	Effect Effect

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
	Float    Kind = "float"

	// OptionalBool is a three-state setting: unset, true or false.
	// No flag binds it, since a flag cannot say unset.
	OptionalBool Kind = "optional-bool"

	// StringList is a repeatable flag: each occurrence appends its value whole,
	// commas included, so a caller that splits a value decides how. It starts
	// empty and declares no Default.
	StringList Kind = "string-list"

	// Text is a setting whose type reads its own value as an
	// encoding.TextUnmarshaler. No command Flag declares it.
	Text Kind = "text"

	// StringMap is a string-to-string map only a config file sets. No
	// command Flag declares it.
	StringMap Kind = "string-map"

	// Custom is a flag declared here and bound by hand: a repeatable flag.Value, or
	// a mode selector read from argv before any FlagSet exists. The generator emits
	// only its name constant, since a second binding would panic as redefined.
	Custom Kind = "custom"
)

// Effect classifies what a command does outside its own process, so a
// reader can judge an argv without running it. The zero value is undeclared.
type Effect string

const (
	// EffectRead reads state and changes none.
	EffectRead Effect = "read"
	// EffectLocalWrite writes state only this checkout or user reads, such
	// as a job store entry, a config file or an installed skill.
	EffectLocalWrite Effect = "local-write"
	// EffectSharedWrite writes state others read, such as a remote branch,
	// a pull request, a queue or a running server.
	EffectSharedWrite Effect = "shared-write"
	// EffectRun executes workspace code, such as a target or a script,
	// whose own effects the tree cannot see.
	EffectRun Effect = "run"
	// EffectStream follows a source and prints until interrupted.
	EffectStream Effect = "stream"
	// EffectWait blocks until something outside the command finishes.
	EffectWait Effect = "wait"
)

// Effects lists every declared Effect, in the order above.
var Effects = []Effect{EffectRead, EffectLocalWrite, EffectSharedWrite, EffectRun, EffectStream, EffectWait}

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

	// Group is the heading -h prints this flag under, such as a mode's usage
	// line. Empty prints it first, under no heading.
	Group string

	// Unit is what a bare number means on a Duration flag (ms, s, m, h). Required
	// there so help can name it; empty on every other Kind.
	Unit string

	// Effect replaces the command's Effect when the flag holds a value
	// other than its Default. Empty keeps the command's Effect. An alias
	// takes the Effect of the flag it spells.
	Effect Effect
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
// with its generated binders. It fails on an unknown Kind, on a Default whose
// type does not match its Kind, and on what Check refuses.
//
// Each bool also gets its --no-<name> form, as the generated binders do.
func (c Command) BindFlags(fs *flag.FlagSet) error {
	if err := checkFlags(c.Name, c.Flags); err != nil {
		return err
	}
	for _, f := range c.Flags {
		switch f.Kind {
		case Bool:
			def, err := TypedDefault[bool](f)
			if err != nil {
				return err
			}
			p := fs.Bool(f.Name, def, f.Doc)
			if f.AliasOf == "" {
				NegatedBoolVar(fs, p, f.Name)
			}
		case String, Custom:
			def, err := TypedDefault[string](f)
			if err != nil {
				return err
			}
			fs.String(f.Name, def, f.Doc)
		case Int:
			def, err := TypedDefault[int](f)
			if err != nil {
				return err
			}
			fs.Int(f.Name, def, f.Doc)
		case Duration:
			def, err := TypedDefault[time.Duration](f)
			if err != nil {
				return err
			}
			unit, usage := f.Unit, f.Doc
			if f.AliasOf == "" {
				usage = DurationUsage(f)
			} else if unit == "" {
				for _, p := range c.Flags {
					if p.Name == f.AliasOf {
						unit = p.Unit
						break
					}
				}
			}
			DurationVar(fs, new(time.Duration), f.Name, def, unit, usage)
		case StringList:
			StringListVar(fs, new([]string), f.Name, f.Doc)
		default:
			return fmt.Errorf("flag --%s has unknown kind %s", f.Name, string(f.Kind))
		}
	}
	return nil
}

// Check reports every flag declaration in root's tree the generators refuse: a bool
// whose name starts with no- (declare the positive name; the generator adds the
// negation), a bool with no Default, and a bool whose --no-<name> form another flag
// of the same command already takes. It also reports an Effect, on a command
// or a flag, that is none of Effects.
func Check(root Command) error {
	errs := errors.Join(checkFlags(root.Name, root.Flags), checkEffects(root.Name, root))
	Walk(root, func(path string, c Command) {
		errs = errors.Join(errs, checkFlags(path, c.Flags), checkEffects(path, c))
	})
	return errs
}

func checkEffects(command string, c Command) error {
	var errs error
	if c.Effect != "" && !slices.Contains(Effects, c.Effect) {
		errs = errors.Join(errs, fmt.Errorf("command:%s declares Effect %q, which is none of %v", command, c.Effect, Effects))
	}
	for _, f := range c.Flags {
		if f.Effect != "" && !slices.Contains(Effects, f.Effect) {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s declares Effect %q, which is none of %v", command, f.Name, f.Effect, Effects))
		}
	}
	return errs
}

func checkFlags(command string, flags []Flag) error {
	declared := make(map[string]bool, len(flags))
	for _, f := range flags {
		declared[f.Name] = true
	}
	var errs error
	for _, f := range flags {
		if f.Kind == Duration && f.AliasOf == "" && f.Unit == "" {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s is a Duration with no Unit: declare the unit bare numbers take (ms, s, m, h), so help can name it", command, f.Name))
		}
		if f.Kind != Duration && f.Unit != "" {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s sets Unit %q on a %s flag: Unit is only for Duration", command, f.Name, f.Unit, f.Kind))
		}
		if f.Kind == Duration && f.Unit != "" && unitPhrase[f.Unit] == "" {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s Unit %q is not one of ns, us, µs, ms, s, m, h", command, f.Name, f.Unit))
		}
		if f.Kind == StringList && f.Default != nil {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s is a StringList with a Default: a repeatable flag starts empty", command, f.Name))
		}
		if f.Kind != Bool {
			continue
		}
		if strings.HasPrefix(f.Name, NegationPrefix) {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s is a bool named with %q: declare the positive name and let the generator emit the negation", command, f.Name, NegationPrefix))
		}
		if f.Default == nil && f.AliasOf == "" && negatable(f.Name) {
			errs = errors.Join(errs, fmt.Errorf("command:%s --%s is a bool with no Default: declare true or false", command, f.Name))
		}
		if f.AliasOf == "" && negatable(f.Name) && declared[NegationPrefix+f.Name] {
			errs = errors.Join(errs, fmt.Errorf("command:%s declares --%s, which is the negation of bool --%s", command, NegationPrefix+f.Name, f.Name))
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

// StringListVar registers name on fs as a [StringList] flag that appends each
// occurrence's value to *p. An empty or blank value is refused.
func StringListVar(fs *flag.FlagSet, p *[]string, name, usage string) {
	fs.Var(stringListValue{p: p}, name, usage)
}

type stringListValue struct{ p *[]string }

func (v stringListValue) String() string {
	if v.p == nil {
		return ""
	}
	return strings.Join(*v.p, ",")
}

func (v stringListValue) Set(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("empty entry")
	}
	*v.p = append(*v.p, s)
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
// another type is an error; see BindFlags.
func TypedDefault[T any](f Flag) (T, error) {
	var zero T
	if f.Default == nil {
		return zero, nil
	}
	v, ok := f.Default.(T)
	if !ok {
		return zero, fmt.Errorf("flag --%s is %s but its default %v is a %T", f.Name, f.Kind, f.Default, f.Default)
	}
	return v, nil
}
