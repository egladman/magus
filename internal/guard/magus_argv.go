package guard

import (
	"flag"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
)

// magusGlobalFlags is the flag set magus parses before its subcommand, built from the same
// generated config binding the CLI uses plus the flags the CLI binds by hand. The guard reads
// a magus argv with it so a flag's value is never mistaken for a subcommand word: a rule that
// matched on `agent harness install` missed `magus --root . agent harness install`.
//
// cmd/magus's TestGuardKnowsEveryGlobalFlag holds the hand-bound half to the CLI's own
// binding (bindGlobalFlags and bindDisplayFlags), so a new global flag cannot reopen that
// gap.
var magusGlobalFlags = sync.OnceValue(func() *flag.FlagSet {
	fs := flag.NewFlagSet("magus", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	gen.BindFlags(fs, &config.Config{})
	for _, name := range []string{"root", "C", "config", "c", "output", "o", "tee"} {
		fs.String(name, "", "")
	}
	for _, name := range []string{"v", "quiet", "q", "silent", "s"} {
		fs.Bool(name, false, "")
	}
	return fs
})

// MagusFlagTakesValue reports whether the magus global flag name consumes the next word as
// its value when it is not written with `=`. known is false for a name magus does not
// accept before its subcommand.
func MagusFlagTakesValue(name string) (takesValue, known bool) {
	f := magusGlobalFlags().Lookup(name)
	if f == nil {
		return false, false
	}
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return false, true
	}
	return true, true
}

// magusSubcommandWords is the bare words of a magus argv: every flag and every global
// flag's value dropped, stopping at `--` because everything past it belongs to an
// underlying tool. A flag magus does not know is read as taking no value, which leaves
// its value as a word; that fails toward a rule not matching a mangled command magus
// would itself refuse to parse.
func magusSubcommandWords(args []string) []string {
	var words []string
	skip := false
	for _, a := range args {
		switch {
		case skip:
			skip = false
		case a == "--":
			return words
		case a == "" || a == "-":
			continue
		case a[0] == '-':
			name := strings.TrimLeft(a, "-")
			if strings.Contains(name, "=") {
				continue
			}
			skip, _ = MagusFlagTakesValue(name)
		default:
			words = append(words, a)
		}
	}
	return words
}

// magusFlag reports whether a magus argv sets any of the flags names, read the way Go's flag
// package reads it: one or two dashes, the whole name, an optional `=value`, and the last
// occurrence winning. A global boolean given a value strconv.ParseBool reads as false is
// unset, so `-s=false` is not silent. Any other flag with a value is set: the guard cannot
// tell a subcommand's boolean from its `--timeout=0`. Go flags do not cluster, so
// `-cache-dir` is never `-h`.
func magusFlag(args []string, names ...string) bool {
	set := false
	for _, a := range args {
		if a == "--" {
			break
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if trimmed == a {
			continue
		}
		name, value, joined := strings.Cut(trimmed, "=")
		if !slices.Contains(names, name) {
			continue
		}
		set = !joined || !unsetsBool(name, value)
	}
	return set
}

func unsetsBool(name, value string) bool {
	if takesValue, known := MagusFlagTakesValue(name); !known || takesValue {
		return false
	}
	b, err := strconv.ParseBool(value)
	return err == nil && !b
}
