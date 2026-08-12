package types

// LogLevel names the severity a log\at call emits at.
//
// A named type with a declared case list rather than a bare string, matching
// [SignAlgorithm] and [PlatformStyle]: a level is a closed set, and a typo in one
// should be a checker error rather than a message that silently never prints.
//
// The cases are magus's own levels, not slog's: magus adds `trace` below debug
// (see config.LevelTrace) for the `-vvv` tier, so a magusfile can reach every
// verbosity the CLI exposes.
type LogLevel string

const (
	// LogTrace is the -vvv tier: detail useful when reconstructing what a run did,
	// and noise at any other time.
	LogTrace LogLevel = "trace"
	// LogDebug is the -v tier.
	LogDebug LogLevel = "debug"
	// LogInfo is the default tier: what a run says when nothing is wrong.
	LogInfo LogLevel = "info"
	// LogWarn reports something the reader should act on eventually.
	LogWarn LogLevel = "warn"
	// LogError reports something that already went wrong. It does NOT fail the
	// target - raising does that; this only records.
	LogError LogLevel = "error"
)
