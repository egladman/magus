package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// encode writes v as JSON that keeps every kind apart: {"float": "<text>"},
// {"map": [[key, value]]} and {"object": T, "fields": [[name, value]]}. unencodable
// names the first kind it cannot hold and where, as in `fun at $[1]["cb"]`.
func encode(v vm.Value) (result string, unencodable string) {
	tree, why := toTree(v, "$", nil)
	if why != "" {
		return "", why
	}
	b, err := codec.Marshal(tree)
	if err != nil {
		return "", fmt.Sprintf("%s: %v", v.Kind(), err)
	}
	return string(b), ""
}

func toTree(v vm.Value, where string, path []vm.Value) (any, string) {
	switch {
	case v.IsNull():
		return nil, ""
	case v.IsBool():
		return v.AsBool(), ""
	case v.IsInt():
		return json.Number(strconv.FormatInt(v.AsInt(), 10)), ""
	case v.IsFloat():
		return map[string]any{"float": strconv.FormatFloat(v.AsFloat(), 'g', -1, 64)}, ""
	case v.IsString():
		return v.AsString(), ""
	}
	if !v.IsList() && !v.IsMap() && !v.IsObject() {
		return nil, fmt.Sprintf("%s at %s", v.Kind(), where)
	}
	if v.HasNativePayload() {
		return nil, fmt.Sprintf("%s with a native payload at %s", v.ObjectTypeName(), where)
	}
	for _, p := range path {
		if p.Equal(v) {
			return nil, "a cycle at " + where
		}
	}
	path = append(path, v)
	switch {
	case v.IsList():
		items := v.ListItems()
		out := make([]any, len(items))
		for i, it := range items {
			x, why := toTree(it, fmt.Sprintf("%s[%d]", where, i), path)
			if why != "" {
				return nil, why
			}
			out[i] = x
		}
		return out, ""
	case v.IsMap():
		pairs, why := mapPairs(v, where, path)
		if why != "" {
			return nil, why
		}
		return map[string]any{"map": pairs}, ""
	}
	fields, _ := v.MapView()
	pairs, why := instanceFields(fields, where, path)
	if why != "" {
		return nil, why
	}
	return map[string]any{"object": v.ObjectTypeName(), "fields": pairs}, ""
}

func mapPairs(m vm.Value, where string, path []vm.Value) ([]any, string) {
	keys, vals := m.MapKeyValues(), m.MapValues()
	pairs := make([]any, len(keys))
	for i, k := range keys {
		if !k.IsString() {
			return nil, fmt.Sprintf("map with a %s key at %s", k.Kind(), where)
		}
		x, why := toTree(vals[i], fmt.Sprintf("%s[%s]", where, strconv.Quote(k.AsString())), path)
		if why != "" {
			return nil, why
		}
		pairs[i] = []any{k.AsString(), x}
	}
	return pairs, ""
}

func instanceFields(m vm.Value, where string, path []vm.Value) ([]any, string) {
	names, vals := m.MapKeys(), m.MapValues()
	pairs := make([]any, len(names))
	for i, name := range names {
		x, why := toTree(vals[i], fmt.Sprintf("%s.%s", where, name), path)
		if why != "" {
			return nil, why
		}
		pairs[i] = []any{name, x}
	}
	return pairs, ""
}

// decodeWith reads a result encode wrote. A list or map comes back immutable. An instance
// is rebuilt from the type defOf names. It is refused, naming the type, when defOf has
// none or the fields differ from the declared ones.
func decodeWith(result string, defOf func(typeName string) (vm.Value, bool)) (vm.Value, error) {
	var tree any
	if err := codec.UnmarshalLossless([]byte(result), &tree); err != nil {
		return vm.Null, fmt.Errorf("replay: decode result: %w", err)
	}
	return fromTree(tree, defOf)
}

