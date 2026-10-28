package ffi

import (
	"fmt"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// Object decodes arg n with decode, the generated strict decoder of a boundary type.
// An absent argument decodes as null, which every decoder refuses by name.
func Object[T any](member, name string, args []vm.Value, n int, decode func(vm.Value) (T, error)) (T, error) {
	v, err := decode(arg(args, n))
	if err != nil {
		return v, fmt.Errorf("%s: %s: %w", member, name, err)
	}
	return v, nil
}

// Objects decodes arg n as a list, each item with decode.
func Objects[T any](member, name string, args []vm.Value, n int, decode func(vm.Value) (T, error)) ([]T, error) {
	v := arg(args, n)
	if !v.IsList() {
		return nil, fmt.Errorf("%s: %s: want a list, got %s", member, name, v.Kind())
	}
	items := v.ListItems()
	out := make([]T, len(items))
	for i, item := range items {
		x, err := decode(item)
		if err != nil {
			return nil, fmt.Errorf("%s: %s[%d]: %w", member, name, i, err)
		}
		out[i] = x
	}
	return out, nil
}

// arg is arg n, or null when the caller passed fewer.
func arg(args []vm.Value, n int) vm.Value {
	if n >= len(args) {
		return vm.Null
	}
	return args[n]
}

// Present reports whether arg n was passed and is not null: the test an optional
// object argument's trampoline makes before decoding it.
func Present(args []vm.Value, n int) bool { return !arg(args, n).IsNull() }
