package comparison

import (
	"context"
	"testing"

	tengo "github.com/d5/tengo/v2"
	tengostdlib "github.com/d5/tengo/v2/stdlib"
	"github.com/dop251/goja"
	buzz "github.com/egladman/gopherbuzz"
	vmpkg "github.com/egladman/gopherbuzz/vm"
	lua "github.com/yuin/gopher-lua"
)

// TestExtraStringWorkloadsAgree guards the honest-comparison string workloads
// (KmerCount, SubstringSearch): a cross-language benchmark only means something
// if every engine computes the same answer, so this runs each engine's program
// once and asserts they agree before any timing is trusted.
func TestExtraStringWorkloadsAgree(t *testing.T) {
	cases := []struct{ name, tengoVar string }{
		{"KmerCount", "total"},
		{"SubstringSearch", "count"},
	}
	for _, tc := range cases {
		w := workloadByName(t, tc.name)
		bz := buzzResult(t, w.bzHot)
		lv := luaResult(t, w.lua)
		tg := tengoResult(t, w.tengo, tc.tengoVar)
		js := gojaResult(t, w.js)
		if bz != lv || bz != tg || bz != js {
			t.Errorf("%s engines disagree: buzz=%d lua=%d tengo=%d goja=%d", tc.name, bz, lv, tg, js)
			continue
		}
		t.Logf("%s = %d (buzz == lua == tengo == goja)", tc.name, bz)
	}
}

func workloadByName(t *testing.T, name string) workload {
	t.Helper()
	for _, w := range workloads {
		if w.name == name {
			return w
		}
	}
	t.Fatalf("workload %q not found", name)
	return workload{}
}

func buzzResult(t *testing.T, program string) int64 {
	t.Helper()
	prog, err := buzz.ParseEmbedded(program)
	if err != nil {
		t.Fatalf("buzz parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("buzz compile: %v", err)
	}
	env := vmpkg.NewEnv()
	vmpkg.RegisterStdlib(env)
	v, err := vmpkg.NewVM(context.Background()).Run(chunk, env)
	if err != nil {
		t.Fatalf("buzz run: %v", err)
	}
	return v.AsInt()
}

func luaResult(t *testing.T, src string) int64 {
	t.Helper()
	L := lua.NewState()
	defer L.Close()
	fn, err := L.LoadString(src)
	if err != nil {
		t.Fatalf("lua load: %v", err)
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		t.Fatalf("lua call: %v", err)
	}
	r := L.Get(-1)
	L.Pop(1)
	return int64(lua.LVAsNumber(r))
}

func tengoResult(t *testing.T, src, varName string) int64 {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	s.SetImports(tengostdlib.GetModuleMap("math"))
	c, err := s.Run()
	if err != nil {
		t.Fatalf("tengo run: %v", err)
	}
	return c.Get(varName).Int64()
}

func gojaResult(t *testing.T, src string) int64 {
	t.Helper()
	v, err := goja.New().RunString(src)
	if err != nil {
		t.Fatalf("goja run: %v", err)
	}
	return v.ToInteger()
}
