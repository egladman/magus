package std

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module flags -lang buzz -out ../internal/interp/bindings/gen/flags.go

func init() { Register(Flags) }

// Flags is the "flags" host module: argv parsing for a script that receives one.
//
// A Buzz script run by `magus buzz <file> -- <args>` gets its argv as a list and, before
// this, hand-rolled the walk over it. Five shipped hook templates did exactly that, and
// each grew a slightly different answer to the same questions: what happens to an argument
// nobody declared, to a repeated flag, to one given no value.
//
// The API is DECLARATIVE. A script says which flags it supports and gets back three
// separated lists; it never asks this module to guess. That separation is the whole
// design: an argument the script did not declare is handed back untouched rather than
// dropped, folded into the positionals, or rejected here, because only the caller knows
// whether an unrecognized argument should block the run or be reported and ignored.
var Flags = Module{
	Name: "flags",
	WASM: true,
	Doc:  "Parse a script's argv against the flags it declares.",
	Methods: []Method{
		{
			Name: "parse",
			Doc: "Parse argv against the declared flags, returning {values, lists, positionals, unknown}: " +
				"switches take no value and record \"true\", valued flags take the next word or an " +
				"=value suffix (a repeated valued flag records the last), flags named in repeated take " +
				"a value each time and collect them in order into lists, everything after `--` is a " +
				"positional, and every argument that was not declared is returned in unknown rather " +
				"than guessed at. A flag is declared as it is typed, dashes included (\"--file\", not " +
				"\"file\"), and values and lists are keyed the same way. A bare word before `--` is " +
				"unknown, not a positional; `magus buzz <file> -- <args>` consumes its own `--`, so a " +
				"script taking paths is run with a second one. With command set, the first bare word " +
				"instead starts a command, the way sudo, env and Go's flag package read argv: it and " +
				"every word after it are positionals, verbatim, flags included. Errors when a valued " +
				"flag is given no value, and when a flag named in required is absent or given an empty " +
				"value: a workflow passing an unset variable (`--issue \"$ISSUE\"`) is refused rather " +
				"than read as a choice.",
			Args: []Arg{
				{Name: "argv", Type: TypeStringSlice},
				{Name: "switches", Type: TypeStringSlice},
				{Name: "valued", Type: TypeStringSlice},
				{Name: "required", Type: TypeStringSlice, Optional: true},
				{Name: "repeated", Type: TypeStringSlice, Optional: true},
				{Name: "command", Type: TypeBool, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "FlagParse"}},
			Raises:  true,
			Impl:    FlagsParse,
		},
	},
}

// FlagsParse implements flags.parse.
//
// The separator ends flag parsing entirely, which is what `--` means everywhere else: a
// word after it is data even when it is spelled like a flag this script declares.
func FlagsParse(_ context.Context, argv, switches, valued, required, repeated []string, command bool) (types.FlagParse, error) {
	for _, name := range required {
		if !slices.Contains(valued, name) && !slices.Contains(repeated, name) {
			return types.FlagParse{}, fmt.Errorf("flags.parse: %s is required but not declared valued or repeated; a switch cannot be required", name)
		}
	}
	parsed, err := flagsParse(argv, switches, valued, repeated, command)
	if err != nil {
		return types.FlagParse{}, err
	}
	var missing []string
	for _, name := range required {
		if parsed.Values[name] == "" && !slices.ContainsFunc(parsed.Lists[name], func(v string) bool { return v != "" }) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return types.FlagParse{}, fmt.Errorf("flags.parse: %s required and absent or empty", strings.Join(missing, ", "))
	}
	return parsed, nil
}

func flagsParse(argv, switches, valued, repeated []string, command bool) (types.FlagParse, error) {
	kind := make(map[string]byte, len(switches)+len(valued)+len(repeated))
	for _, name := range switches {
		kind[name] = 's'
	}
	for _, name := range valued {
		kind[name] = 'v'
	}
	for _, name := range repeated {
		kind[name] = 'r'
	}

	// Non-nil so a script reads an absent group as an empty list rather than null, which
	// is one branch every caller would otherwise write.
	parsed := types.FlagParse{
		Values:      map[string]string{},
		Lists:       map[string][]string{},
		Positionals: []string{},
		Unknown:     []string{},
	}
	record := func(name, value string) {
		if kind[name] == 'r' {
			parsed.Lists[name] = append(parsed.Lists[name], value)
		} else {
			parsed.Values[name] = value
		}
	}
	// Consumed from the front rather than walked by index: a valued flag eats the word
	// after it, and "what is left" says that plainly where an index the loop also
	// increments does not.
	rest := argv
	for len(rest) > 0 {
		word := rest[0]
		rest = rest[1:]
		if word == "--" {
			parsed.Positionals = append(parsed.Positionals, rest...)
			break
		}
		switch kind[word] {
		case 's':
			parsed.Values[word] = "true"
		case 'v', 'r':
			// The value is the NEXT word, and running off the end is an error rather
			// than an empty string: a flag whose value silently became "" is the shape
			// that makes a misconfigured call look like a configured one.
			if len(rest) == 0 {
				return types.FlagParse{}, fmt.Errorf("flags.parse: %s takes a value and none followed it", word)
			}
			record(word, rest[0])
			rest = rest[1:]
		default:
			// The =value form, accepted only for a flag declared to take a value. Split on
			// the FIRST = so a value may contain one.
			if name, value, ok := strings.Cut(word, "="); ok && (kind[name] == 'v' || kind[name] == 'r') {
				record(name, value)
				continue
			}
			// In command mode a bare word starts the command, and the command keeps its
			// own flags: `on-linux --arch arm64 magus run test -s` runs `magus run test -s`.
			if command && !strings.HasPrefix(word, "-") {
				parsed.Positionals = append(parsed.Positionals, word)
				parsed.Positionals = append(parsed.Positionals, rest...)
				return parsed, nil
			}
			parsed.Unknown = append(parsed.Unknown, word)
		}
	}
	return parsed, nil
}
