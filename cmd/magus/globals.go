package main

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/output"
)

// The -o formats under the names every command's switch spells them (case outputJSON);
// the values are libs/clikit/output's.
const (
	outputText     = output.Text
	outputJSON     = output.JSON
	outputYAML     = output.YAML
	outputJSONL    = output.JSONL
	outputName     = output.Name
	outputTemplate = output.Template
)

// globalFlags carries display/verbosity flags shared across the top-level and every subcommand FlagSet
// (last write wins, so `magus -v ls` and `magus ls -v` are equivalent).
type globalFlags struct {
	teeInitialized string    // the capture already replaced during this invocation
	output         string    // raw --output/-o value; parsed on demand
	tee            string    // mirror this invocation's structured output to this file
	verbose        verbosity // counted -v level
	quiet          bool      // suppress progress; quiet wins over -v
	silent         bool      // quiet + bounded failure dumps + bubbled notice lines
}

var global globalFlags

// template[=<body>]: a body renders a Go template; bare "-o template" lists the
// output's fields (the json keys usable in -o json and -o template).
// outputFormatHelp names the bare -o template form explicitly, because that is how a
// caller discovers which field names a template may use: they are the -o json keys
// (.name), not the Go field names (.Name), and nothing else advertises that.
var outputFormatHelp = fmt.Sprintf("Output format (%s|template[=<go-template>]); default: text. Bare -o template lists the available template fields (they are the -o json names); see docs/reference/output-formats.md", output.Join(output.Common, "|"))

// bindGlobalFlags registers the flags magus accepts before its subcommand, bar the display
// set bindDisplayFlags adds. The guard reads a magus argv with its own copy of both
// (guard.MagusFlagTakesValue), held to them by TestGuardKnowsEveryGlobalFlag.
func bindGlobalFlags(fs *flag.FlagSet, root, cfgPath *string) {
	fs.StringVar(root, "root", "", "Path to start the workspace search from, -C after make (must precede subcommand; default: cwd)")
	fs.StringVar(root, "C", "", "Short for --root")
	fs.StringVar(cfgPath, "config", "", "Config file path (must precede subcommand; default: search magus.yaml in CWD / XDG)")
	fs.StringVar(cfgPath, "c", "", "Short for --config")
	flaggen.BindFlags(fs, &globalCfg)
}

func bindDisplayFlags(fs *flag.FlagSet) {
	bindOutputFlags(fs)
	bindProgressFlags(fs)
}

func bindOutputFlags(fs *flag.FlagSet) {
	fs.StringVar(&global.output, "output", global.output, outputFormatHelp)
	fs.StringVar(&global.output, "o", global.output, "Short for --output")
	fs.StringVar(&global.tee, "tee", global.tee, "Also write structured output (-o json|yaml|jsonl|template) to this file (replaces previous contents)")
}

func bindProgressFlags(fs *flag.FlagSet) {
	fs.Var(&global.verbose, "v", "increase log verbosity (-v/-vv: debug; -vvv: trace)")
	fs.BoolVar(&global.quiet, "quiet", global.quiet, "suppress progress output; only print errors and dump failing project output to stderr")
	fs.BoolVar(&global.quiet, "q", global.quiet, "short for --quiet")
	fs.BoolVar(&global.silent, "silent", global.silent, "like --quiet, but bound failing output to its tail (+full-log path) and bubble up only lines a target marks with 'magus:notice:'")
	fs.BoolVar(&global.silent, "s", global.silent, "short for --silent")
}

// commandParse binds config/display flags, runs local registration, parses args, and returns positionals.
func commandParse(name string, args []string, local func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	flaggen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	if local != nil {
		local(fs)
	}
	recordFlagSet(name, fs)
	// Reorder so a flag may follow a positional (`magus run build --explain`);
	// stdlib flag otherwise stops at the first positional. Done after binding so the
	// full flag set (config + display + local) is known for value detection.
	if err := fs.Parse(reorderFlagsFirst(fs, expandVerbosityArgs(args))); err != nil {
		return nil, err
	}
	if flagWasSet(fs, "tee") {
		global.teeInitialized = ""
	}
	applyDisplay()
	if err := checkOutputSupported(name, fs); err != nil {
		return nil, err
	}
	return fs.Args(), nil
}

// commandAt is the tree's node for a FlagSet's name, or false for a name that
// is no path, such as "run build".
func commandAt(name string) (command.Command, bool) {
	node := commands.Root.Spec.Command
	for word := range strings.FieldsSeq(name) {
		i := slices.IndexFunc(node.Children, func(c command.Command) bool { return c.Name == word })
		if i < 0 {
			return command.Command{}, false
		}
		node = node.Children[i]
	}
	return node, true
}

