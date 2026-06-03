//go:build cgo_engines

// wazero is a pure-Go WebAssembly runtime whose compiler backend emits native
// machine code at module-compile time — i.e. a real JIT, with no cgo. It is the
// only pure-Go JIT in this comparison. Its "language" is WebAssembly, so the
// compute kernels are written in C and compiled to wasm32 (see internal/wasm);
// this measures the ceiling a pure-Go native-codegen runtime reaches on a
// compiled kernel, not a scripting engine.
//
// Grouped under the cgo_engines tag with the other non-default engines for one
// opt-in switch, though wazero itself needs no C toolchain at run time (the
// .wasm is embedded). Unlike LuaJIT/Umka, wazero allocates on the Go heap, so
// its -benchmem IS captured and comparable.
package comparison

import (
	"context"
	_ "embed"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed internal/wasm/kernels.wasm
var wasmKernels []byte

// wazeroEngine holds a compiler-backed runtime and the compiled (JIT'd) module.
type wazeroEngine struct {
	ctx      context.Context
	rt       wazero.Runtime
	compiled wazero.CompiledModule
}

func wazeroNew() (wazeroEngine, error) {
	ctx := context.Background()
	rt := wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfigCompiler())
	compiled, err := rt.CompileModule(ctx, wasmKernels)
	if err != nil {
		_ = rt.Close(ctx)
		return wazeroEngine{}, err
	}
	return wazeroEngine{ctx, rt, compiled}, nil
}

// instantiate creates a fresh module instance (fresh linear memory). The kernels
// export anonymous (empty-name) modules, which wazero allows to coexist.
func (e wazeroEngine) instantiate() (api.Module, error) {
	return e.rt.InstantiateModule(e.ctx, e.compiled, wazero.NewModuleConfig().WithName(""))
}

func (e wazeroEngine) close() { _ = e.rt.Close(e.ctx) }
