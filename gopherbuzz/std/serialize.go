package std

import (
	"context"
	"encoding/json"
	"fmt"

	buzz "github.com/egladman/gopherbuzz"
)

// serializeModule builds the "serialize" module matching Buzz's serialize reference:
// https://buzz-lang.dev/0.5.0/reference/std/serialize.html
func serializeModule() buzz.Value {
	m := mod()

	// Boxed constructor map: Boxed.init(data)
	boxedDef := mod()
	boxedDef.MapSet("init", fn("Boxed.init", boxedInit))
	m.MapSet("Boxed", boxedDef)

	m.MapSet("serialize", fn("serialize.serialize", serializeSerialize))
	m.MapSet("jsonEncode", fn("serialize.jsonEncode", serializeJSONEncode))
	m.MapSet("jsonDecode", fn("serialize.jsonDecode", serializeJSONDecode))
	return m
}

// boxedInit wraps any Buzz value in a Boxed instance.
// Buzz signature: static fun init(any data) > Boxed !> CircularReference, NotSerializable
func boxedInit(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("Boxed.init: requires 1 argument")
	}
	return makeBoxed(args[0]), nil
}

// boxedRawKey is the private map key under which makeBoxed stores the
// underlying raw value so jsonEncode can extract it without serializing
// the native Go method values that also live in the map.
const boxedRawKey = "\x00boxed"

// makeBoxed wraps a buzz Value in a Boxed map with typed accessor methods.
func makeBoxed(v buzz.Value) buzz.Value {
	m := mod()
	m.MapSet(boxedRawKey, v)

	m.MapSet("q", fn("Boxed.q", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		// Path segments come either as upstream's single list (q([any], with str
		// keys for maps and int indices for lists) or the legacy variadic-string
		// form (q("a", "b")). Accept both so the same source runs on both runtimes.
		path := args
		if len(args) == 1 && args[0].IsList() {
			path = args[0].ListItems()
		}
		cur := v
		for _, seg := range path {
			switch {
			case cur.IsMap() && seg.IsStr():
				got, ok := cur.MapGet(seg.AsString())
				if !ok {
					return makeBoxed(buzz.Null), nil
				}
				cur = got
			case cur.IsList() && seg.IsInt():
				items := cur.ListItems()
				idx := int(seg.AsInt())
				if idx < 0 || idx >= len(items) {
					return makeBoxed(buzz.Null), nil
				}
				cur = items[idx]
			default:
				return makeBoxed(buzz.Null), nil
			}
		}
		return makeBoxed(cur), nil
	}))
	m.MapSet("string", fn("Boxed.string", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsStr() {
			return v, nil
		}
		return buzz.Null, nil
	}))
	m.MapSet("boolean", fn("Boxed.boolean", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsBool() {
			return v, nil
		}
		return buzz.Null, nil
	}))
	m.MapSet("integer", fn("Boxed.integer", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsInt() {
			return v, nil
		}
		return buzz.Null, nil
	}))
	floating := fn("Boxed.floating", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsFloat() {
			return v, nil
		}
		return buzz.Null, nil
	})
	m.MapSet("floating", floating)
	m.MapSet("float", floating) // upstream serialize names it float()
	m.MapSet("map", fn("Boxed.map", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if !v.IsMap() {
			return buzz.Null, nil
		}
		// Return a {str: Boxed} map.
		out := buzz.NewMap()
		for _, k := range v.MapKeys() {
			kv, _ := v.MapGet(k)
			out.MapSet(k, makeBoxed(kv))
		}
		return out, nil
	}))
	m.MapSet("list", fn("Boxed.list", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if !v.IsList() {
			return buzz.Null, nil
		}
		items := v.ListItems()
		out := make([]buzz.Value, len(items))
		for i, it := range items {
			out[i] = makeBoxed(it)
		}
		return buzz.ListValue(out), nil
	}))
	m.MapSet("stringValue", fn("Boxed.stringValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsStr() {
			return v, nil
		}
		return buzz.StrValue(""), nil
	}))
	m.MapSet("booleanValue", fn("Boxed.booleanValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsBool() {
			return v, nil
		}
		return buzz.False, nil
	}))
	m.MapSet("integerValue", fn("Boxed.integerValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsInt() {
			return v, nil
		}
		return buzz.IntValue(0), nil
	}))
	floatingValue := fn("Boxed.floatingValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if v.IsFloat() {
			return v, nil
		}
		return buzz.FloatValue(0), nil
	})
	m.MapSet("floatingValue", floatingValue)
	m.MapSet("floatValue", floatingValue) // upstream serialize names it floatValue()
	m.MapSet("mapValue", fn("Boxed.mapValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if !v.IsMap() {
			return buzz.NewMap(), nil
		}
		out := buzz.NewMap()
		for _, k := range v.MapKeys() {
			kv, _ := v.MapGet(k)
			out.MapSet(k, makeBoxed(kv))
		}
		return out, nil
	}))
	m.MapSet("listValue", fn("Boxed.listValue", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		if !v.IsList() {
			return buzz.ListValue(nil), nil
		}
		items := v.ListItems()
		out := make([]buzz.Value, len(items))
		for i, it := range items {
			out[i] = makeBoxed(it)
		}
		return buzz.ListValue(out), nil
	}))
	return m
}