// reorderFlagsFirst moves recognized flags (and their values) ahead of positional
// arguments, so a flag may appear after a positional and stdlib flag still sees it.
// It restores the GNU-style interspersed-flag behavior users expect, without a
// dependency. It stops at "--": everything after is left in place (the passthrough
// marker and its tail survive for the caller or flag.Parse to handle). A value-taking
// flag is detected from fs (a flag whose Value is not a bool flag consumes the next
// token); an unknown or "=" flag is passed through untouched for flag.Parse to report.
func reorderFlagsFirst(fs *flag.FlagSet, args []string) []string {
	flags, positionals := partitionFlags(fs, args)
	return append(flags, positionals...)
}

// partitionFlags splits args into recognized flags (with their values) and positionals,
// preserving order within each group. Shared by reorderFlagsFirst (which concatenates
// them) and splitTargetFromArgs (which needs the first positional). See reorderFlagsFirst
// for the "--"/value-flag/unknown-flag rules.
func partitionFlags(fs *flag.FlagSet, args []string) (flags, positionals []string) {
	flags = make([]string, 0, len(args))
	positionals = make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positionals = append(positionals, a)
			continue
		}
		name := strings.TrimLeft(a, "-")
		if strings.IndexByte(name, '=') >= 0 {
			flags = append(flags, a) // -flag=value: self-contained
			continue
		}
		if f := fs.Lookup(name); f != nil && !flagIsBool(f) && i+1 < len(args) {
			flags = append(flags, a, args[i+1]) // value flag consumes the next token
			i++
			continue
		}
		flags = append(flags, a) // bool flag, or unknown (flag.Parse reports it)
	}
	return flags, positionals
}

// splitTargetFromArgs finds the target (the first positional) even when recognized
// global/display flags precede it, so `magus run --dry-run build` sees "build" as the
// target instead of mistaking the flag for it. It hoists global/display flags out first,
// then returns the first positional as the target and the remaining flags+positionals for
// the subcommand's own commandParse. ok is false when there is no positional target at all.
//
// bind registers the command's OWN flags, and is what keeps the prescan from reading a
// value-taking local flag as a bool: with the flag unknown here its value is hoisted out
// as a positional, so `--skip api --skip web` reorders to `--skip --skip api web` and the
// first flag swallows the second as its value. A command that passes nil accepts that
// limit: one such flag survives it by luck of ordering, two do not.
func splitTargetFromArgs(args []string, bind func(*flag.FlagSet)) (target string, rest []string, ok bool) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	flaggen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	if bind != nil {
		bind(fs)
	}
	flags, positionals := partitionFlags(fs, args)
	if len(positionals) == 0 || positionals[0] == "--" {
		return "", nil, false
	}
	rest = append(append(make([]string, 0, len(args)), flags...), positionals[1:]...)
	return positionals[0], rest, true
}

// flagIsBool reports whether f is a boolean flag (usable without a value), which
// includes counted flags like -v (verbosity implements IsBoolFlag).
func flagIsBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func outputOptionsOrDefault() (output.Options, error) {
	return output.Parse(global.output)
}

// isFlagNamed and parseFlagValue recognize a flag the way Go's flag package does,
// for the few readers that scan a raw argument tail instead of a FlagSet.
//
// Go accepts all FOUR spellings of every flag (`-f v`, `--f v`, `-f=v`,
// `--f=v`) so every magus flag does too, for free, everywhere commandParse is used.
// A hand-rolled scanner only accepts the spellings its author happened to write,
// and the resulting gap is invisible until someone types the missing one:
//
//   - `magus affected -explain .` did not error on the flag at all. The scanner
//     matched only `--explain`, so `-explain` fell through and `.` was read as a
//     TARGET, producing "target name \".\": must contain only letters, digits..."
//   - an error naming neither the flag nor the real problem.
//
// These exist so that gap is closed once rather than re-derived per call site.
// Prefer a FlagSet; reach for these only when the tail cannot go through one.
func isFlagNamed(arg, name string) bool {
	return arg == "-"+name || arg == "--"+name
}

// parseFlagValue returns the value of an `-name=value` or `--name=value` argument,
// or "" when arg is not that flag.
func parseFlagValue(arg, name string) string {
	for _, prefix := range []string{fmt.Sprintf("-%s=", name), fmt.Sprintf("--%s=", name)} {
		if v, ok := strings.CutPrefix(arg, prefix); ok {
			return v
		}
	}
	return ""
}

// ---- what each command actually bound, for the man page's parity gate ----

// The registry declares every command's flags and the binders in cmd/magus/gen are
// generated from it, so a generated flag cannot drift from its man page. What still can
// is a flag bound by hand: a FlagCustom flag's fs.Var call, and the config-derived
// globals. commandParse is the single funnel every command's flags pass through, so
// recording there yields each command's real, fully-bound flag set (config flags,
// display flags and local ones alike) and TestManpageFlagsMatchTheCLI compares that
// against the registry.
//
// Recording is unconditional rather than test-gated. It is one map write per invocation
// of one command, and a record that only exists under a test hook is a record that can be
// wrong in the binary people actually run.

