package vm

// Env is a lexical scope: a chain of name→Value arrays.
// Variables are stored in a flat slots slice and looked up via a names map
// (map[string]int32) rather than a map[string]Value. This keeps the hot-path
// assign/get working on small integer values (4 B) instead of copying 32-byte
// Values through the map layer.
type Env struct {
	slots  []Value
	names  map[string]int32
	parent *Env
}

func newEnv(parent *Env) *Env {
	return &Env{names: make(map[string]int32), parent: parent}
}

// NewEnv creates a new top-level environment with no parent.
func NewEnv() *Env { return newEnv(nil) }

func (e *Env) define(name string, v Value) {
	if n, ok := e.names[name]; ok {
		e.slots[n] = v
		return
	}
	e.names[name] = int32(len(e.slots))
	e.slots = append(e.slots, v)
}

// Define binds name to v in this environment (exported).
func (e *Env) Define(name string, v Value) { e.define(name, v) }

func (e *Env) get(name string) (Value, bool) {
	for s := e; s != nil; s = s.parent {
		if n, ok := s.names[name]; ok {
			return s.slots[n], true
		}
	}
	return Null, false
}

// Get resolves name through the env chain (exported).
func (e *Env) Get(name string) (Value, bool) { return e.get(name) }

// Names returns all name→slot entries at the top level of this env (not parent).
func (e *Env) Names() map[string]int32 { return e.names }

// Slots returns the value slots of this env (not parent).
func (e *Env) Slots() []Value { return e.slots }

// getWithSlot resolves name and returns the containing Env and its slot index.
// Used by the VM's inline name cache to avoid repeated map lookups on hot paths.
func (e *Env) getWithSlot(name string) (Value, *Env, int32, bool) {
	for s := e; s != nil; s = s.parent {
		if n, ok := s.names[name]; ok {
			return s.slots[n], s, n, true
		}
	}
	return Null, nil, -1, false
}