// serializeSerialize converts a Buzz value to a serializable form (same value
// for primitives; maps and lists pass through; errors on circular structures).
func serializeSerialize(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("serialize.serialize: requires 1 argument")
	}
	return args[0], nil // primitives are already serializable
}

// serializeJSONEncode encodes a Boxed value to a JSON string.
func serializeJSONEncode(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("serialize.jsonEncode: requires a Boxed argument")
	}
	src := args[0]
	// A Boxed value (from makeBoxed) stores its raw data under boxedRawKey.
	// Extract it so we serialize the data, not the Go method wrappers.
	if raw, ok := src.MapGet(boxedRawKey); ok {
		src = raw
	}
	goVal, err := buzzToGo(src)
	if err != nil {
		return buzz.Null, fmt.Errorf("serialize.jsonEncode: %w", err)
	}
	b, err := json.Marshal(goVal)
	if err != nil {
		return buzz.Null, fmt.Errorf("serialize.jsonEncode: %w", err)
	}
	return buzz.StrValue(string(b)), nil
}

// serializeJSONDecode decodes a JSON string to a Boxed value.
func serializeJSONDecode(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("serialize.jsonDecode: requires a str argument")
	}
	var raw any
	if err := json.Unmarshal([]byte(args[0].AsString()), &raw); err != nil {
		return buzz.Null, fmt.Errorf("serialize.jsonDecode: %w", err)
	}
	return makeBoxed(goToBoxedBuzz(raw)), nil
}

// buzzToGo converts a Buzz value to a Go-native value suitable for JSON marshaling.
func buzzToGo(v buzz.Value) (any, error) {
	switch {
	case v.IsNull():
		return nil, nil
	case v.IsBool():
		return v.AsBool(), nil
	case v.IsInt():
		return v.AsInt(), nil
	case v.IsFloat():
		return v.AsFloat(), nil
	case v.IsStr():
		return v.AsString(), nil
	case v.IsList():
		items := v.ListItems()
		out := make([]any, len(items))
		for i, it := range items {
			gv, err := buzzToGo(it)
			if err != nil {
				return nil, err
			}
			out[i] = gv
		}
		return out, nil
	case v.IsMap():
		out := make(map[string]any)
		for _, k := range v.MapKeys() {
			kv, _ := v.MapGet(k)
			gv, err := buzzToGo(kv)
			if err != nil {
				return nil, err
			}
			out[k] = gv
		}
		return out, nil
	default:
		return nil, fmt.Errorf("value of kind %q is not serializable", v.Kind())
	}
}

// goToBoxedBuzz converts a Go value (as returned by json.Unmarshal) to a Buzz value.
func goToBoxedBuzz(v any) buzz.Value {
	if v == nil {
		return buzz.Null
	}
	switch x := v.(type) {
	case bool:
		return buzz.BoolValue(x)
	case float64:
		// json.Unmarshal always uses float64 for numbers.
		if x == float64(int64(x)) {
			return buzz.IntValue(int64(x))
		}
		return buzz.FloatValue(x)
	case string:
		return buzz.StrValue(x)
	case []any:
		items := make([]buzz.Value, len(x))
		for i, it := range x {
			items[i] = goToBoxedBuzz(it)
		}
		return buzz.ListValue(items)
	case map[string]any:
		m := buzz.NewMap()
		for k, val := range x {
			m.MapSet(k, goToBoxedBuzz(val))
		}
		return m
	default:
		return buzz.StrValue(fmt.Sprintf("%v", v))
	}
}
