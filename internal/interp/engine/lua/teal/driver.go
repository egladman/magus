package teal

import (
	"strings"

	"github.com/egladman/magus/internal/interp/engine"
	lua "github.com/egladman/magus/internal/interp/engine/lua"
)

// Driver implements engine.ReplDriver for Teal-mode evaluation.
type Driver struct{ r lua.Session }

// NewDriver returns a Teal REPL driver backed by r; r must have the compiler loaded.
func NewDriver(r lua.Session) *Driver {
	return &Driver{r: r}
}

func (d *Driver) Language() string { return "teal" }

func (d *Driver) EvalLine(snippet string) ([]engine.Value, error) {
	code, err := CompileSnippet(d.r, "return "+snippet)
	if err != nil {
		code, err = CompileSnippet(d.r, snippet)
		if err != nil {
			return nil, err
		}
		fn, loadErr := d.r.LoadString(string(code))
		if loadErr != nil {
			return nil, loadErr
		}
		top := d.r.GetTop()
		if cerr := d.r.Call(engine.CallParams{Fn: fn, NRet: -1, Protect: true}); cerr != nil {
			return nil, cerr
		}
		d.r.Pop(d.r.GetTop() - top)
		return nil, nil
	}
	fn, loadErr := d.r.LoadString(string(code))
	if loadErr != nil {
		return nil, loadErr
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

func (d *Driver) IsIncomplete(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "'<eof>'") || strings.Contains(msg, "<eof>")
}

func (d *Driver) LineDelta(_ string) int { return 0 }

func (d *Driver) HostBindingNames() []string { return lua.LuaHostBindings }

func (d *Driver) UserGlobals() map[string]engine.Value {
	return lua.NewDriver(d.r).UserGlobals()
}
