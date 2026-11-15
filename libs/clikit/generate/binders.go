package generate

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/configuration"
)

// Binders renders gofmt'd Go source for package pkg: a constant per flag of every
// command below root, so a caller never spells a flag name as a literal, and a
// Bind<Command> function per command and mode that binds its flags to a struct.
//
// Constants are prefixed by command because a bare flag name is not unique, and a
// shared constant would quietly tie two commands' flags together. An unknown Kind or
// a mistyped Default is an error, as command.Command.BindFlags reports it.
func Binders(root command.Command, pkg string, opts ...BindersOption) ([]byte, error) {
	if err := command.Check(root); err != nil {
		return nil, fmt.Errorf("binders: %w", err)
	}
	var o bindersOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.settings != nil {
		if err := o.target.check(); err != nil {
			return nil, fmt.Errorf("binders: %w", err)
		}
	}
	var b bytes.Buffer
	b.WriteString(generatedHeader)
	fmt.Fprintf(&b, `package %s

`, pkg)
	b.WriteString("import (\n\t\"flag\"\n\t\"time\"\n\n\t\"github.com/egladman/magus/libs/clikit/command\"\n")
	if o.settings != nil {
		fmt.Fprintf(&b, "\t%q\n", o.target.Import)
	}
	b.WriteString(")\n\n")
	b.WriteString("var _ = command.NegationPrefix // a tree with no bool still imports command\n\n")
	b.WriteString("// Flag names declared by the command tree, one constant\n")
	b.WriteString("// each so a caller never spells a flag as a literal.\n")
	b.WriteString("const (\n")

	seen := map[string]string{}
	var lines []string
	var walkErr error
	command.Walk(root, func(path string, cmd command.Command) {
		if walkErr != nil {
			return
		}
		consts, err := renderConsts(path, cmd.Flags, seen)
		if err != nil {
			walkErr = err
			return
		}
		lines = append(lines, consts...)
	})
	if walkErr != nil {
		return nil, walkErr
	}
	slices.Sort(lines)
	for _, l := range lines {
		b.WriteString(l)
	}
	b.WriteString(")\n")

	command.Walk(root, func(path string, cmd command.Command) {
		for _, mode := range flagModes(cmd.Flags) {
			if walkErr != nil {
				return
			}
			name := path
			if mode != "" {
				name += " " + mode
			}
			walkErr = writeBinder(&b, name, path, flagsForMode(cmd.Flags, mode))
		}
	})
	if walkErr != nil {
		return nil, walkErr
	}
	if o.settings != nil {
		if err := writeConfigBinder(&b, o.settings, o.target); err != nil {
			return nil, err
		}
	}
	return gofmt("binders", b.Bytes())
}

// BindersOption adjusts Binders.
type BindersOption func(*bindersOptions)

type bindersOptions struct {
	settings []configuration.Setting
	target   Target
}

// WithSettings adds BindFlags, which binds a flag per setting that has one
// to its field of a *target.Type.
func WithSettings(settings []configuration.Setting, target Target) BindersOption {
	return func(o *bindersOptions) { o.settings, o.target = settings, target }
}

