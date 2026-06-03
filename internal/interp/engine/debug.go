package engine

// Frame describes one entry on a scripting engine's call stack as reported
// by the engine's debug API. Source is the chunk name as the engine reports it
// (e.g. "@magusfile.bzz"); ShortSrc is a truncated form suitable for display;
// CurrentLine is the 1-based line number, or -1 if unknown.
type Frame struct {
	Source      string
	ShortSrc    string
	CurrentLine int
	Name        string // function name when discoverable
	What        string // engine-reported frame kind (e.g. "main", "tail")
}

// StepEvent identifies which engine event fired a step hook.
type StepEvent int

const (
	StepLine StepEvent = iota
	StepCall
	StepReturn
)

// StepMask selects which engine events the step hook subscribes to.
// Combine values with bitwise OR.
type StepMask int

const (
	MaskLine StepMask = 1 << iota
	MaskCall
	MaskReturn
)

// DebugReader is an optional interface that sessions may implement to expose
// call-stack introspection. magus.pry() type-asserts the session to
// DebugReader at call time; sessions that don't implement it report no frames.
type DebugReader interface {
	// Frames walks the active call stack from innermost to outermost,
	// skipping host (Go/C) frames. The slice is fresh on every call.
	Frames() []Frame

	// Locals returns the named locals at the frame indicated by level
	// (0 = innermost). Returns an empty map if the level is out of range.
	Locals(level int) map[string]Value

	// Upvalues returns the captured upvalues at the frame indicated by level.
	// Returns an empty map if the function has no upvalues or the level is
	// out of range.
	Upvalues(level int) map[string]Value

	// CallDepth reports the number of active call frames. Used by step-over
	// to know when a frame boundary is crossed.
	CallDepth() int
}

// Stepper is an optional interface for sessions that support line-level step
// hooks. LuaJIT uses C-level debug hooks; gopher-lua uses source-level
// instrumentation (gopherlua.rewriteSteps) that injects hook trampolines
// before each statement at compile time.
//
// SetStepHook installs cb to fire on the next event matching mask. cb is
// invoked synchronously on the script execution goroutine and may re-enter
// the pry REPL (the pry loop is re-entrant). After cb returns, execution
// resumes. ClearStepHook removes any installed hook.
type Stepper interface {
	SetStepHook(mask StepMask, cb func(StepEvent, Frame))
	ClearStepHook()
}

// ReplDriver is an optional interface sessions expose to allow the shared
// REPL to evaluate snippets, detect partial-input continuation, and filter
// host-injected globals — without knowing the engine's surface language.
//
// Each engine exposes one driver per language it speaks; the REPL switches
// between drivers by language.
type ReplDriver interface {
	// Language returns the driver's name (e.g. "buzz").
	Language() string

	// EvalLine evaluates snippet. Implementations should first try
	// "return "+snippet so that bare expressions print a result; if that
	// fails with a syntax error they should fall back to running snippet
	// as a statement. Returns (nil, nil) if execution succeeded with no
	// printable value. Multiple return values are supported.
	EvalLine(snippet string) ([]Value, error)

	// IsIncomplete returns true when err indicates that snippet is a partial
	// statement that needs more input (e.g. an unexpected end-of-input error).
	// The REPL accumulates additional lines when this returns true.
	IsIncomplete(err error) bool

	// LineDelta returns the net open-block delta for this line of source (e.g.
	// bracket-depth counting for JS). Engines that rely on error-based
	// continuation instead return 0 always. The REPL buffers more input while
	// the cumulative delta is positive.
	LineDelta(line string) int

	// HostBindingNames returns the names injected by the host runtime so the
	// REPL can omit them from .globals output.
	HostBindingNames() []string

	// UserGlobals returns the current user-defined globals with host bindings
	// filtered out. Returns nil when the engine does not support listing globals.
	UserGlobals() map[string]Value
}

// DriversProvider is an optional interface for sessions that expose
// language-specific REPL drivers. The shared REPL calls Drivers() to get
// the available drivers and picks the appropriate one for each language mode.
type DriversProvider interface {
	Drivers() []ReplDriver
}
