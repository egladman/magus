package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newFlagSetRe finds every command FlagSet construction and captures the
// variable it is bound to, so the check can require that same variable be
// handed to bindDisplayFlags.
var newFlagSetRe = regexp.MustCompile(`^\s*(\w+) := flag\.NewFlagSet\(`)

// TestEveryCommandBindsDisplayFlags is a source-level invariant: every command
// FlagSet gets the display flags (-o/--output, --tee, -v, -q/--quiet,
// -s/--silent).
//
// It is a TEXT scan rather than a behavioral test on purpose. The failure it
// prevents is a command forgetting to call bindDisplayFlags, which no runtime
// assertion can see without enumerating and invoking every subcommand - and an
// enumeration is exactly the thing that goes stale when someone adds a command.
//
// The rule exists because partial support is worse than none. `magus graph
// verify -s` died on "flag provided but not defined", and `magus agent install
// ... -s` did the same with stderr redirected, which looked precisely like a
// successful install - the skills were never written and nothing said so. An
// agent told to always pass -s meets one of those, concludes the flag is
// unreliable, and stops using it everywhere. One gap decays the whole
// convention, so the convention has to be total.
func TestEveryCommandBindsDisplayFlags(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)

	var missing []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		body, err := os.ReadFile(file)
		require.NoError(t, err, "read %s", file)
		lines := strings.Split(string(body), "\n")

		for i, line := range lines {
			m := newFlagSetRe.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			// Scan to the end of the enclosing function rather than a fixed
			// window: the bind is often separated from the construction by the
			// comment explaining why it is there, and a line budget would make
			// this test fail on formatting instead of on the thing it checks.
			// A `nodisplayflags:` comment in the preceding lines opts out, and the
			// text after the marker must say why. There is exactly one legitimate
			// case (main.go's "adopted" set, which redeclares the globals into
			// ignored variables precisely to swallow them), so the escape hatch is
			// deliberately ugly and has to be justified in place.
			if slicesContainsSubstr(lines[max(i-8, 0):i], "nodisplayflags:") {
				continue
			}
			want := "bindDisplayFlags(" + m[1] + ")"
			if !slicesContainsSubstr(lines[i+1:endOfFunc(lines, i)], want) {
				missing = append(missing, file+":"+itoa(i+1)+" ("+m[1]+")")
			}
		}
	}

	assert.Empty(t, missing,
		"every command FlagSet must call bindDisplayFlags right after construction, so -s/-q/-v/-o work on every command:\n  %s",
		strings.Join(missing, "\n  "))
}

// endOfFunc returns the index of the closing brace of the function containing
// line i, relying on gofmt putting a top-level "}" in column zero.
func endOfFunc(lines []string, i int) int {
	for j := i + 1; j < len(lines); j++ {
		if lines[j] == "}" {
			return j
		}
	}
	return len(lines)
}

func slicesContainsSubstr(lines []string, want string) bool {
	for _, l := range lines {
		if strings.Contains(l, want) {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
