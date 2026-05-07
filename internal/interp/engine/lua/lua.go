// Package lua defines the Session interface for Lua sessions and the GoFunc trampoline type.
package lua

import (
	"context"

	"github.com/egladman/magus/internal/interp/engine"
)

// Session is the per-session Lua interface; not safe for concurrent use.
type Session interface {
	engine.Session

	Push(v engine.Value)
	Pop(n int)
	Get(idx int) engine.Value
	GetTop() int

	NewFunction(fn GoFunc) engine.Value

	CheckString(argIdx int) string
	CheckNumber(argIdx int) float64
	CheckInt(argIdx int) int
	CheckTable(argIdx int) engine.Table
	CheckFunction(argIdx int) engine.Value
	CheckAny(argIdx int) engine.Value

	RaiseError(format string, args ...any)
	ArgError(argIdx int, msg string)
}

// GoFunc is the Go function type callable from Lua; ctx enables cancellation propagation.
type GoFunc = func(ctx context.Context, r Session) int
