package cmdline

import (
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// unitPhrase is the English form a help line uses when a bare number takes Unit.
var unitPhrase = map[string]string{
	"ns": "nanoseconds",
	"us": "microseconds",
	"µs": "microseconds",
	"ms": "milliseconds",
	"s":  "seconds",
	"m":  "minutes",
	"h":  "hours",
}

// ParseDuration reads a Go duration (5m, 420ms, 1h30m). A bare number takes
// bareUnit (e.g. "ms"): that unit is the flag's declared default, named in help
// so a caller never has to guess. An empty bareUnit refuses a bare number.
func ParseDuration(s, bareUnit string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty duration")
	}
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return 0, fmt.Errorf("invalid duration %q (want a Go duration like 5m or 420ms%s)", s, bareHint(bareUnit))
	}
	if bareUnit == "" {
		return 0, fmt.Errorf("%q has no unit: write one (e.g. %sms, 5m)", s, s)
	}
	d, err := time.ParseDuration(s + bareUnit)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q with bare unit %q: %w", s, bareUnit, err)
	}
	return d, nil
}

func bareHint(unit string) string {
	if unit == "" {
		return ""
	}
	phrase, ok := unitPhrase[unit]
	if !ok {
		phrase = unit
	}
	return fmt.Sprintf("; bare numbers are %s", phrase)
}

// DurationUsage appends the bare-unit sentence to a Duration flag's Doc so help,
// the man page and -h cannot forget it. Doc that already names bare numbers is
// left alone.
func DurationUsage(f Flag) string {
	doc := strings.TrimSpace(f.Doc)
	if f.Kind != Duration || f.Unit == "" {
		return doc
	}
	if strings.Contains(strings.ToLower(doc), "bare numbers are") {
		return doc
	}
	phrase, ok := unitPhrase[f.Unit]
	if !ok {
		phrase = f.Unit
	}
	if doc == "" {
		return fmt.Sprintf("Bare numbers are %s.", phrase)
	}
	return strings.TrimRight(doc, ".") + ". Bare numbers are " + phrase + "."
}

// durationValue is a flag.Value that stores into *time.Duration and accepts a
// bare number as Unit.
type durationValue struct {
	p    *time.Duration
	unit string
}

func (v durationValue) String() string {
	if v.p == nil {
		return ""
	}
	return v.p.String()
}

func (v durationValue) Set(s string) error {
	d, err := ParseDuration(s, v.unit)
	if err != nil {
		return err
	}
	*v.p = d
	return nil
}

// DurationVar registers name on fs as a duration that accepts Go duration
// strings and bare numbers counted in unit (e.g. "ms"). usage should already
// be DurationUsage of the flag so the default unit is loud in help.
func DurationVar(fs *flag.FlagSet, p *time.Duration, name string, value time.Duration, unit, usage string) {
	*p = value
	fs.Var(durationValue{p: p, unit: unit}, name, usage)
}
