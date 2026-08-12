package std

import (
	"context"
	"fmt"
	"log/slog"
	"sort"

	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module log -lang buzz -out ../internal/interp/bindings/gen/log.go

func init() { Register(Log) }

// logLevelTrace mirrors config.LevelTrace (slog.LevelDebug-4). Duplicated here
// for the same reason internal/cache does: config imports this package's tree, so
// importing it back would cycle.
const logLevelTrace slog.Level = slog.LevelDebug - 4

// Log is the "log" host module: a magusfile's way to say something at a level.
//
// This is NOT a second logger. Every method here calls slog at magus's own
// process-wide default logger - the one cmd/magus installs from the verbosity
// flags (see cmd/magus/verbosity.go) - which is the same call the host modules
// already make for their own diagnostics (env.set, os.exec, magus.bust_cache).
// A magusfile therefore inherits, with nothing to wire up:
//
//   - LEVEL FILTERING from -q/-v/-vv/-vvv, so log.debug costs nothing on a normal
//     run instead of printing unconditionally.
//   - THE HANDLER, so a message renders in the same compact style as the rest of
//     the run under `pretty`, and as a JSON object under --log-format=json,
//     landing on the same stream as everything else.
//   - SECRET REDACTION, so a value that came from a secret provider is masked on
//     its way out rather than at the call site.
//   - JOURNAL CAPTURE, so the message is in the run's persisted log and reachable
//     later through `magus query output`.
//
// That is the whole reason it exists. `std\print` writes a bare line to the
// script output stream: it cannot be quieted, cannot be raised to a level, is not
// captured as a structured event, and is not redacted. The 100-plus std\print
// calls across this repo's magusfiles all bypass a logging system magus already
// has.
//
// Pure compute at this layer (the handler does the I/O), so it is WASM-safe.
var Log = Module{
	Name: "log",
	WASM: true,
	Doc:  "Emit a message at a level through magus's own logger, so it honors -q/-v/-vv, renders in the run's format, is redacted, and is captured in the run log. Unlike std\\print, which is an uncontrolled bare line.",
	Methods: []Method{
		{
			Name:    "trace",
			Doc:     "Log at trace level (shown at -vvv). For detail worth having when reconstructing a run and noise at any other time.",
			Args:    []Arg{{Name: "message", Type: TypeString}, {Name: "attrs", Type: TypeAnyMap, Optional: true}},
			Returns: nil,
			Impl:    LogTrace,
		},
		{
			Name:    "debug",
			Doc:     "Log at debug level (shown at -v).",
			Args:    []Arg{{Name: "message", Type: TypeString}, {Name: "attrs", Type: TypeAnyMap, Optional: true}},
			Returns: nil,
			Impl:    LogDebug,
		},
		{
			Name:    "info",
			Doc:     "Log at info level (shown by default, hidden by -q).",
			Args:    []Arg{{Name: "message", Type: TypeString}, {Name: "attrs", Type: TypeAnyMap, Optional: true}},
			Returns: nil,
			Impl:    LogInfo,
		},
		{
			Name:    "warn",
			Doc:     "Log at warn level: something the reader should act on eventually.",
			Args:    []Arg{{Name: "message", Type: TypeString}, {Name: "attrs", Type: TypeAnyMap, Optional: true}},
			Returns: nil,
			Impl:    LogWarn,
		},
		{
			Name:    "error",
			Doc:     "Log at error level. This RECORDS a problem; it does not fail the target - raise (or os\\exit) is what ends a run.",
			Args:    []Arg{{Name: "message", Type: TypeString}, {Name: "attrs", Type: TypeAnyMap, Optional: true}},
			Returns: nil,
			Impl:    LogError,
		},
		{
			Name: "at",
			Doc:  "Log at a level chosen at runtime. Use it when the level is DATA rather than a literal - mapping a scanner's severity onto magus's levels, say - and the five named methods when it is not.",
			Args: []Arg{
				{Name: "level", Type: TypeString, Enum: "LogLevel"},
				{Name: "message", Type: TypeString},
				{Name: "attrs", Type: TypeAnyMap, Optional: true},
			},
			Returns: nil,
			Raises:  true,
			Impl:    LogAt,
		},
	},
}

// LogTrace logs message at trace level.
func LogTrace(ctx context.Context, message string, attrs map[string]any) error {
	return logEmit(ctx, logLevelTrace, message, attrs)
}

// LogDebug logs message at debug level.
func LogDebug(ctx context.Context, message string, attrs map[string]any) error {
	return logEmit(ctx, slog.LevelDebug, message, attrs)
}

// LogInfo logs message at info level.
func LogInfo(ctx context.Context, message string, attrs map[string]any) error {
	return logEmit(ctx, slog.LevelInfo, message, attrs)
}

// LogWarn logs message at warn level.
func LogWarn(ctx context.Context, message string, attrs map[string]any) error {
	return logEmit(ctx, slog.LevelWarn, message, attrs)
}

// LogError logs message at error level.
func LogError(ctx context.Context, message string, attrs map[string]any) error {
	return logEmit(ctx, slog.LevelError, message, attrs)
}

// LogAt logs message at the named level.
func LogAt(ctx context.Context, level, message string, attrs map[string]any) error {
	lvl, err := logSlogLevel(level)
	if err != nil {
		return err
	}
	return logEmit(ctx, lvl, message, attrs)
}

// logSlogLevel maps a LogLevel case onto its slog level.
//
// The empty string is REJECTED rather than defaulting to info. Every other
// optional enum in the stdlib treats "" as "unset, use the default", but here the
// level is a required argument: reaching this with "" means a caller computed a
// level and got nothing, and silently logging that at info would bury the bug in
// the output it was trying to produce.
func logSlogLevel(level string) (slog.Level, error) {
	switch types.LogLevel(level) {
	case types.LogTrace:
		return logLevelTrace, nil
	case types.LogDebug:
		return slog.LevelDebug, nil
	case types.LogInfo:
		return slog.LevelInfo, nil
	case types.LogWarn:
		return slog.LevelWarn, nil
	case types.LogError:
		return slog.LevelError, nil
	}
	// The cases are spelled out rather than read from the generated
	// types.LogLevel.Values(): the enums generator imports this package, so a
	// reference to its own output here cannot compile until it has already run.
	return 0, fmt.Errorf("log.at: unknown level %q (want trace|debug|info|warn|error)", level)
}

// logEmit forwards to the process-wide default logger, which is magus's own.
//
// It checks Enabled first so a filtered-out call does not pay to sort and build
// its attributes - a log.trace inside a loop over every file in a workspace is
// the shape that makes that matter.
func logEmit(ctx context.Context, level slog.Level, message string, attrs map[string]any) error {
	l := slog.Default()
	if !l.Enabled(ctx, level) {
		return nil
	}
	l.Log(ctx, level, message, logArgs(attrs)...)
	return nil
}

// logArgs flattens attrs into slog's alternating key/value form, sorted by key.
//
// Sorted because Go randomizes map iteration: without it the same log line would
// order its fields differently on every run, which makes output diffs and golden
// tests noise and makes a reader hunt for the field they want.
func logArgs(attrs map[string]any) []any {
	if len(attrs) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	args := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		args = append(args, k, attrs[k])
	}
	return args
}
