package buzz

// This file plugs the Buzz interpreter core's debug seam (core.Session's
// Frames/Locals/Upvalues/CallDepth and step hooks, in core types) into
// the generic engine.DebugReader / engine.Stepper / engine.DriversProvider /
// engine.ReplDriver interfaces the shared REPL and magus.pry() drive against.

import (
	core "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/interp/engine"
)

// Wrap adapts a live core.Session to the generic engine.Session (plus the
// optional REPL/debug interfaces). magus.pry() uses it to hand the session that
// is running a magusfile.buzz to the shared Pry REPL.
func Wrap(c *core.Session) engine.Session { return &session{core: c} }

// Drivers implements engine.DriversProvider: Buzz speaks one language.
func (s *session) Drivers() []engine.ReplDriver {
	return []engine.ReplDriver{&buzzReplDriver{core: s.core}}
}

// --- engine.DebugReader ---

func (s *session) Frames() []engine.Frame {
	cf := s.core.Frames()
	if len(cf) == 0 {
		return nil
	}
	out := make([]engine.Frame, len(cf))
	for i, f := range cf {
		out[i] = engine.Frame{
			Source:      f.Source,
			ShortSrc:    f.Source,
			CurrentLine: f.Line,
			Name:        f.Name,
			What:        "buzz",
		}
	}
	return out
}

func (s *session) Locals(level int) map[string]engine.Value {
	return toEngineMap(s.core.Locals(level))
}

func (s *session) Upvalues(level int) map[string]engine.Value {
	return toEngineMap(s.core.Upvalues(level))
}

func (s *session) CallDepth() int { return s.core.CallDepth() }

// --- engine.Stepper ---

func (s *session) SetStepHook(mask engine.StepMask, cb func(engine.StepEvent, engine.Frame)) {
	s.core.SetStepHook(fromEngineMask(mask), func(ev core.StepEvent, f core.DebugFrame) {
		cb(toEngineEvent(ev), engine.Frame{
			Source:      f.Source,
			ShortSrc:    f.Source,
			CurrentLine: f.Line,
			Name:        f.Name,
			What:        "buzz",
		})
	})
}

func (s *session) ClearStepHook() { s.core.ClearStepHook() }

// --- translation helpers ---

func toEngineMap(m map[string]core.Value) map[string]engine.Value {
	if len(m) == 0 {
		return map[string]engine.Value{}
	}
	out := make(map[string]engine.Value, len(m))
	for k, v := range m {
		out[k] = toEngine(v)
	}
	return out
}

func fromEngineMask(m engine.StepMask) core.StepMask {
	var out core.StepMask
	if m&engine.MaskLine != 0 {
		out |= core.MaskLine
	}
	if m&engine.MaskCall != 0 {
		out |= core.MaskCall
	}
	if m&engine.MaskReturn != 0 {
		out |= core.MaskReturn
	}
	return out
}

func toEngineEvent(ev core.StepEvent) engine.StepEvent {
	switch ev {
	case core.StepCall:
		return engine.StepCall
	case core.StepReturn:
		return engine.StepReturn
	default:
		return engine.StepLine
	}
}
