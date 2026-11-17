package commands

import (
	"flag"
	"fmt"
	"strings"

	"github.com/egladman/magus/libs/clikit/output"
)

// OutputFormatHelp is the usage of -o.
//
// template[=<body>]: a body renders a Go template; bare "-o template" lists the
// output's fields (the json keys usable in -o json and -o template).
// It names the bare -o template form explicitly, because that is how a
// caller discovers which field names a template may use: they are the -o json keys
// (.name), not the Go field names (.Name), and nothing else advertises that.
var OutputFormatHelp = fmt.Sprintf("Output format (%s|template[=<go-template>]); default: text. Bare -o template lists the available template fields (they are the -o json names); see docs/reference/output-formats.md", output.Join(output.Common, "|"))

// SplitOnDashDash splits args at the first "--": what precedes it, and what follows it
// verbatim. An absent separator leaves the tail nil, a trailing one leaves it empty.
func SplitOnDashDash(args []string) (before, after []string) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:]
		}
	}
	return args, nil
}

// ReorderFlagsFirst moves recognized flags (and their values) ahead of positional
// arguments, so a flag may appear after a positional and stdlib flag still sees it.
// It restores the GNU-style interspersed-flag behavior users expect, without a
// dependency. It stops at "--": everything after is left in place (the passthrough
// marker and its tail survive for the caller or flag.Parse to handle). A value-taking
// flag is detected from fs (a flag whose Value is not a bool flag consumes the next
// token); an unknown or "=" flag is passed through untouched for flag.Parse to report.
func ReorderFlagsFirst(fs *flag.FlagSet, args []string) []string {
	flags, positionals := PartitionFlags(fs, args)
	return append(flags, positionals...)
}

// PartitionFlags splits args into recognized flags (with their values) and positionals,
// preserving order within each group. Shared by ReorderFlagsFirst (which concatenates
// them) and SplitTargetFromArgs (which needs the first positional). See ReorderFlagsFirst
// for the "--"/value-flag/unknown-flag rules.
func PartitionFlags(fs *flag.FlagSet, args []string) (flags, positionals []string) {
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
		if f := fs.Lookup(name); f != nil && !FlagIsBool(f) && i+1 < len(args) {
			flags = append(flags, a, args[i+1]) // value flag consumes the next token
			i++
			continue
		}
		flags = append(flags, a) // bool flag, or unknown (flag.Parse reports it)
	}
	return flags, positionals
}

// SplitTargetFromArgs finds the target (the first positional) even when recognized
// global/display flags precede it, so `magus run --dry-run build` sees "build" as the
// target instead of mistaking the flag for it. It hoists global/display flags out first,
// then returns the first positional as the target and the remaining flags+positionals for
// the subcommand's own commandParse. ok is false when there is no positional target at all.
//
// bind registers every flag the invocation accepts, the global and display set and the
// command's OWN, and is what keeps the prescan from reading a value-taking local flag as
// a bool. With the flag unknown here its value is hoisted out as a positional. So
// `--skip api --skip web` reorders to `--skip --skip api web`, and the first flag
// swallows the second as its value. A command that binds nothing of its own accepts that
// limit: one such flag survives it by luck of ordering, two do not.
func SplitTargetFromArgs(args []string, bind func(*flag.FlagSet)) (target string, rest []string, ok bool) {
	fs := flag.NewFlagSet("prescan", flag.ContinueOnError)
	bind(fs)
	flags, positionals := PartitionFlags(fs, args)
	if len(positionals) == 0 || positionals[0] == "--" {
		return "", nil, false
	}
	rest = append(append(make([]string, 0, len(args)), flags...), positionals[1:]...)
	return positionals[0], rest, true
}

// FlagIsBool reports whether f is a boolean flag (usable without a value), which
// includes counted flags like -v (verbosity implements IsBoolFlag).
func FlagIsBool(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// IsFlagNamed and ParseFlagValue recognize a flag the way Go's flag package does,
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
func IsFlagNamed(arg, name string) bool {
	return arg == "-"+name || arg == "--"+name
}

// ParseFlagValue returns the value of an `-name=value` or `--name=value` argument,
// or "" when arg is not that flag.
func ParseFlagValue(arg, name string) string {
	for _, prefix := range []string{fmt.Sprintf("-%s=", name), fmt.Sprintf("--%s=", name)} {
		if v, ok := strings.CutPrefix(arg, prefix); ok {
			return v
		}
	}
	return ""
}
