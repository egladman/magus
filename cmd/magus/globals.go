package main

import (
	"flag"
	"strings"

	"github.com/egladman/magus/cmd/magus/gen"
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
var outputFormatHelp = "Output format (" + JoinFormats(CommonFormats, "|") + "|template[=<go-template>]); default: text"

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
// the subcommand's own cmdParse. Only global/display flags are recognized here; a target's
// own local flags still belong after the target (they can't be resolved until the target
// is known). ok is false when there is no positional target at all.
func splitTargetFromArgs(args []string) (target string, rest []string, ok bool) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
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
