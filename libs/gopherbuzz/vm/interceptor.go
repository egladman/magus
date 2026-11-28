package vm

import "context"

// CallInterceptor sees every call into a Go native built with DirectValue or
// DirectClosure; builtin methods such as list.append never reach it. InterceptCall
// decides whether to run call: a recorder runs it, a player may answer
// without it. Returning call's error verbatim keeps Suspend unchanged.
//
// args is the VM's live operand stack: copy what must outlive the call, never
// the slice.
type CallInterceptor interface {
	InterceptCall(ctx context.Context, name string, args []Value, call Callable) (Value, error)
}

type callInterceptorKey struct{}

// WithCallInterceptor returns ctx carrying obs. A VM built on ctx routes its
// calls through obs, and so does every fiber or callback VM built under
// it. Embedders install one with the Session option of the same name.
func WithCallInterceptor(ctx context.Context, obs CallInterceptor) context.Context {
	return context.WithValue(ctx, callInterceptorKey{}, obs)
}

// interceptCall is the one path from a call site's arguments into a direct
// callable. With no observer it costs one nil check over d.Fn.
func (vm *VM) interceptCall(d *directObj, args []Value) (Value, error) {
	if vm.interceptor == nil || !d.intercept {
		return d.Fn(vm.ctx, args)
	}
	return vm.interceptor.InterceptCall(vm.ctx, d.Name, args, d.Fn)
}