var flagRecord struct {
	sync.Mutex
	byCommand map[string][]recordedFlag
}

// recordedFlag is what a command actually bound, as opposed to what the man page claims.
type recordedFlag struct {
	Name  string
	Usage string
	// Bool is whether the flag takes no value, which the guard reads a command's argv by.
	Bool bool
}

// recordFlagSet snapshots every flag bound for name. Called by commandParse AFTER the local
// registration closure, so the record is the complete set the parser will honor.
func recordFlagSet(name string, fs *flag.FlagSet) {
	var flags []recordedFlag
	fs.VisitAll(func(f *flag.Flag) {
		flags = append(flags, recordedFlag{Name: f.Name, Usage: f.Usage, Bool: flagIsBool(f)})
	})
	flagRecord.Lock()
	defer flagRecord.Unlock()
	if flagRecord.byCommand == nil {
		flagRecord.byCommand = map[string][]recordedFlag{}
	}
	flagRecord.byCommand[name] = flags
}

// printOwnDefaults prints the flags fs declares beyond the global ones. Every command
// accepts the globals and `magus -h` lists them once; repeating them under each command
// buried its few own flags in about a thousand lines of help.
//
// It prints through command.FlagHelp, the formatter behind the man page, so a bool reads
// --[no-]name (default: on|off) in both. flag.PrintDefaults prints a single dash and
// lifts a backquoted word out of the usage as the value's name.
func printOwnDefaults(fs *flag.FlagSet) {
	globals := globalFlagNames()
	help := command.FlagHelp(fs, func(name string) bool { return globals[name] })
	cmd, known := commandAt(fs.Name())
	if !known {
		printFlagHelp(fs.Output(), help)
		return
	}
	printGroupedFlagHelp(fs.Output(), help, cmd.Flags)
	printExamples(fs.Output(), cmd.Examples)
}

func printExamples(w io.Writer, examples []command.Example) {
	if len(examples) > 0 {
		fmt.Fprintln(w, "\nExamples:")
	}
	for _, e := range examples {
		fmt.Fprintf(w, "  %s\n    \t%s\n", e.Command, e.Comment)
	}
}

// printGroupedFlagHelp prints the flags no group claims, then each group's
// under its heading, in the order the tree declares them.
func printGroupedFlagHelp(w io.Writer, help []command.Help, declared []command.Flag) {
	groupOf := map[string]string{}
	var groups []string
	for _, f := range declared {
		groupOf[f.Name] = f.Group
		if f.Group != "" && !slices.Contains(groups, f.Group) {
			groups = append(groups, f.Group)
		}
	}
	inGroup := func(group string) []command.Help {
		return slices.DeleteFunc(slices.Clone(help), func(h command.Help) bool {
			return groupOf[strings.TrimLeft(strings.TrimPrefix(h.Name, "--[no-]"), "-")] != group
		})
	}
	printFlagHelp(w, inGroup(""))
	for _, group := range groups {
		fmt.Fprintf(w, "\n%s\n", group)
		printFlagHelp(w, inGroup(group))
	}
}

// printRootFlags prints the flags the command tree declares on the program itself.
func printRootFlags(w io.Writer) {
	// nodisplayflags: this set only prints the root's declared flags in the top-level
	// help, which lists the display flags itself; it parses nothing.
	fs := flag.NewFlagSet(commands.Root.Spec.Name, flag.ContinueOnError)
	if err := commands.Root.Spec.BindFlags(fs); err != nil {
		fmt.Fprintf(w, "  the program's own flags cannot be listed: %v\n", err)
		return
	}
	printFlagHelp(w, command.FlagHelp(fs, nil))
}

func printFlagHelp(w io.Writer, flags []command.Help) {
	for _, h := range flags {
		label := "  " + h.Name
		if h.Value != "" {
			label += " " + h.Value
		}
		if h.Default != "" {
			label = fmt.Sprintf("%s (default: %s)", label, h.Default)
		}
		fmt.Fprintf(w, "%s\n    \t%s\n", label, strings.ReplaceAll(h.Usage, "\n", "\n    \t"))
	}
}

// globalFlagNames is the set every command gets for free: the config flags generated
// from the config schema plus the display flags. The man page documents these once,
// centrally, so they are subtracted from both sides of the comparison rather than
// guessed at: --dry-run reads like an undocumented command flag to a source scanner and
// is in fact a generated config flag, and that false positive is what made a purely
// static version of this check unusable.
func globalFlagNames() map[string]bool {
	fs := flag.NewFlagSet("globals", flag.ContinueOnError)
	flaggen.BindFlags(fs, &config.Config{})
	bindDisplayFlags(fs)
	out := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { out[f.Name] = true })
	return out
}
