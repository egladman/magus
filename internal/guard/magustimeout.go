package guard

import (
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/hint"
)

// timeoutWrappers are coreutils timeout and the name Homebrew installs it under.
var timeoutWrappers = map[string]bool{"timeout": true, "gtimeout": true}

// timeoutInnerWrappers may sit between the timeout and magus, and between an outer
// launcher and the timeout, without changing which process the timeout kills.
var timeoutInnerWrappers = map[string]bool{
	"env": true, "nohup": true, "nice": true, "command": true, "exec": true, "stdbuf": true, "time": true,
}

// timedMagus is a magus invocation run under a timeout wrapper.
type timedMagus struct {
	// argv is the magus invocation as written, the binary's spelling first.
	argv []string
	// limit is the wrapper's duration, which the served command carries over.
	limit time.Duration
}

// magusTimeoutFires finds a magus invocation wrapped in timeout or gtimeout, in any
// statement of the line and inside a shell's -c script or an eval. It walks the words
// as written, since the parser's wrapper peeling drops the timeout this looks for.
//
// A wrapper sending QUIT or ABRT is left alone: that is how a goroutine dump is taken
// from a run that hung, and magus has no flag for it. So is a verb that runs until it
// is interrupted, where a timeout is the only way to bound it.
func magusTimeoutFires(command string, d Dialect) (timedMagus, bool) {
	f, err := parseFile(command, d)
	if err != nil {
		return timedMagus{}, false
	}
	var found timedMagus
	ok := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if ok {
			return false
		}
		if call, isCall := n.(*syntax.CallExpr); isCall {
			found, ok = timedMagusIn(literalWords(call.Args), d)
		}
		return !ok
	})
	return found, ok
}

func timedMagusIn(words []string, d Dialect) (timedMagus, bool) {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case timeoutWrappers[name]:
			return timedMagusUnder(words[1:], d)
		case timeoutInnerWrappers[name]:
			words = skipWrapperArgs(name, words[1:])
		case shells[name]:
			script, ok := shellDashC(words[1:])
			if !ok {
				return timedMagus{}, false
			}
			return magusTimeoutFires(script, wrapperShellDialect(name, d))
		case name == "eval":
			return magusTimeoutFires(strings.Join(words[1:], " "), d)
		default:
			return timedMagus{}, false
		}
	}
	return timedMagus{}, false
}

// timedMagusUnder reads a timeout's own arguments, then the command it runs.
func timedMagusUnder(args []string, d Dialect) (timedMagus, bool) {
	signal := ""
	i := 0
flags:
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			break flags
		case a == "-s" || a == "--signal":
			if i+1 < len(args) {
				signal = args[i+1]
			}
			i += 2
		case strings.HasPrefix(a, "--signal="):
			signal = strings.TrimPrefix(a, "--signal=")
			i++
		case strings.HasPrefix(a, "-s") && !strings.HasPrefix(a, "--"):
			signal = a[2:]
			i++
		case a == "-k" || a == "--kill-after":
			i += 2
		case strings.HasPrefix(a, "-") && a != "-":
			i++
		default:
			break flags
		}
	}
	if i >= len(args) || dumpsGoroutines(signal) {
		return timedMagus{}, false
	}
	limit, ok := parseTimeoutDuration(args[i])
	if !ok || limit <= 0 {
		return timedMagus{}, false
	}
	argv, ok := magusUnder(args[i+1:], d)
	if !ok || runsUntilInterrupted(argv[1:]) {
		return timedMagus{}, false
	}
	return timedMagus{argv: argv, limit: limit}, true
}

// magusUnder is the magus argv a timeout's command runs, through the launchers that
// leave magus the process the timeout kills. A shell's -c script or an eval counts when
// it runs magus at all.
func magusUnder(words []string, d Dialect) ([]string, bool) {
	for len(words) > 0 {
		name := path.Base(words[0])
		switch {
		case name == "magus":
			return slices.Clone(words), true
		case timeoutInnerWrappers[name]:
			words = skipWrapperArgs(name, words[1:])
		case strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "/"):
			words = words[1:]
		case shells[name]:
			script, ok := shellDashC(words[1:])
			if !ok {
				return nil, false
			}
			return magusInScript(script, wrapperShellDialect(name, d))
		case name == "eval":
			return magusInScript(strings.Join(words[1:], " "), d)
		default:
			return nil, false
		}
	}
	return nil, false
}

// magusInScript is the first magus argv a script runs.
func magusInScript(script string, d Dialect) ([]string, bool) {
	f, err := parseFile(script, d)
	if err != nil {
		return nil, false
	}
	var argv []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if argv != nil {
			return false
		}
		if call, ok := n.(*syntax.CallExpr); ok {
			argv, _ = magusUnder(literalWords(call.Args), d)
		}
		return argv == nil
	})
	return argv, argv != nil
}

// dumpsGoroutines reports a signal Go answers with a stack dump of every goroutine.
func dumpsGoroutines(signal string) bool {
	switch strings.TrimPrefix(strings.ToUpper(signal), "SIG") {
	case "QUIT", "ABRT", "IOT", "3", "6":
		return true
	}
	return false
}

