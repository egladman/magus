package buzz

import vmpackage "github.com/egladman/gopherbuzz/vm"

// This file is the interpreter core's debug seam: stack introspection and
// line-level step hooks, expressed in core types. The magus engine
// adapter (internal/interp/engine/buzz) translates these into the generic
// engine.DebugReader / engine.Stepper interfaces — the core never imports magus.
//
// All methods read the session's currently-executing VM (curVM), which Exec/
// ExecChunk/Eval/CallValue set for the duration of a run. They are meaningful
// only while the goroutine is paused inside that run (i.e. from within a
// magus.pry() direct call or a step-hook callback); outside a run they report
// empty.

// Frames returns the active call stack of the currently-executing VM, innermost
// first. Empty when no run is in progress.
func (s *Session) Frames() []vmpackage.DebugFrame {
	if s.curVM == nil {
		return nil
	}
	return s.curVM.DebugFrames()
}

// CallDepth reports the number of active frames in the current VM. Used by
// step-over to detect frame-boundary crossings.
func (s *Session) CallDepth() int {
	if s.curVM == nil {
		return 0
	}
	return s.curVM.CallDepth()
}

// Locals returns the named locals of the frame at level (0 = innermost) in the
// current VM, read from its stack register window. Empty when the level is out
// of range or no debug-name info was compiled.
func (s *Session) Locals(level int) map[string]vmpackage.Value {
	if s.curVM == nil {
		return nil
	}
	return s.curVM.DebugLocals(level)
}

// Upvalues returns the captured upvalues of the frame at level (0 = innermost),
// keyed by name. Empty when the frame is not a closure or no names were compiled.
func (s *Session) Upvalues(level int) map[string]vmpackage.Value {
	if s.curVM == nil {
		return nil
	}
	return s.curVM.DebugUpvalues(level)
}

// SetStepHook installs cb to fire on the current VM for events matching mask.
// cb runs synchronously on the execution goroutine and may re-enter the pry
// REPL. It applies to the VM currently executing; if none is active the hook is
// stored and applied to the next run that starts.
func (s *Session) SetStepHook(mask vmpackage.StepMask, cb func(vmpackage.StepEvent, vmpackage.DebugFrame)) {
	s.stepHook = cb
	s.stepMask = mask
	if s.curVM != nil {
		s.curVM.SetStepHook(mask, cb)
	}
}

// ClearStepHook removes any installed step hook from the session and the current VM.
func (s *Session) ClearStepHook() {
	s.stepHook = nil
	s.stepMask = 0
	if s.curVM != nil {
		s.curVM.ClearStepHook()
	}
}

// Ensure the vmpackage import is used (it's used via type aliases in vm_exports.go,
// but we reference it here directly too via the method receivers on *VM).
var _ = vmpackage.StepLine
