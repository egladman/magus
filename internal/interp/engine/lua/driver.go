package lua

import (
	"strings"

	"github.com/egladman/magus/internal/interp/engine"
)

// LuaHostBindings lists names injected by the host; the REPL filters them from .globals.
var LuaHostBindings = []string{
	"magus", "_magus_targets", "_magus_dep_visiting", "_G", "_ENV",
	"string", "table", "math", "os", "io", "package", "coroutine",
	"debug", "bit", "bit32", "utf8",
}

type luaReplDriver struct{ r Session }

// NewDriver creates a Lua REPL driver backed by r.
func NewDriver(r Session) engine.ReplDriver { return &luaReplDriver{r: r} }

func (d *luaReplDriver) Language() string { return "lua" }

func (d *luaReplDriver) EvalLine(snippet string) ([]engine.Value, error) {
	fn, err := d.r.LoadString("return " + snippet) // try expression first
	if err != nil {
		fn, err = d.r.LoadString(snippet)
		if err != nil {
			return nil, err
		}
		top := d.r.GetTop()
		if cerr := d.r.Call(engine.CallParams{Fn: fn, NRet: -1, Protect: true}); cerr != nil {
			return nil, cerr
		}
		d.r.Pop(d.r.GetTop() - top)
		return nil, nil
	}
	top := d.r.GetTop()
	if err := d.r.Call(engine.CallParams{Fn: fn, NRet: -1, Protect: true}); err != nil {
		return nil, err
	}
	nret := d.r.GetTop() - top
	vals := make([]engine.Value, nret)
	for i := 0; i < nret; i++ {
		vals[i] = d.r.Get(-(nret - i))
	}
	d.r.Pop(nret)
	return vals, nil
}

func (d *luaReplDriver) IsIncomplete(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "'<eof>'") || strings.Contains(msg, "<eof>")
}

func (d *luaReplDriver) LineDelta(_ string) int { return 0 }

func (d *luaReplDriver) HostBindingNames() []string { return LuaHostBindings }

func (d *luaReplDriver) UserGlobals() map[string]engine.Value {
	g := d.r.GetGlobal("_G")
	tbl, ok := g.AsTable()
	if !ok {
		return nil
	}
	skip := make(map[string]struct{}, len(LuaHostBindings))
	for _, n := range LuaHostBindings {
		skip[n] = struct{}{}
	}
	out := map[string]engine.Value{}
	tbl.ForEach(func(k, v engine.Value) {
		s, ok := k.AsString()
		if !ok {
			return
		}
		if _, skipped := skip[s]; skipped {
			return
		}
		if strings.HasPrefix(s, "_") && strings.HasSuffix(s, "_") {
			return
		}
		out[s] = v
	})
	return out
}
