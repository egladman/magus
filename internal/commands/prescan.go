package commands

import (
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/log/quiet"
)

// ExtractRootFlag peeks the workspace root before the main flag parse. -C is the
// bound short form of --root and has to be read here too: skipping it loaded THIS
// workspace's magus.yaml while the magusfile came from the one -C names. Matching
// is exact and case-sensitive, so -c stays the short form of --config
// (config.ExtractFlag owns that one).
func ExtractRootFlag(args []string) string {
	for i, a := range args {
		if a == "--" {
			return ""
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		body := strings.TrimPrefix(strings.TrimPrefix(a, "-"), "-")
		name, value, inline := strings.Cut(body, "=")
		if name != "root" && name != "C" {
			continue
		}
		if inline {
			return value
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// ExtractQuietFlag peeks --quiet/--silent before the main flag parse, because
// applyDisplay runs during early startup and decides progress suppression from
// global.quiet. --silent counts here: it is documented as "like --quiet, but
// ...", and reading only --quiet made -s byte-identical to no flag on a passing
// run: the flag parse set global.silent long after the display was configured.
func ExtractQuietFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-q", "--quiet", "-quiet", "-s", "--silent", "-silent":
			return true
		}
	}
	return false
}

// ExtractSilentFlag peeks --silent for the same reason, so the bounded-dump and
// notice-bubbling behavior is configured in the same early pass.
func ExtractSilentFlag(args []string) bool {
	for _, a := range args {
		if a == "--" {
			return false
		}
		switch a {
		case "-s", "--silent", "-silent":
			return true
		}
	}
	return false
}

// ExtractServerEnabledFlag peeks the --server-enabled bool flag before the main flag
// parse, so it can gate the shared-server discovery that runs during early startup
// (mirrors ExtractRootFlag/ExtractQuietFlag). Returns the parsed value and whether the
// flag was present; a bare --server-enabled means true (Go bool-flag convention).
func ExtractServerEnabledFlag(args []string) (val, set bool) {
	for _, a := range args {
		if a == "--" {
			return false, false
		}
		switch {
		case a == "-server-enabled" || a == "--server-enabled":
			return true, true
		case strings.HasPrefix(a, "-server-enabled="), strings.HasPrefix(a, "--server-enabled="):
			_, v, _ := strings.Cut(a, "=")
			if b, err := strconv.ParseBool(v); err == nil {
				return b, true
			}
			return false, false
		}
	}
	return false, false
}

// ExtractVerbosityCount counts the -v tokens before a "--", -vv spelled as two.
func ExtractVerbosityCount(args []string) int {
	n := 0
	for _, a := range quiet.ExpandVerbosityArgs(args) {
		if a == "--" {
			break
		}
		if a == "-v" {
			n++
		}
	}
	return n
}
