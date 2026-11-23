package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

var boxedPayload vm.NativePayloadKey

func serializeCoreModule() vm.Value {
	m := mod()
	m.MapSet("wrap", function("serializecore.wrap", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 2 {
			return vm.Null, fmt.Errorf("serializecore.wrap: expected a type and a value")
		}
		return wrapBoxed(args[0], args[1])
	}))
	m.MapSet("raw", function("serializecore.raw", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 {
			return vm.Null, fmt.Errorf("serializecore.raw: expected a Boxed value")
		}
		return rawBoxed(args[0])
	}))
	m.MapSet("map", function("serializecore.map", boxedMap))
	m.MapSet("list", function("serializecore.list", boxedList))
	m.MapSet("serialize", function("serializecore.serialize", serializeSerialize))
	m.MapSet("jsonEncode", function("serializecore.jsonEncode", serializeJSONEncode))
	m.MapSet("decode", function("serializecore.decode", decodeBoxedJSON))
	return m
}

func rawBoxed(box vm.Value) (vm.Value, error) {
	raw, ok := box.NativePayload(&boxedPayload)
	if !ok {
		return vm.Null, fmt.Errorf("serialize: expected a Boxed instance created by init or jsonDecode")
	}
	return raw, nil
}

func wrapBoxed(definition, raw vm.Value) (vm.Value, error) {
	query := vm.DirectClosure("Boxed.q", func(_ context.Context, path []vm.Value) (vm.Value, error) {
		if len(path) == 1 && path[0].IsList() {
			path = path[0].ListItems()
		}
		value := raw
		for _, segment := range path {
			switch {
			case value.IsMap() && segment.IsString():
				next, ok := value.MapGet(segment.AsString())
				if !ok {
					return wrapBoxed(definition, vm.Null)
				}
				value = next
			case value.IsList() && segment.IsInt():
				items, index := value.ListItems(), segment.AsInt()
				if index < 0 || index >= int64(len(items)) {
					return wrapBoxed(definition, vm.Null)
				}
				value = items[index]
			default:
				return wrapBoxed(definition, vm.Null)
			}
		}
		return wrapBoxed(definition, value)
	}, definition, raw)
	return definition.NewNativeInstance(&boxedPayload, []vm.Value{query}, raw)
}

func boxedMap(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) != 2 {
		return vm.Null, fmt.Errorf("serializecore.map: expected a type and a box")
	}
	raw, err := rawBoxed(args[1])
	if err != nil || !raw.IsMap() {
		return vm.Null, err
	}
	result := vm.NewMap()
	for _, key := range raw.MapKeys() {
		value, _ := raw.MapGet(key)
		boxed, err := wrapBoxed(args[0], value)
		if err != nil {
			return vm.Null, err
		}
		result.MapSet(key, boxed)
	}
	return result, nil
}

func boxedList(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) != 2 {
		return vm.Null, fmt.Errorf("serializecore.list: expected a type and a box")
	}
	raw, err := rawBoxed(args[1])
	if err != nil || !raw.IsList() {
		return vm.Null, err
	}
	items := raw.ListItems()
	result := make([]vm.Value, len(items))
	for i, value := range items {
		result[i], err = wrapBoxed(args[0], value)
		if err != nil {
			return vm.Null, err
		}
	}
	return vm.ListValue(result), nil
}

func decodeBoxedJSON(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) != 1 || !args[0].IsString() {
		return vm.Null, fmt.Errorf("serializecore.decode: expected a JSON string")
	}
	value, err := decodeJSON([]byte(args[0].AsString()))
	message := vm.Null
	if err != nil {
		message = vm.StringValue("serialize.jsonDecode: " + err.Error())
	}
	result := vm.NewMap()
	result.MapSet("value", value)
	result.MapSet("error", message)
	return result, nil
}
