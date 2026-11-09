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
	for _, ns := range m.Namespaces {
		if len(ns.Methods) == 0 {
			return fmt.Errorf("namespace %q declares no methods, it would render as an empty object", ns.Name)
		}
		for _, meth := range ns.Methods {
			// A namespace exists precisely because the RUNTIME assembles it, so there is
			// no Impl to reflect over or generate a trampoline from. Requiring Extern
			// here keeps that from being an accident: a namespace method carrying an
			// Impl would generate nothing and silently never be bound.
			if meth.Extern == nil {
				return fmt.Errorf("namespace %q method %q must be Extern, the runtime binds it", ns.Name, meth.Name)
			}
			if err := validateMethod(meth); err != nil {
				return fmt.Errorf("namespace %q method %q: %w", ns.Name, meth.Name, err)
			}
		}
	}
	return validateMCPTools(m)
}

// validateMCPTools checks the module's MCP tools: unique tool names, unique
// parameter names within a tool, and a schema-representable type on every parameter.
func validateMCPTools(m Module) error {
	seen := map[string]bool{}
	for _, tool := range m.MCPTools {
		if tool.Name == "" {
			return fmt.Errorf("mcp tool: empty Name")
		}
		if seen[tool.Name] {
			return fmt.Errorf("mcp tool %q: declared twice", tool.Name)
		}
		seen[tool.Name] = true
		if tool.Doc == "" {
			return fmt.Errorf("mcp tool %q: empty Doc, the description is what an agent picks the tool by", tool.Name)
		}
		params := map[string]bool{}
		for _, p := range tool.Params {
			if p.Name == "" {
				return fmt.Errorf("mcp tool %q: empty param Name", tool.Name)
			}
			if params[p.Name] {
				return fmt.Errorf("mcp tool %q: param %q declared twice", tool.Name, p.Name)
			}
			params[p.Name] = true
			switch p.Type {
			case TypeString, TypeInt, TypeFloat, TypeBool, TypeStringSlice, TypeAnyMap:
			default:
				return fmt.Errorf("mcp tool %q: param %q has type %s, which has no supported JSON schema shape", tool.Name, p.Name, p.Type.GoType())
			}
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
	if meth.Extern != nil && meth.Impl != nil {
		return fmt.Errorf("method is Extern but carries an Impl, it is one or the other")
	}
	rt := meth.Signature()
	if rt == nil {
		return fmt.Errorf("method Impl must not be nil (set Extern if it is bound at run time)")
	}
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
	for i, a := range meth.Args {
		if err := validateCallback(rt.In(i + 1)); err != nil {
			return fmt.Errorf("arg %s: %w", a.Name, err)
		}
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

// validateCallback checks a func-typed parameter has the one shape a Buzz
// function value adapts to: context.Context first, then error last after at
// most one value. Any other parameter passes.
func validateCallback(param reflect.Type) error {
	if param.Kind() != reflect.Func {
		return nil
	}
	if param.IsVariadic() {
		return fmt.Errorf("callback %s is variadic, and a Buzz function takes no variadic parameter", param)
	}
	if param.NumIn() < 1 || param.In(0).String() != "context.Context" {
		return fmt.Errorf("callback %s must take context.Context first", param)
	}
	if param.NumOut() < 1 || param.NumOut() > 2 || param.Out(param.NumOut()-1).String() != "error" {
		return fmt.Errorf("callback %s must return error, or one value and error", param)
	}
	return nil
}
