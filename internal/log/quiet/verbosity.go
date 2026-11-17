package quiet

import (
	"log/slog"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/config"
)

// Verbosity is a counted flag value (flag.Value); IsBoolFlag lets -v be used without an argument.
type Verbosity int

func (v *Verbosity) String() string   { return strconv.Itoa(int(*v)) }
func (v *Verbosity) Set(string) error { *v++; return nil }
func (*Verbosity) IsBoolFlag() bool   { return true }

// ExpandVerbosityArgs rewrites each -vv run as separate -v tokens, up to a "--".
func ExpandVerbosityArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i, a := range args {
		if a == "--" {
			// Past the separator the tokens belong to a forwarded tool, not to magus;
			// this builds the master argv startup() parses, so copy the rest through
			// unchanged instead of expanding -vvv-lookalikes meant for the tool.
			out = append(out, args[i:]...)
			break
		}
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			allV := true
			for _, c := range a[1:] {
				if c != 'v' {
					allV = false
					break
				}
			}
			if allV {
				for range a[1:] {
					out = append(out, "-v")
				}
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

// EffectiveLevel maps verbosity/quiet flags to a slog.Level; quiet wins over -v.
//
// -v and -vv share the debug level on purpose: they differ in whether each
// target's own output streams (Log.Stream, set in applyDisplay), not in which
// records are admitted. Splitting them by level instead would mean -v silently
// dropping debug records, which is the opposite of what the flag is for.
func EffectiveLevel(v Verbosity, quiet bool) slog.Level {
	switch {
	case quiet:
		return slog.LevelError
	case v >= 3:
		return config.LevelTrace
	case v >= 1:
		return slog.LevelDebug
	default:
		return slog.LevelInfo
	}
}

// LevelName spells lvl the way log.level accepts it.
func LevelName(lvl slog.Level) string {
	if lvl == config.LevelTrace {
		return "trace"
	}
	// Lowercase, the spelling log.level accepts: this value is written back into the
	// config and validated with it, and slog spells its own levels in capitals.
	return strings.ToLower(lvl.String())
}
