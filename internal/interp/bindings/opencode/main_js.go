//go:build js && wasm

package opencode //nolint:hostagnostic // the package is named for the product it binds

import (
	"context"
	"errors"
	"fmt"
	"syscall/js"
	"time"
)

// abiVersion is the API version the loader script checks before use.
const abiVersion = 1

// Serve publishes the wasm API on globalThis and blocks for the life of the
// JavaScript runtime.
func Serve() {
	js.Global().Set("__magusOpenCodeWasmV1", map[string]any{"version": abiVersion, "create": js.FuncOf(create), "createPlugin": js.FuncOf(createPluginHook)})
	select {}
}

func promise(work func() (any, error)) js.Value {
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any {
		resolve, reject := args[0], args[1]
		go func() {
			value, err := javascript(work)
			if err != nil {
				reject.Invoke(js.Global().Get("Error").New(err.Error()))
				return
			}
			resolve.Invoke(value)
		}()
		return nil
	})
	value := js.Global().Get("Promise").New(executor)
	executor.Release()
	return value
}

func rejected(err error) js.Value { return promise(func() (any, error) { return nil, err }) }

// syscall/js surfaces thrown JavaScript exceptions as panics. They must
// reject the corresponding hook, not end every plugin instance in the VM.
func javascript(work func() (any, error)) (value any, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("harness plugin: JavaScript runtime: %v", failure)
		}
	}()
	return work()
}

type settled struct {
	value js.Value
	err   error
}

func rejection(args []js.Value) string {
	message := "native invocation rejected"
	if len(args) > 0 {
		_, _ = javascript(func() (any, error) { message = js.Global().Get("String").Invoke(args[0]).String(); return nil, nil })
	}
	return message
}

func await(ctx context.Context, pending js.Value, cancel func()) (js.Value, error) {
	done := make(chan settled, 1)
	resolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
		value := js.Undefined()
		if len(args) > 0 {
			value = args[0]
		}
		done <- settled{value: value}
		return nil
	})
	reject := js.FuncOf(func(_ js.Value, args []js.Value) any {
		done <- settled{err: fmt.Errorf("harness plugin: %s", rejection(args))}
		return nil
	})
	release := func() { resolve.Release(); reject.Release() }
	if _, err := javascript(func() (any, error) { pending.Call("then", resolve, reject); return nil, nil }); err != nil {
		release()
		return js.Undefined(), err
	}
	select {
	case result := <-done:
		release()
		return result.value, result.err
	case <-ctx.Done():
		cancel()
		// Cancellation can reject the hook before the native promise
		// settles. Keep its callbacks until settlement, even when kill
		// finishes asynchronously.
		go func() { <-done; release() }()
		return js.Undefined(), ctx.Err()
	}
}

func spawner(directory, binary string) invoker {
	return func(ctx context.Context, input magusCall) (processOutput, error) {
		return invoke(ctx, directory, binary, input)
	}
}

func invoke(ctx context.Context, directory, binary string, input magusCall) (processOutput, error) {
	var capture processOutput
	_, err := javascript(func() (any, error) {
		var err error
		capture, err = runProcess(ctx, directory, binary, input)
		return nil, err
	})
	return capture, spawnError(binary, err)
}

func bunRuntime() (js.Value, error) {
	bun := js.Global().Get("Bun")
	if bun.IsUndefined() || bun.IsNull() || bun.Get("spawn").Type() != js.TypeFunction {
		return js.Undefined(), errors.New("harness plugin: Bun.spawn is unavailable")
	}
	return bun, nil
}

func runProcess(ctx context.Context, directory, binary string, input magusCall) (processOutput, error) {
	if err := ctx.Err(); err != nil {
		return processOutput{}, err
	}
	bun, err := bunRuntime()
	if err != nil {
		return processOutput{}, err
	}
	argv := make([]any, 0, len(input.Args)+1)
	argv = append(argv, binary)
	for _, arg := range input.Args {
		argv = append(argv, arg)
	}
	stdin := js.Global().Get("TextEncoder").New().Call("encode", input.Input)
	return waitProcess(ctx, bun.Call("spawn", argv, map[string]any{"cwd": directory, "stdin": stdin, "stdout": "pipe", "stderr": "pipe"}))
}

// waitProcess collects the process's output, killing it on any way out but
// completion.
func waitProcess(ctx context.Context, proc js.Value) (processOutput, error) {
	kill := func() { _, _ = javascript(func() (any, error) { proc.Call("kill"); return nil, nil }) }
	completed := false
	defer func() {
		if !completed {
			kill()
		}
	}()
	response := js.Global().Get("Response")
	streams := []any{response.New(proc.Get("stdout")).Call("text"), response.New(proc.Get("stderr")).Call("text"), proc.Get("exited")}
	result, err := await(ctx, js.Global().Get("Promise").Call("all", streams), kill)
	if err != nil {
		return processOutput{}, err
	}
	completed = true
	return processOutput{Stdout: result.Index(0).String(), Stderr: result.Index(1).String()}, nil
}

func createArgs(args []js.Value) (string, string, time.Duration, error) {
	if len(args) != 3 {
		return "", "", 0, errors.New("harness plugin: create requires directory, binary, timeout milliseconds")
	}
	if args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || args[2].Type() != js.TypeNumber {
		return "", "", 0, errors.New("harness plugin: invalid create arguments")
	}
	timeout, err := parseTimeout(args[0].String(), args[1].String(), args[2].Float())
	return args[0].String(), args[1].String(), timeout, err
}

func create(_ js.Value, args []js.Value) any {
	directory, binary, timeout, err := createArgs(args)
	if err != nil {
		return rejected(err)
	}
	return promise(func() (any, error) {
		runtime, err := openRuntime(context.Background(), pluginSource, spawner(directory, binary))
		if err != nil {
			return nil, err
		}
		return runtimeHandle(runtime, timeout), nil
	})
}

func runtimeHandle(runtime *buzzRuntime, timeout time.Duration) map[string]any {
	var dispatch, closeRuntime js.Func
	dispatch = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 || args[0].Type() != js.TypeString {
			return rejected(errors.New("harness plugin: dispatch requires one event JSON string"))
		}
		data := args[0].String()
		return promise(func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			return dispatchJSON(ctx, runtime, data)
		})
	})
	closeRuntime = js.FuncOf(func(_ js.Value, _ []js.Value) any {
		return promise(func() (any, error) {
			err := runtime.Close()
			dispatch.Release()
			closeRuntime.Release()
			return nil, err
		})
	})
	return map[string]any{"version": abiVersion, "dispatch": dispatch, "close": closeRuntime}
}

func createPluginHook(_ js.Value, args []js.Value) any {
	if len(args) != 1 {
		return rejected(errors.New("harness plugin: createPlugin requires the harness context"))
	}
	input := args[0]
	return promise(func() (any, error) { return createPlugin(input) })
}
