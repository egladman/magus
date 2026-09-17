package vm

// StepEvent identifies which VM event fired a step hook.
type StepEvent int

const (
	StepLine   StepEvent = iota // about to execute a statement on a new source line
	StepCall                    // entered a Buzz function call
	StepReturn                  // returning from a Buzz function
)

// StepMask selects which events a step hook subscribes to. Combine with OR.
type StepMask int

const (
	MaskLine StepMask = 1 << iota
	MaskCall
	MaskReturn
)

// DebugFrame describes one activation record for the debugger: the chunk's
// source name, the enclosing function name, and the 1-based current line.
type DebugFrame struct {
	Source string
	// SourceFile is the .buzz path stamped at compile time, or "" when unknown.
	// Distinct from Source, which falls back to the function name for debugger
	// display: line coverage must not treat those labels as paths.
	SourceFile string
	Name       string
	Line       int
}

// frameName returns a human-readable name for f's function ("(top level)" for
// the outermost frame, the closure's recorded name otherwise).
func frameName(f *frame) string {
	if f.chunk != nil && f.chunk.Name != "" {
		return f.chunk.Name
	}
	return "(top level)"
}

// frameToDebug builds a DebugFrame from a live frame. ip-1 is the instruction
// last fetched (the one currently executing / paused at).
func frameToDebug(f *frame) DebugFrame {
	src := "<buzz>"
	sourceFile := ""
	if f.chunk != nil {
		sourceFile = f.chunk.SourceFile
		// Prefer the file the chunk was compiled from. Falling back to the
		// function name kept Debugger displays working before SourceFile existed,
		// and still does for one-shot Compile that never stamps a path.
		if f.chunk.SourceFile != "" {
			src = f.chunk.SourceFile
		} else if f.chunk.Name != "" {
			src = f.chunk.Name
		}
	}
	return DebugFrame{Source: src, SourceFile: sourceFile, Name: frameName(f), Line: f.chunk.lineAt(f.ip - 1)}
}
