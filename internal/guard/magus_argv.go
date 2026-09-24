package guard

import (
	"flag"
	"io"
	"strings"
	"sync"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
)

// magusGlobalFlags is the flag set magus parses before its subcommand, built from the same
// generated config binding the CLI uses plus the flags the CLI binds by hand. The guard reads
// a magus argv with it so a flag's value is never mistaken for a subcommand word: a rule that
// matched on `agent harness apply` missed `magus --root . agent harness apply`.
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

// magusFlag reports whether a magus argv carries the flag name, read the way Go's flag
// package reads it: one or two dashes, the whole name, and an optional `=value`. Go flags
// do not cluster, so `-cache-dir` is never `-h`.
func magusFlag(args []string, name string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		trimmed := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		if trimmed == a {
			continue
		}
		if trimmed == name || strings.HasPrefix(trimmed, name+"=") {
			return true
		}
	}
	return false
}