// runsUntilInterrupted reports a magus argv that has no end of its own, or asks only
// for the version.
func runsUntilInterrupted(args []string) bool {
	if magusFlag(args, "version") {
		return true
	}
	words := magusSubcommandWords(args)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "watch", "events", "version":
		return true
	case "job":
		return len(words) > 1 && words[1] == "watch"
	}
	return false
}

// parseTimeoutDuration reads timeout's DURATION: a number, optionally fractional, with an
// s, m, h or d suffix and seconds without one.
func parseTimeoutDuration(s string) (time.Duration, bool) {
	unit := time.Second
	num := s
	if s != "" {
		switch s[len(s)-1] {
		case 's':
			num = s[:len(s)-1]
		case 'm':
			unit, num = time.Minute, s[:len(s)-1]
		case 'h':
			unit, num = time.Hour, s[:len(s)-1]
		case 'd':
			unit, num = 24*time.Hour, s[:len(s)-1]
		}
	}
	whole, frac, _ := strings.Cut(num, ".")
	if whole == "" || !allDigits(whole) || !allDigits(frac) {
		return 0, false
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, false
	}
	return time.Duration(f * float64(unit)).Round(time.Second), true
}

func allDigits(s string) bool {
	return !strings.ContainsFunc(s, func(r rune) bool { return r < '0' || r > '9' })
}

// formatLimit spells a duration the way a person types a flag: 10m, not 10m0s.
func formatLimit(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

const (
	magusTimeoutWhy        = "coreutils timeout kills magus from outside, so the run log records no cause and the tools it started can outlive it."
	magusTimeoutFlags      = "`--timeout <dur>` bounds the whole run and cancels its own process tree, `--target-timeout <dur>` caps each target, and `--stall-timeout <dur>` stops a run making no progress."
	magusTimeoutRead       = "It holds no lock worth waiting on: a held lock refuses at once (MGS3009)."
	adviseMagusTimeoutBuzz = "magus workspace: `magus buzz` has no timeout of its own, so this wrapper is its only bound. " +
		"It kills magus from outside, so nothing records why the script stopped, and the tools it started can outlive it."
)

// magusTimeoutVerdict judges a magus invocation wrapped in timeout. A run is served the
// same argv bounded by its own --timeout, any other verb the bare command, and a Buzz
// script, which has no bound of its own, an advisory.
func magusTimeoutVerdict(command string, d Dialect) (ShellVerdict, bool) {
	t, ok := magusTimeoutFires(command, d)
	if !ok {
		return ShellVerdict{}, false
	}
	rule := denyRule{Name: denyRuleMagusTimeout}
	words := magusSubcommandWords(t.argv[1:])
	verb := ""
	if len(words) > 0 {
		verb = words[0]
	}
	switch verb {
	case "buzz":
		return ShellVerdict{Context: adviseMagusTimeoutBuzz, Rule: rule}, true
	case "run", "affected":
		next := boundedRun(t.argv, formatLimit(t.limit))
		remedy := hint.NextForDenyRemedy(string(denyRuleMagusTimeout), next, "magus stops the run itself and records why.")
		lead := "Bound the run with magus's own flag instead of `timeout`.\n" + magusTimeoutWhy + "\n" + magusTimeoutFlags
		deny := "Bound the run with magus's own flag instead of `timeout`: `" + remedy.Run + "`.\n" + magusTimeoutWhy + "\n" + magusTimeoutFlags
		return ShellVerdict{Deny: deny, Rule: rule}.withRemedy(lead, remedy), true
	default:
		remedy := hint.NextForDenyRemedy(string(denyRuleMagusTimeout), t.argv, "it returns on its own.")
		lead := "Drop the `timeout` wrapper.\n" + magusTimeoutWhy + " " + magusTimeoutRead
		deny := "Drop the `timeout` wrapper: `" + remedy.Run + "`.\n" + magusTimeoutWhy + " " + magusTimeoutRead
		return ShellVerdict{Deny: deny, Rule: rule}.withRemedy(lead, remedy), true
	}
}

// boundedRun is argv with --timeout limit after its verb, unless it already carries one.
func boundedRun(argv []string, limit string) []string {
	if magusFlag(argv[1:], "timeout") {
		return slices.Clone(argv)
	}
	at := verbIndex(argv)
	out := slices.Clone(argv[:at+1])
	out = append(out, "--timeout", limit)
	return append(out, argv[at+1:]...)
}

// verbIndex is the index in a magus argv of its first subcommand word, read past the
// global flags and their values the way magusSubcommandWords reads them.
func verbIndex(argv []string) int {
	skip := false
	for i := 1; i < len(argv); i++ {
		a := argv[i]
		switch {
		case skip:
			skip = false
		case a == "" || a == "-":
		case a[0] == '-':
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") {
				skip, _ = MagusFlagTakesValue(name)
			}
		default:
			return i
		}
	}
	return len(argv) - 1
}
