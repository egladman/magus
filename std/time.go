package std

import (
	"context"
	"fmt"
	"time"
)

//go:generate go run ../cmd/magus-scribe bindings -module time -lang buzz -out ../host/gen/time.go

func init() { Register(Time) }

// Time is the "time" host module: timestamp formatting/parsing and duration
// parsing, delegated straight to Go's time package so a spell doesn't reinvent
// calendar math. Times cross the VM boundary as Unix epoch milliseconds carried
// as float64 — the exact type (and unit) Buzz's os.time() already returns, so a
// timestamp flows through os.time() → extra.time without a conversion, and the
// 64-bit value never narrows to a 32-bit int. Layouts are Go reference-time
// strings (e.g. "2006-01-02T15:04:05Z07:00"). Formatting and parsing are anchored
// to UTC, so results are deterministic and location-free.
var Time = Module{
	Name: "time",
	Doc:  "Timestamp formatting/parsing and duration parsing (Go time, UTC).",
	Methods: []Method{
		{
			Name:    "format",
			Doc:     "Render Unix-millis as a string using a Go reference layout (UTC).",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "unix_millis", Type: TypeFloat}},
			Returns: []Ret{{Type: TypeString}},
			Impl:    TimeFormat,
		},
		{
			Name:    "parse",
			Doc:     "Parse a string with a Go reference layout into Unix-millis (UTC); errors on mismatch.",
			Args:    []Arg{{Name: "layout", Type: TypeString}, {Name: "value", Type: TypeString}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    TimeParse,
		},
		{
			Name:    "parse_duration",
			Doc:     "Parse a Go duration string (e.g. \"168h\", \"1h30m\") into milliseconds; errors on mismatch.",
			Args:    []Arg{{Name: "duration", Type: TypeString}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    TimeParseDuration,
		},
		{
			Name:    "now_iso",
			Doc:     "Return the current UTC time as an RFC 3339 string. For the raw epoch-millis value use Buzz's os.time().",
			Args:    nil,
			Returns: []Ret{{Type: TypeString}},
			Impl:    TimeNowISO,
		},
		{
			Name:    "add",
			Doc:     "Add a Go duration string (e.g. \"24h\", \"-1h30m\") to a Unix-millis timestamp; returns the new Unix-millis timestamp.",
			Args:    []Arg{{Name: "unix_millis", Type: TypeFloat}, {Name: "duration", Type: TypeString}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    TimeAdd,
		},
		{
			Name:    "diff",
			Doc:     "Return a minus b in milliseconds (positive when a is later than b).",
			Args:    []Arg{{Name: "a", Type: TypeFloat}, {Name: "b", Type: TypeFloat}},
			Returns: []Ret{{Type: TypeFloat}},
			Impl:    TimeDiff,
		},
	},
}

// TimeFormat renders unixMillis (interpreted as UTC) with a Go reference layout.
func TimeFormat(_ context.Context, layout string, unixMillis float64) (string, error) {
	return time.UnixMilli(int64(unixMillis)).UTC().Format(layout), nil
}

// TimeNowISO returns the current UTC time formatted as RFC 3339. The raw
// epoch-millis clock value is already available as Buzz's os.time(); this is the
// formatted-string convenience the time module would otherwise force a caller to
// build by hand via os.time() + time.format.
func TimeNowISO(_ context.Context) (string, error) {
	return time.Now().UTC().Format(time.RFC3339), nil
}

// TimeParse parses value with layout into Unix epoch milliseconds. A zoneless
// layout is read as UTC (Go's default); a zoned one (RFC 3339's Z07:00) is
// normalized to the UTC instant.
func TimeParse(_ context.Context, layout, value string) (float64, error) {
	t, err := time.Parse(layout, value)
	if err != nil {
		return 0, fmt.Errorf("time.parse: %w", err)
	}
	return float64(t.UnixMilli()), nil
}

// TimeParseDuration parses a Go duration string into whole milliseconds.
func TimeParseDuration(_ context.Context, duration string) (float64, error) {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return 0, fmt.Errorf("time.parse_duration: %w", err)
	}
	return float64(d.Milliseconds()), nil
}

// TimeAdd adds a Go duration string to a Unix-millis timestamp and returns the
// resulting Unix-millis value.
func TimeAdd(_ context.Context, unixMillis float64, duration string) (float64, error) {
	d, err := time.ParseDuration(duration)
	if err != nil {
		return 0, fmt.Errorf("time.add: %w", err)
	}
	return float64(time.UnixMilli(int64(unixMillis)).Add(d).UnixMilli()), nil
}

// TimeDiff returns a minus b in milliseconds.
func TimeDiff(_ context.Context, a, b float64) (float64, error) {
	return a - b, nil
}