// flagModes lists the distinct sub-modes declared across a command's flags, base
// ("") first. A command whose flags declare no Modes yields just the base, so it
// keeps a single binder.
func flagModes(flags []command.Flag) []string {
	out := []string{""}
	seen := map[string]bool{"": true}
	for _, f := range flags {
		for _, m := range f.Modes {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	return out
}

// flagsForMode selects the flags one mode accepts: those declaring it, plus (for
// the base) those declaring no mode at all.
func flagsForMode(flags []command.Flag, mode string) []command.Flag {
	var out []command.Flag
	for _, f := range flags {
		if len(f.Modes) == 0 {
			if mode == "" {
				out = append(out, f)
			}
			continue
		}
		for _, m := range f.Modes {
			if m == mode {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// writeBinder emits the typed struct and the Bind function for one command.
//
// A struct of typed fields rather than a name-keyed lookup, because the hand-written
// binding it replaces already had typed destinations (a local var per flag, passed
// to fs.BoolVar and friends), and a lookup would have made every read site worse to
// buy nothing. The field names are derived from the flag names, so the struct is the
// flag list: adding a flag to the registry adds a field, and removing one breaks
// every read of it at compile time instead of leaving a silently-always-false bool.
// binder names the emitted type and function ("affected plan"); constCommand
// names the command whose constants its flags resolve to ("affected"). They
// differ only for a sub-mode: a mode binds a subset of ONE command's flags, so
// --base is a single FlagAffectedBase however many modes accept it.
func writeBinder(b *bytes.Buffer, cmdPath, constCommand string, flags []command.Flag) error {
	if len(flags) == 0 {
		return nil
	}
	// A custom-valued flag is declared for the docs and bound by the command
	// itself; emitting a field and a second fs.Var for it would panic at parse
	// time with "flag redefined". Its name constant still comes out, via renderConsts.
	bindable := make([]command.Flag, 0, len(flags))
	var custom []string
	for _, f := range flags {
		if f.Kind == command.Custom {
			custom = append(custom, "--"+f.Name)
			continue
		}
		bindable = append(bindable, f)
	}
	if len(bindable) == 0 {
		return nil
	}
	groups, err := groupAliases(cmdPath, bindable)
	if err != nil {
		return err
	}

	// The struct SAYS what it cannot carry, because a caller that binds it and stops
	// there gets a command whose custom flags parse into nothing and still compiles.
	partial := ""
	if len(custom) > 0 {
		partial = fmt.Sprintf("\n//\n// It does NOT carry %s: a custom-valued flag is bound by the command itself,\n// which must do so alongside this binder.", strings.Join(custom, ", "))
	}
	typeName := goIdent(cmdPath) + "Flags"
	fmt.Fprintf(b, "\n// %sFlags are the flags declared for `magus %s`.%s\ntype %s struct {\n", goIdent(cmdPath), cmdPath, partial, typeName)
	for _, g := range groups {
		typ, err := goType(g.primary.Kind)
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "\t%s %s // %s\n", g.field, typ, g.spellings())
	}
	b.WriteString("}\n\n")

	// Commands with a runtime default take a Defaults argument. Emitted only when one
	// is declared, so the common command keeps a one-argument binder.
	var runtime []aliasGroup
	for _, g := range groups {
		if g.primary.DefaultAtBind {
			runtime = append(runtime, g)
		}
	}
	defaultsType := goIdent(cmdPath) + "Defaults"
	param := ""
	if len(runtime) > 0 {
		fmt.Fprintf(b, "// %s carries the defaults `magus %s` resolves at runtime (config, a\n// package constant) rather than declaring as a literal.\ntype %s struct {\n", defaultsType, cmdPath, defaultsType)
		for _, g := range runtime {
			typ, err := goType(g.primary.Kind)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "\t%s %s // %s\n", g.field, typ, g.spellings())
		}
		b.WriteString("}\n\n")
		param = ", d " + defaultsType
	}

	fmt.Fprintf(b, "// Bind%s registers `magus %s`'s flags on fs and returns the destination.\nfunc Bind%s(fs *flag.FlagSet%s) *%s {\n\tvar f %s\n", goIdent(cmdPath), cmdPath, goIdent(cmdPath), param, typeName, typeName)
	for _, g := range groups {
		def := "d." + g.field
		if !g.primary.DefaultAtBind {
			var err error
			if def, err = flagDefaultLiteral(g.primary); err != nil {
				return err
			}
		}
		for _, name := range g.names {
			usage := flagDoc(flags, name)
			if g.primary.Kind == command.StringList {
				fmt.Fprintf(b, "\tcommand.StringListVar(fs, &f.%s, %s, %q)\n",
					g.field, fmt.Sprintf("Flag%s%s", goIdent(constCommand), goIdent(name)), usage)
				continue
			}
			if g.primary.Kind == command.Duration {
				if name == g.primary.Name {
					usage = command.DurationUsage(g.primary)
				}
				fmt.Fprintf(b, "\tcommand.DurationVar(fs, &f.%s, %s, %s, %q, %q)\n",
					g.field, fmt.Sprintf("Flag%s%s", goIdent(constCommand), goIdent(name)), def, g.primary.Unit, usage)
				continue
			}
			method, err := bindMethod(g.primary.Kind)
			if err != nil {
				return err
			}
			fmt.Fprintf(b, "\tfs.%sVar(&f.%s, %s, %s, %q)\n",
				method, g.field, fmt.Sprintf("Flag%s%s", goIdent(constCommand), goIdent(name)), def, usage)
		}
		if g.primary.Kind == command.Bool {
			fmt.Fprintf(b, "\tcommand.NegatedBoolVar(fs, &f.%s, Flag%s%s)\n", g.field, goIdent(constCommand), goIdent(g.primary.Name))
		}
	}
	b.WriteString("\treturn &f\n}\n")
	return nil
}

// aliasGroup is one destination and every flag name that writes to it.
type aliasGroup struct {
	primary command.Flag
	names   []string // primary first, then its aliases, in declaration order
	field   string
}

// spellings renders the group's names as a struct-field comment: "--watch, -W".
func (g aliasGroup) spellings() string {
	out := make([]string, 0, len(g.names))
	for i, n := range g.names {
		spelling := dashes(n) + n
		if i == 0 && g.primary.Kind == command.Bool && len(n) > 1 {
			spelling = "--[no-]" + n
		}
		out = append(out, spelling)
	}
	return strings.Join(out, ", ")
}

// dashes picks the prefix a name is actually invoked with. Go's flag package
// accepts either, but a one-character flag is written -c everywhere in this CLI's
// help and docs, and the generated comment should read the way the flag is typed.
func dashes(name string) string {
	if len(name) == 1 {
		return "-"
	}
	return "--"
}

// groupAliases folds each Flag.AliasOf into its primary, so the binder emits one
// destination per group rather than one per name.
//
// Order is the declaration order of the primaries; an alias never introduces a
// group. A dangling or mistyped AliasOf is a panic rather than a silent extra
// field, because the failure it would otherwise produce is a shorthand that parses
// and does nothing, the exact bug this concept was added to prevent.
func groupAliases(cmdPath string, flags []command.Flag) ([]aliasGroup, error) {
	index := make(map[string]command.Flag, len(flags))
	for _, f := range flags {
		index[f.Name] = f
	}

	var groups []aliasGroup
	at := make(map[string]int, len(flags))
	for _, f := range flags {
		if f.AliasOf != "" {
			continue
		}
		at[f.Name] = len(groups)
		groups = append(groups, aliasGroup{primary: f, names: []string{f.Name}})
	}
	for _, f := range flags {
		if f.AliasOf == "" {
			continue
		}
		primary, ok := index[f.AliasOf]
		if !ok {
			return nil, fmt.Errorf("%s --%s is AliasOf %q, which it does not declare", cmdPath, f.Name, f.AliasOf)
		}
		if primary.AliasOf != "" {
			return nil, fmt.Errorf("%s --%s aliases --%s, which is itself an alias", cmdPath, f.Name, f.AliasOf)
		}
		if primary.Kind != f.Kind {
			return nil, fmt.Errorf("%s --%s is %s but aliases --%s, which is %s", cmdPath, f.Name, f.Kind, f.AliasOf, primary.Kind)
		}
		i := at[f.AliasOf]
		groups[i].names = append(groups[i].names, f.Name)
	}

	// The field is named for the LONGEST spelling in the group, which is the one
	// written to be read: a group of {t, test} yields Test, not T. Naming it after
	// the primary instead would hand the reader f.T at every use site purely because
	// the short form happens to be the one the help text treats as canonical.
	for i, g := range groups {
		longest := g.names[0]
		for _, n := range g.names {
			if len(n) > len(longest) {
				longest = n
			}
		}
		groups[i].field = goIdent(longest)
	}
	return groups, nil
}

// flagDoc returns the help text declared for one flag name.
func flagDoc(flags []command.Flag, name string) string {
	if f, ok := flagByName(flags, name); ok {
		return f.Doc
	}
	return ""
}

func flagByName(flags []command.Flag, name string) (command.Flag, bool) {
	for _, f := range flags {
		if f.Name == name {
			return f, true
		}
	}
	return command.Flag{}, false
}

func goType(k command.Kind) (string, error) {
	switch k {
	case command.Bool:
		return "bool", nil
	case command.Int:
		return "int", nil
	case command.Duration:
		return "time.Duration", nil
	case command.String:
		return "string", nil
	case command.StringList:
		return "[]string", nil
	}
	return "", fmt.Errorf("unknown flag kind %s", k)
}

func bindMethod(k command.Kind) (string, error) {
	switch k {
	case command.Bool:
		return "Bool", nil
	case command.Int:
		return "Int", nil
	case command.Duration:
		return "Duration", nil
	case command.String:
		return "String", nil
	}
	return "", fmt.Errorf("unknown flag kind %s", k)
}

// flagDefaultLiteral renders a Flag's default as Go source for the generated binder.
// A nil Default is the kind's zero. A Default of another Go type is an error.
func flagDefaultLiteral(f command.Flag) (string, error) {
	switch f.Kind {
	case command.Bool:
		v, err := command.TypedDefault[bool](f)
		return fmt.Sprintf("%t", v), err
	case command.Int:
		v, err := command.TypedDefault[int](f)
		return fmt.Sprintf("%d", v), err
	case command.Duration:
		v, err := command.TypedDefault[time.Duration](f)
		if err != nil {
			return "", err
		}
		if v != 0 {
			return fmt.Sprintf("time.Duration(%d)", int64(v)), nil
		}
		return "0", nil
	case command.String:
		v, err := command.TypedDefault[string](f)
		return fmt.Sprintf("%q", v), err
	case command.StringList:
		return "nil", nil
	}
	return "", fmt.Errorf("unknown flag kind %s", f.Kind)
}

// renderConsts renders one command's flags, skipping a name it has already emitted so
// a command listed twice cannot produce a duplicate declaration.
func renderConsts(command string, flags []command.Flag, seen map[string]string) ([]string, error) {
	var out []string
	for _, f := range flags {
		name := fmt.Sprintf("Flag%s%s", goIdent(command), goIdent(f.Name))
		if prev, dup := seen[name]; dup {
			if prev == f.Name {
				continue
			}
			return nil, fmt.Errorf("%s would name both %q and %q", name, prev, f.Name)
		}
		seen[name] = f.Name
		out = append(out, fmt.Sprintf("\t// %s: --%s\n\t%s = %q\n", command, f.Name, name, f.Name))
	}
	return out, nil
}

// initialisms are the words Go spells in full caps, as a subset of the list Go's
// own linters carry (x/lint's commonInitialisms, which staticcheck's ST1003
// inherited), plus the two this workspace's vocabulary adds.
//
// A table rather than a library call because there is no algorithm here: no
// transformation derives "URL" from "url"; it is a convention list, and every
// package that offers this ships the same hardcoded map or asks the caller to
// register one. golang.org/x/text/cases does LANGUAGE casing, and titling "url"
// with it produces "Url", the output this exists to prevent.
//
// It matters more here than in hand-written code because these identifiers are
// EXPORTED constants and struct fields: a generated "Url" cannot be fixed at the
// place it is read.
var initialisms = map[string]string{
	"url": "URL", "vcs": "VCS", "id": "ID", "api": "API",
	"ci": "CI", "json": "JSON", "yaml": "YAML", "tty": "TTY", "mcp": "MCP",
}

// FieldName is the Go identifier the generated binders give flag name, so a
// vocabulary check can hold the Go option fields to the same spelling.
func FieldName(name string) string { return goIdent(name) }

// goIdent turns a command or flag name into an exported Go identifier fragment:
// "no-default-charms" becomes "NoDefaultCharms", "graph deps" becomes "GraphDeps".
//
// Each word is title-cased with the rest lowercased, so the one-character flags -c and
// -C collapse to the same fragment; renderConsts's error catches that at generate time
// rather than emitting one constant for two flags.
func goIdent(s string) string {
	var b strings.Builder
	for _, part := range words(s) {
		if up, ok := initialisms[strings.ToLower(part)]; ok {
			b.WriteString(up)
			continue
		}
		b.WriteString(strings.ToUpper(part[:1]) + strings.ToLower(part[1:]))
	}
	return b.String()
}

// words splits s at every rune that is not a letter or digit, and between a letter
// and a digit or a lower and an upper case letter: "max-shards2" is max, shards, 2.
func words(s string) []string {
	var out []string
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, s[start:end])
		}
		start = -1
	}
	var prev rune
	for i, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			flush(i)
			prev = r
			continue
		}
		if start >= 0 && (unicode.IsDigit(r) != unicode.IsDigit(prev) || unicode.IsLower(prev) && unicode.IsUpper(r)) {
			flush(i)
		}
		if start < 0 {
			start = i
		}
		prev = r
	}
	flush(len(s))
	return out
}