func fromTree(x any, defOf func(string) (vm.Value, bool)) (vm.Value, error) {
	switch t := x.(type) {
	case nil:
		return vm.Null, nil
	case bool:
		return vm.BoolValue(t), nil
	case string:
		return vm.StringValue(t), nil
	case json.Number:
		n, err := strconv.ParseInt(string(t), 10, 64)
		if err != nil {
			return vm.Null, fmt.Errorf("replay: decode result: %s is not an int, and a float is tagged", t)
		}
		return vm.IntValue(n), nil
	case []any:
		items := make([]vm.Value, len(t))
		for i, it := range t {
			v, err := fromTree(it, defOf)
			if err != nil {
				return vm.Null, err
			}
			items[i] = v
		}
		return vm.ListValue(items), nil
	case map[string]any:
		return fromTagged(t, defOf)
	}
	return vm.Null, fmt.Errorf("replay: decode result: unexpected %T", x)
}

func fromTagged(t map[string]any, defOf func(string) (vm.Value, bool)) (vm.Value, error) {
	if text, ok := t["float"].(string); ok && len(t) == 1 {
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return vm.Null, fmt.Errorf("replay: decode result: float %q: %w", text, err)
		}
		return vm.FloatValue(f), nil
	}
	if pairs, ok := t["map"].([]any); ok && len(t) == 1 {
		m := vm.NewMap()
		for _, p := range pairs {
			k, raw, err := splitPair(p)
			if err != nil {
				return vm.Null, err
			}
			v, err := fromTree(raw, defOf)
			if err != nil {
				return vm.Null, err
			}
			m.MapSet(k, v)
		}
		return m, nil
	}
	if name, ok := t["object"].(string); ok {
		return fromInstance(name, t["fields"], defOf)
	}
	return vm.Null, fmt.Errorf("replay: decode result: an object tagged with none of float, map or object")
}

func fromInstance(name string, rawFields any, defOf func(string) (vm.Value, bool)) (vm.Value, error) {
	var def vm.Value
	found := false
	if defOf != nil {
		def, found = defOf(name)
	}
	if !found {
		return vm.Null, fmt.Errorf("replay: decode result: an instance of %s needs its object definition, which the caller does not hold", name)
	}
	pairs, ok := rawFields.([]any)
	if !ok {
		return vm.Null, fmt.Errorf("replay: decode result: the fields of %s are not a list", name)
	}
	declared := def.ObjectFieldNames()
	if len(declared) != len(pairs) {
		return vm.Null, fmt.Errorf("replay: decode result: %s was recorded with %d fields and declares %d", name, len(pairs), len(declared))
	}
	fields := make([]vm.Value, len(pairs))
	for i, p := range pairs {
		field, raw, err := splitPair(p)
		if err != nil {
			return vm.Null, err
		}
		if field != declared[i] {
			return vm.Null, fmt.Errorf("replay: decode result: %s was recorded with field %q at %d and declares %q", name, field, i, declared[i])
		}
		if fields[i], err = fromTree(raw, defOf); err != nil {
			return vm.Null, err
		}
	}
	inst, err := def.NewInstance(fields)
	if err != nil {
		return vm.Null, fmt.Errorf("replay: decode result: %s: %w", name, err)
	}
	return inst, nil
}

func splitPair(p any) (string, any, error) {
	kv, ok := p.([]any)
	if !ok || len(kv) != 2 {
		return "", nil, fmt.Errorf("replay: decode result: a map entry or field is not a [key, value] pair")
	}
	k, ok := kv[0].(string)
	if !ok {
		return "", nil, fmt.Errorf("replay: decode result: a map key or field name is not a str")
	}
	return k, kv[1], nil
}

// argsDigest is the sha256 hex over each argument's encoding. An unencodable argument
// digests as its kind, so two different callbacks digest the same.
func argsDigest(args []vm.Value) string {
	var b []byte
	for _, a := range args {
		result, why := encode(a)
		if why != "" {
			result = "~" + a.Kind()
		}
		b = append(append(b, result...), 0)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
