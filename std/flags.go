package std

import (
	"context"
	"fmt"
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
			Doc: "Parse argv against the declared flags, returning {values, positionals, unknown}: " +
				"switches take no value and record \"true\", valued flags take the next word or an " +
				"=value suffix, everything after `--` is a positional, and every argument that was " +
				"not declared is returned in unknown rather than guessed at. Errors when a valued " +
				"flag is given no value.",
			Args: []Arg{
				{Name: "argv", Type: TypeStringSlice},
				{Name: "switches", Type: TypeStringSlice},
				{Name: "valued", Type: TypeStringSlice},
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
func FlagsParse(_ context.Context, argv, switches, valued []string) (types.FlagParse, error) {
	isSwitch := make(map[string]bool, len(switches))
	for _, name := range switches {
		isSwitch[name] = true
	}
	isValued := make(map[string]bool, len(valued))
	for _, name := range valued {
		isValued[name] = true
	}

	// Non-nil so a script reads an absent group as an empty list rather than null, which
	// is one branch every caller would otherwise write.
	parsed := types.FlagParse{
		Values:      map[string]string{},
		Positionals: []string{},
		Unknown:     []string{},
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
		switch {
		case isSwitch[word]:
			parsed.Values[word] = "true"
		case isValued[word]:
			// The value is the NEXT word, and running off the end is an error rather
			// than an empty string: a flag whose value silently became "" is the shape
			// that makes a misconfigured call look like a configured one.
			if len(rest) == 0 {
				return types.FlagParse{}, fmt.Errorf("flags.parse: %s takes a value and none followed it", word)
			}
			parsed.Values[word] = rest[0]
			rest = rest[1:]
		default:
			// The =value form, accepted only for a flag declared as valued. Split on the
			// FIRST = so a value may contain one.
			if name, value, ok := strings.Cut(word, "="); ok && isValued[name] {
				parsed.Values[name] = value
				continue
			}
			parsed.Unknown = append(parsed.Unknown, word)
		}
	}
	return parsed, nil
}
