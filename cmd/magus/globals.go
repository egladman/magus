package main

import (
	"flag"
	"strings"
	"sync"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
)

// Bridge aliases: the output vocabulary lives in output.go (package main).
// These keep the cmd/magus switch statements (case outputJSON, …) and OutputOptions
// signatures reading naturally.

const (
	outputText     = FormatText
	outputJSON     = FormatJSON
	outputYAML     = FormatYAML
	outputJSONL    = FormatJSONL
	outputName     = FormatName
	outputTemplate = FormatTemplate
	outputDot      = FormatDot
	outputMermaid  = FormatMermaid
	outputTree     = FormatTree
	outputMarkdown = FormatMarkdown
	outputGraphML  = FormatGraphML
)

// globalFlags carries display/verbosity flags shared across the top-level and every subcommand FlagSet
// (last write wins, so `magus -v ls` and `magus ls -v` are equivalent).
type globalFlags struct {
	output  string    // raw --output/-o value; parsed on demand
	tee     string    // mirror structured output to this file in append-create mode
	verbose verbosity // counted -v level
	quiet   bool      // suppress progress; quiet wins over -v
	silent  bool      // quiet + bounded failure dumps + bubbled notice lines
}

var global globalFlags

// template[=<body>]: a body renders a Go template; bare "-o template" lists the
// output's fields (the json keys usable in -o json and -o template).
// outputFormatHelp names the bare -o template form explicitly, because that is how a
// caller discovers which field names a template may use - they are the -o json keys
// (.name), not the Go field names (.Name), and nothing else advertises that.
var outputFormatHelp = "Output format (" + JoinFormats(CommonFormats, "|") +
	"|template[=<go-template>]); default: text. Bare -o template lists the available " +
	"template fields (they are the -o json names)"

func bindDisplayFlags(fs *flag.FlagSet) {
	fs.StringVar(&global.output, "output", global.output, outputFormatHelp)
	fs.StringVar(&global.output, "o", global.output, "Short for --output")
	fs.StringVar(&global.tee, "tee", global.tee, "Also write structured output (-o json|yaml|jsonl|template) to this file (append-create mode)")
	fs.Var(&global.verbose, "v", "increase log verbosity (-v/-vv: debug; -vvv: trace)")
	fs.BoolVar(&global.quiet, "quiet", global.quiet, "suppress progress output; only print errors and dump failing project output to stderr")
	fs.BoolVar(&global.quiet, "q", global.quiet, "short for --quiet")
	fs.BoolVar(&global.silent, "silent", global.silent, "like --quiet, but bound failing output to its tail (+full-log path) and bubble up only lines a target marks with 'magus:notice:'")
	fs.BoolVar(&global.silent, "s", global.silent, "short for --silent")
}

// cmdParse binds config/display flags, runs local registration, parses args, and returns positionals.
func cmdParse(name string, args []string, local func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
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
	applyDisplay()
	return fs.Args(), nil
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

// splitTargetFromArgs finds the target - the first positional - even when recognized
// global/display flags precede it, so `magus run --dry-run build` sees "build" as the
// target instead of mistaking the flag for it. It hoists global/display flags out first,
// then returns the first positional as the target and the remaining flags+positionals for
// the subcommand's own cmdParse. ok is false when there is no positional target at all.
//
// bind registers the command's OWN flags, and is what keeps the prescan from reading a
// value-taking local flag as a bool: with the flag unknown here its value is hoisted out
// as a positional, so `--skip api --skip web` reorders to `--skip --skip api web` and the
// first flag swallows the second as its value. A command that passes nil accepts that
// limit - one such flag survives it by luck of ordering, two do not.
func splitTargetFromArgs(args []string, bind func(*flag.FlagSet)) (target string, rest []string, ok bool) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
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

func outputOptionsOrDefault() (OutputOptions, error) {
	return ResolveOutput(global.output)
}

// isFlagNamed and flagValueOf recognize a flag the way Go's flag package does,
// for the few readers that scan a raw argument tail instead of a FlagSet.
//
// Go accepts all FOUR spellings of every flag - `-f v`, `--f v`, `-f=v`,
// `--f=v` - so every magus flag does too, for free, everywhere cmdParse is used.
// A hand-rolled scanner only accepts the spellings its author happened to write,
// and the resulting gap is invisible until someone types the missing one:
//
//   - `--then outputs export -path=out` was rejected as a bad value, not a bad
//     syntax, because chainPathFlag handled three of the four.
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

// flagValueOf returns the value of an `-name=value` or `--name=value` argument,
// or "" when arg is not that flag.
func flagValueOf(arg, name string) string {
	for _, prefix := range []string{"-" + name + "=", "--" + name + "="} {
		if v, ok := strings.CutPrefix(arg, prefix); ok {
			return v
		}
	}
	return ""
}

// ---- what each command actually bound, for the man page's parity gate ----

// The man page's flag list is a SECOND copy of every command's flags, hand-written in
// internal/clispec/registry.go because the real ones are bound in closures inside this
// package, which nothing outside `package main` can import. A second copy drifts: the
// registry advertised --simple for months after that flag was deleted, and it has never
// mentioned most of what `run` and `affected` actually accept.
//
// Moving 91 binding sites into an importable package would remove the copy outright, at
// the cost of touching every command. This is the cheaper half of that trade and it buys
// the part that matters: the copy stays, but it can no longer drift SILENTLY. cmdParse is
// the single funnel every command's flags pass through, so recording there yields each
// command's real, fully-bound flag set - config flags, display flags and local ones alike
// - and TestManpageFlagsMatchTheCLI compares that against the registry.
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
}

// recordFlagSet snapshots every flag bound for name. Called by cmdParse AFTER the local
// registration closure, so the record is the complete set the parser will honor.
func recordFlagSet(name string, fs *flag.FlagSet) {
	var flags []recordedFlag
	fs.VisitAll(func(f *flag.Flag) {
		flags = append(flags, recordedFlag{Name: f.Name, Usage: f.Usage})
	})
	flagRecord.Lock()
	defer flagRecord.Unlock()
	if flagRecord.byCommand == nil {
		flagRecord.byCommand = map[string][]recordedFlag{}
	}
	flagRecord.byCommand[name] = flags
}

// globalFlagNames is the set every command gets for free - the config flags generated
// from the config schema plus the display flags. The man page documents these once,
// centrally, so they are subtracted from both sides of the comparison rather than
// guessed at: --dry-run reads like an undocumented command flag to a source scanner and
// is in fact a generated config flag, and that false positive is what made a purely
// static version of this check unusable.
func globalFlagNames() map[string]bool {
	fs := flag.NewFlagSet("globals", flag.ContinueOnError)
	gen.BindFlags(fs, &config.Config{})
	bindDisplayFlags(fs)
	out := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { out[f.Name] = true })
	return out
}
