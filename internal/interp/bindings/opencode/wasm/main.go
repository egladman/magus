//go:build js && wasm

package main

import (
	"context"
	"errors"
	"fmt"
	"syscall/js"
	"time"

	"github.com/egladman/magus/internal/interp/bindings/opencode"
	"github.com/egladman/magus/internal/json"
)

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

// syscall/js surfaces thrown host exceptions as panics. They must reject the
// corresponding hook instead of terminating every plugin instance in the VM.
func javascript(work func() (any, error)) (value any, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("opencode: JavaScript host: %v", failure)
		}
	}()
	return work()
}

type settled struct {
	value js.Value
	err   error
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
		message := "native invocation rejected"
		if len(args) > 0 {
			_, _ = javascript(func() (any, error) { message = js.Global().Get("String").Invoke(args[0]).String(); return nil, nil })
		}
		done <- settled{err: fmt.Errorf("opencode: %s", message)}
		return nil
	})
	_, err := javascript(func() (any, error) { pending.Call("then", resolve, reject); return nil, nil })
	if err != nil {
		resolve.Release()
		reject.Release()
		return js.Undefined(), err
	}
	select {
	case result := <-done:
		resolve.Release()
		reject.Release()
		return result.value, result.err
	case <-ctx.Done():
		cancel()
		// Cancellation can reject the hook before the native promise settles. Retain
		// its callbacks until settlement, including when kill finishes asynchronously.
		go func() { <-done; resolve.Release(); reject.Release() }()
		return js.Undefined(), ctx.Err()
	}
}

func invoke(ctx context.Context, directory, binary string, input opencode.Invocation) (opencode.Capture, error) {
	var capture opencode.Capture
	_, err := javascript(func() (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bun := js.Global().Get("Bun")
		if bun.IsUndefined() || bun.IsNull() || bun.Get("spawn").Type() != js.TypeFunction {
			return nil, errors.New("opencode: Bun.spawn is unavailable")
		}
		argv := make([]any, 0, len(input.Args)+1)
		argv = append(argv, binary)
		for _, arg := range input.Args {
			argv = append(argv, arg)
		}
		stdin := js.Global().Get("TextEncoder").New().Call("encode", input.Input)
		proc := bun.Call("spawn", argv, map[string]any{"cwd": directory, "stdin": stdin, "stdout": "pipe", "stderr": "pipe"})
		kill := func() { _, _ = javascript(func() (any, error) { proc.Call("kill"); return nil, nil }) }
		completed := false
		defer func() {
			if !completed {
				kill()
			}
		}()
		response := js.Global().Get("Response")
		streams := []any{response.New(proc.Get("stdout")).Call("text"), response.New(proc.Get("stderr")).Call("text"), proc.Get("exited")}
		pending := js.Global().Get("Promise").Call("all", streams)
		result, err := await(ctx, pending, kill)
		if err != nil {
			kill()
			return nil, err
		}
		capture = opencode.Capture{Stdout: result.Index(0).String(), Stderr: result.Index(1).String()}
		completed = true
		return nil, nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		return capture, fmt.Errorf("%s could not be run", binary)
	}
	return capture, err
}

func main() {
	create := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 3 {
			return promise(func() (any, error) {
				return nil, errors.New("opencode: create requires directory, binary, timeout milliseconds")
			})
		}
		if args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || args[2].Type() != js.TypeNumber {
			return promise(func() (any, error) { return nil, errors.New("opencode: invalid create arguments") })
		}
		directory, binary, milliseconds := args[0].String(), args[1].String(), args[2].Float()
		return promise(func() (any, error) {
			if directory == "" || binary == "" || milliseconds < 1 || milliseconds > 2147483647 || milliseconds != float64(int64(milliseconds)) {
				return nil, errors.New("opencode: invalid directory, binary or timeout")
			}
			timeout := time.Duration(milliseconds) * time.Millisecond
			runtime, err := opencode.Open(context.Background(), opencode.Plugin, func(ctx context.Context, input opencode.Invocation) (opencode.Capture, error) {
				return invoke(ctx, directory, binary, input)
			})
			if err != nil {
				return nil, err
			}
			var dispatch, closeRuntime js.Func
			dispatch = js.FuncOf(func(_ js.Value, args []js.Value) any {
				if len(args) != 1 || args[0].Type() != js.TypeString {
					return promise(func() (any, error) { return nil, errors.New("opencode: dispatch requires one event JSON string") })
				}
				data := args[0].String()
				return promise(func() (any, error) {
					ctx, cancel := context.WithTimeout(context.Background(), timeout)
					defer cancel()
					var event opencode.Event
					if err := json.Unmarshal([]byte(data), &event); err != nil {
						return nil, fmt.Errorf("opencode: event: %w", err)
					}
					result, err := runtime.Dispatch(ctx, event)
					if err != nil {
						return nil, err
					}
					encoded, err := json.Marshal(result)
					return string(encoded), err
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
			return map[string]any{"version": opencode.Version, "dispatch": dispatch, "close": closeRuntime}, nil
		})
	})
	pluginFactory := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) != 1 {
			return promise(func() (any, error) { return nil, errors.New("opencode: createPlugin requires the host context") })
		}
		input := args[0]
		return promise(func() (any, error) { return createPlugin(input) })
	})
	js.Global().Set("__magusOpenCodeWasmV1", map[string]any{"version": opencode.Version, "create": create, "createPlugin": pluginFactory})
	select {}
}
