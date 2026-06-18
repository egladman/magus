package main

import (
	"flag"

	"github.com/egladman/magus/cmd/magus/gen"
)

// Bridge aliases: the output vocabulary lives in output_format.go (package main).
// These keep the cmd/magus switch statements (case outputJSON, …) and outputSpec
// signatures reading naturally.
type outputSpec = OutputSpec

const (
	outputText     = FormatText
	outputJSON     = FormatJSON
	outputYAML     = FormatYAML
	outputJSONL    = FormatJSONL
	outputName     = FormatName
	outputWide     = FormatWide
	outputTemplate = FormatTemplate
	outputDot      = FormatDot
	outputMermaid  = FormatMermaid
	outputTree     = FormatTree
	outputMarkdown = FormatMarkdown
)

// globalFlags carries display/verbosity flags shared across the top-level and every subcommand FlagSet
// (last write wins, so `magus -v ls` and `magus ls -v` are equivalent).
type globalFlags struct {
	output  string    // raw --output/-o value; parsed on demand
	tee     string    // mirror structured output to this file in append-create mode
	verbose verbosity // counted -v level
	quiet   bool      // suppress progress; quiet wins over -v
}

var global globalFlags

var outputFormatHelp = "Output format (" + JoinFormats(CommonFormats, "|") + "|template=<go-template>); default: text"

func bindDisplayFlags(fs *flag.FlagSet) {
	fs.StringVar(&global.output, "output", global.output, outputFormatHelp)
	fs.StringVar(&global.output, "o", global.output, "Short for --output")
	fs.StringVar(&global.tee, "tee", global.tee, "Also write structured output (-o json|yaml|jsonl|template) to this file (append-create mode)")
	fs.Var(&global.verbose, "v", "increase log verbosity (-v/-vv: debug; -vvv: trace)")
	fs.BoolVar(&global.quiet, "quiet", global.quiet, "suppress progress output; only print errors and dump failing project output to stderr")
	fs.BoolVar(&global.quiet, "q", global.quiet, "short for --quiet")
}

// cmdParse binds config/display flags, runs local registration, parses args, and returns positionals.
func cmdParse(name string, args []string, local func(*flag.FlagSet)) ([]string, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	gen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	if local != nil {
		local(fs)
	}
	if err := fs.Parse(expandVerbosityArgs(args)); err != nil {
		return nil, err
	}
	applyDisplay()
	return fs.Args(), nil
}

func outputSpecOrDefault() (outputSpec, error) {
	return ResolveOutput(global.output)
}
