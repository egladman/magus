//go:build !wasm

package std

import (
	"fmt"
	"reflect"
)

// validateModule checks each Method's Impl signature against its declared
// Args and Returns. Mismatches are programmer errors and panic at init. It (and
// its helpers) reflect over func types, which TinyGo's wasm reflect does not
// support, so the wasm build gets the no-op in validate_wasm.go instead; see the
// note in module.go.
func validateModule(m Module) error {
	for _, f := range m.Fields {
		if err := validateField(f); err != nil {
			return fmt.Errorf("field %q: %w", f.Name, err)
		}
	}
	for _, meth := range m.Methods {
		if err := validateMethod(meth); err != nil {
			return fmt.Errorf("method %q: %w", meth.Name, err)
		}
	}
	return nil
}

func validateField(f Field) error {
	if f.Resolver == nil {
		return fmt.Errorf("field Resolver must not be nil")
	}
	rt := reflect.TypeOf(f.Resolver)
	if rt.Kind() != reflect.Func {
		return fmt.Errorf("field Resolver must be a function, got %s", rt.Kind())
	}
	// Accept either () (T, error) or (context.Context) (T, error).
	switch rt.NumIn() {
	case 0:
	case 1:
		if rt.In(0).String() != "context.Context" {
			return fmt.Errorf("field Resolver single arg must be context.Context, got %s", rt.In(0))
		}
	default:
		return fmt.Errorf("field Resolver must take 0 or 1 args (ctx), got %d", rt.NumIn())
	}
	if rt.NumOut() != 2 {
		return fmt.Errorf("field Resolver must return (T, error), got %d returns", rt.NumOut())
	}
	if rt.Out(1).String() != "error" {
		return fmt.Errorf("field Resolver second return must be error, got %s", rt.Out(1))
	}
	return nil
}

func validateMethod(meth Method) error {
	if meth.Impl == nil {
		return fmt.Errorf("method Impl must not be nil")
	}
	rv := reflect.ValueOf(meth.Impl)
	rt := rv.Type()
	if rt.Kind() != reflect.Func {
		return fmt.Errorf("method Impl must be a function, got %s", rt.Kind())
	}

	// First param must be context.Context.
	if rt.NumIn() < 1 {
		return fmt.Errorf("method Impl must take context.Context as first arg")
	}
	if rt.In(0).String() != "context.Context" {
		return fmt.Errorf("method Impl first arg must be context.Context, got %s", rt.In(0))
	}

	// Compute expected number of declared args.
	wantArgs := 1 + len(meth.Args) // +1 for ctx
	hasVariadic := len(meth.Args) > 0 && meth.Args[len(meth.Args)-1].Variadic
	if hasVariadic {
		if !rt.IsVariadic() {
			return fmt.Errorf("declaration says variadic but Impl is not variadic")
		}
	} else if rt.IsVariadic() {
		return fmt.Errorf("method Impl is variadic but declaration has no variadic arg")
	}
	if rt.NumIn() != wantArgs {
		return fmt.Errorf("method Impl takes %d args, declaration has %d (incl. ctx)", rt.NumIn(), wantArgs)
	}

	// Last return must be error.
	if rt.NumOut() < 1 {
		return fmt.Errorf("method Impl must return error as last value")
	}
	if rt.Out(rt.NumOut()-1).String() != "error" {
		return fmt.Errorf("method Impl last return must be error, got %s", rt.Out(rt.NumOut()-1))
	}
	if rt.NumOut()-1 != len(meth.Returns) {
		return fmt.Errorf("method Impl has %d non-error returns, declaration has %d", rt.NumOut()-1, len(meth.Returns))
	}
	return nil
}
