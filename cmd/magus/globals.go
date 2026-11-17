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
	"github.com/egladman/magus/internal/log/quiet"
	"github.com/egladman/magus/internal/render"
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
	output  string          // raw --output/-o value; parsed on demand
	tee     render.Tee      // mirror this invocation's structured output to this file
	verbose quiet.Verbosity // counted -v level
	quiet   bool            // suppress progress; quiet wins over -v
	silent  bool            // quiet + bounded failure dumps + bubbled notice lines
}

var global globalFlags

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
	fs.StringVar(&global.output, "output", global.output, commands.OutputFormatHelp)
	fs.StringVar(&global.output, "o", global.output, "Short for --output")
	fs.StringVar(&global.tee.Path, "tee", global.tee.Path, "Also write structured output (-o json|yaml|jsonl|template) to this file (replaces previous contents)")
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
	if err := command.Parse(fs, commands.ReorderFlagsFirst(fs, quiet.ExpandVerbosityArgs(args))); err != nil {
		return nil, err
	}
	if flagWasSet(fs, "tee") {
		global.tee.Reset()
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

func outputOptionsOrDefault() (output.Options, error) {
	return output.Parse(global.output)
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
		flags = append(flags, recordedFlag{Name: f.Name, Usage: f.Usage, Bool: commands.FlagIsBool(f)})
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

// splitTargetFromArgs is commands.SplitTargetFromArgs over the global and display flags
// this package binds, plus the command's own.
func splitTargetFromArgs(args []string, bind func(*flag.FlagSet)) (target string, rest []string, ok bool) {
	return commands.SplitTargetFromArgs(args, func(fs *flag.FlagSet) {
		flaggen.BindFlags(fs, &globalCfg)
		bindDisplayFlags(fs)
		if bind != nil {
			bind(fs)
		}
	})
}
