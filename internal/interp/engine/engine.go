// Package engine defines the Engine and Session interfaces that all scripting
// engine implementations must satisfy, along with the engine registry.
//
// Concurrency model: a Session is NOT safe for concurrent use from multiple
// goroutines. The pool (magus/internal/interp/pool) ensures each Session is
// owned by exactly one goroutine at a time. Parallelism is achieved by
// maintaining N sessions across N pool workers — no session is shared.
package engine

import (
	"context"
	"fmt"
	"sync"
)

// Engine creates Sessions for script execution.
// Implementations register themselves at init() time via Register.
type Engine interface {
	// ID returns a stable identifier included in compile-cache keys so
	// compiled entries from different engines never cross-pollute.
	ID() string
	// NewSession returns a fresh session with standard libraries loaded
	// and ctx bound for cancellation. Pass context.Background() for no cancellation.
	NewSession(ctx context.Context) (Session, error)
}

// Session is a single isolated script-execution context.
// A Session is NOT safe for concurrent use from multiple goroutines;
// the pool ensures each Session is owned by exactly one goroutine at a time.
type Session interface {
	Close() error

	SetGlobal(name string, v Value)
	GetGlobal(name string) Value

	NewTable() Table
	LoadString(code string) (Value, error)
	DoString(code string) error
	Call(p CallParams) error
}

// Value is a script-side value handle. Engine implementations choose their
// own concrete representation; callers use the methods below for type-safe access.
type Value interface {
	IsNil() bool
	String() string
	AsString() (string, bool)
	AsNumber() (float64, bool)
	AsBool() bool
	AsTable() (Table, bool)
	AsFunction() (Value, bool)
}

// Table is a script-side key-value collection. Constructed via Session.NewTable().
type Table interface {
	Value
	RawSetString(key string, v Value)
	RawGetString(key string) Value
	RawSetInt(key int, v Value)
	RawGetInt(key int) Value
	ForEach(fn func(k, v Value))
	Len() int
}

// CallParams configures a Session.Call invocation. Fn is the compiled value to
// run; the buzz engine ignores arguments and return-value counts (callers that
// need them use the concrete Session.CallValue), so only Fn is carried here.
type CallParams struct {
	Fn Value
}

// StringValue wraps s as a string Value.
func StringValue(s string) Value { return strVal(s) }

// NumberValue wraps n as a numeric Value.
func NumberValue(n float64) Value { return numVal(n) }

// BoolValue wraps b as a boolean Value.
func BoolValue(b bool) Value { return boolVal(b) }

// NilValue is the nil Value.
var NilValue Value = nilVal{}

type strVal string

func (v strVal) IsNil() bool               { return false }
func (v strVal) String() string            { return string(v) }
func (v strVal) AsString() (string, bool)  { return string(v), true }
func (v strVal) AsNumber() (float64, bool) { return 0, false }
func (v strVal) AsBool() bool              { return true }
func (v strVal) AsTable() (Table, bool)    { return nil, false }
func (v strVal) AsFunction() (Value, bool) { return nil, false }

type numVal float64

func (v numVal) IsNil() bool               { return false }
func (v numVal) String() string            { return fmt.Sprintf("%g", float64(v)) }
func (v numVal) AsString() (string, bool)  { return "", false }
func (v numVal) AsNumber() (float64, bool) { return float64(v), true }
func (v numVal) AsBool() bool              { return true }
func (v numVal) AsTable() (Table, bool)    { return nil, false }
func (v numVal) AsFunction() (Value, bool) { return nil, false }

type boolVal bool

func (v boolVal) IsNil() bool { return false }
func (v boolVal) String() string {
	if bool(v) {
		return "true"
	}
	return "false"
}
func (v boolVal) AsString() (string, bool)  { return "", false }
func (v boolVal) AsNumber() (float64, bool) { return 0, false }
func (v boolVal) AsBool() bool              { return bool(v) }
func (v boolVal) AsTable() (Table, bool)    { return nil, false }
func (v boolVal) AsFunction() (Value, bool) { return nil, false }

type nilVal struct{}

func (nilVal) IsNil() bool               { return true }
func (nilVal) String() string            { return "nil" }
func (nilVal) AsString() (string, bool)  { return "", false }
func (nilVal) AsNumber() (float64, bool) { return 0, false }
func (nilVal) AsBool() bool              { return false }
func (nilVal) AsTable() (Table, bool)    { return nil, false }
func (nilVal) AsFunction() (Value, bool) { return nil, false }

var (
	engineMu sync.Mutex
	engines  = map[string]Engine{}
)

// Register registers eng under name. Called from engine init() functions. Panics on duplicate.
func Register(name string, eng Engine) {
	engineMu.Lock()
	defer engineMu.Unlock()
	if _, ok := engines[name]; ok {
		panic(fmt.Sprintf("engine: duplicate registration: %q", name))
	}
	engines[name] = eng
}

// Lookup returns the engine registered under name, or nil if not found.
func Lookup(name string) Engine {
	engineMu.Lock()
	defer engineMu.Unlock()
	return engines[name]
}
