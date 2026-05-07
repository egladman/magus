package buzz

import (
	"context"
	"strings"

	core "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/interp/engine"
)

// buzzHostBindingNames lists names the magus host injects into a Buzz session;
// the REPL omits them from .globals output.
var buzzHostBindingNames = []string{"magus"}

// buzzReplDriver implements engine.ReplDriver for Buzz evaluation, driving the
// concrete core.Session (Eval/Globals) the generic engine.Session can't reach.
type buzzReplDriver struct{ core *core.Session }

func (d *buzzReplDriver) Language() string { return "buzz" }

func (d *buzzReplDriver) EvalLine(snippet string) ([]engine.Value, error) {
	// Compile (not run) the expression form first so bare expressions print a
	// result; only on a compile error fall back to the statement form. Compiling
	// before running — rather than try-running both — means a snippet with side
	// effects can never execute twice (mirrors the Lua/Teal drivers).
	chunk, err := d.core.Compile("return " + snippet)
	if err != nil {
		chunk, err = d.core.Compile(snippet)
		if err != nil {
			return nil, err
		}
	}
	v, err := d.core.EvalChunk(context.Background(), chunk)
	if err != nil {
		return nil, err
	}
	if v.IsNull() {
		return nil, nil
	}
	return []engine.Value{toEngine(v)}, nil
}

// IsIncomplete reports whether err is the parser hitting end-of-input mid-form,
// signalling the REPL to read another line. Buzz primarily relies on LineDelta
// brace counting; this catches single-line forms that need a continuation.
func (d *buzzReplDriver) IsIncomplete(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "got EOF") || strings.Contains(msg, "unexpected token EOF")
}

func (d *buzzReplDriver) LineDelta(line string) int { return bracketDepthDelta(line) }

func (d *buzzReplDriver) HostBindingNames() []string { return buzzHostBindingNames }

func (d *buzzReplDriver) UserGlobals() map[string]engine.Value {
	skip := make(map[string]struct{}, len(buzzHostBindingNames))
	for _, n := range buzzHostBindingNames {
		skip[n] = struct{}{}
	}
	out := map[string]engine.Value{}
	for name, v := range d.core.Globals() {
		if _, skipped := skip[name]; skipped {
			continue
		}
		out[name] = toEngine(v)
	}
	return out
}

// bracketDepthDelta returns the net change in {}/()/[ ] depth for a line of
// Buzz source, ignoring characters inside double-quoted string literals (and
// thus their {expr} interpolations, which are balanced within the literal).
func bracketDepthDelta(line string) int {
	delta := 0
	inStr := false
	escaped := false
	for _, ch := range line {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if inStr {
			if ch == '"' {
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{', '(', '[':
			delta++
		case '}', ')', ']':
			delta--
		}
	}
	return delta
}
