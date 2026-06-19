package vm

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// arith evaluates +, -, *, /, % with Buzz's numeric promotion rules.
// optimization: int+int fast path avoids float promotion for the hot case.
// measured: eliminates conditional float-check on every arithmetic op.
// trade-off: code slightly longer; branch predictor handles it well.
// assumes: tagInt and tagFloat are distinct (value.go invariant).
func arith(vm *VM, op OpCode, left, right Value) (Value, error) {
	// Fast path: int op int (most common in loops)
	if left.tag() == tagInt && right.tag() == tagInt {
		return intArith(op, int64(left.num()), int64(right.num()))
	}

	// String concatenation (OpAdd only)
	if op == OpAdd {
		if left.tag() == tagStr {
			return StrValue(vm.asStr(left).V + right.String()), nil
		}
		if left.tag() == tagList && right.tag() == tagList {
			leftList, rightList := vm.asList(left), vm.asList(right)
			merged := make([]Value, 0, len(leftList.Items)+len(rightList.Items))
			merged = append(merged, leftList.Items...)
			merged = append(merged, rightList.Items...)
			return ListValue(merged), nil
		}
	}

	// Float promotion
	leftFloat, leftOk := asNumeric(left)
	rightFloat, rightOk := asNumeric(right)
	if !leftOk || !rightOk {
		return Null, fmt.Errorf("buzz: cannot apply %q to %s and %s",
			arithSymbol(op), left.buzzKind(), right.buzzKind())
	}
	return floatArith(op, leftFloat, rightFloat)
}

func intArith(op OpCode, a, b int64) (Value, error) {
	switch op {
	case OpAdd:
		return IntValue(a + b), nil
	case OpSub:
		return IntValue(a - b), nil
	case OpMul:
		return IntValue(a * b), nil
	case OpDiv:
		if b == 0 {
			return Null, fmt.Errorf("buzz: integer division by zero")
		}
		return IntValue(a / b), nil
	case OpMod:
		if b == 0 {
			return Null, fmt.Errorf("buzz: integer modulo by zero")
		}
		return IntValue(a % b), nil
	default:
		return Null, fmt.Errorf("buzz: unknown arith opcode %d", op)
	}
}

func floatArith(op OpCode, a, b float64) (Value, error) {
	switch op {
	case OpAdd:
		return FloatValue(a + b), nil
	case OpSub:
		return FloatValue(a - b), nil
	case OpMul:
		return FloatValue(a * b), nil
	case OpDiv:
		if b == 0 {
			return Null, fmt.Errorf("buzz: division by zero")
		}
		return FloatValue(a / b), nil
	case OpMod:
		return Null, fmt.Errorf("buzz: %% not supported for float operands")
	default:
		return Null, fmt.Errorf("buzz: unknown arith opcode %d", op)
	}
}

// floatBinop is the float+float fast path shared by the fused superinstruction
// handlers (OpBinLL/OpBinLC). It mirrors what applyBinop would
// compute for two float operands, but inline, skipping arith's int/str probes,
// the two asNumeric type switches, and floatArith's opcode switch.
//
// ok==false means "not a float-handled op here, fall back to applyBinop": that
// is OpMod (no float modulo) and float division by zero, both of which must
// surface arith's exact error. The standalone OpAdd/OpSub/… handlers inline
// their own single-op float path (the op is statically known there); this helper
// exists only where the sub-opcode is a runtime value.
//
// optimization: collapses the float arithmetic/compare dispatch the fused ops
//
//	otherwise pay via applyBinop→arith→asNumeric×2→floatArith. The float kernels
//	(Mandelbrot, NBody) drive their inner-loop locals through the fused ops, so
//	every iteration hit that chain before this path.
//	measured: BenchmarkComparison/{Mandelbrot,NBody}/Warm/Gopherbuzz -14.8% /
//	  -7.5% ns/op (benchstat n=10, Go 1.25.10, amd64; Mandelbrot -22% B/op);
//	  see benchmarks/comparison and bench/floatfast.txt.
//	trade-off: a second tag-pair test per fused op on the int-operand path
//	  (one predicted-not-taken branch); int benches (LoopSum/Fib) unchanged.
//	assumes: caller has verified both operands are tagFloat.
func floatBinop(op OpCode, a, b float64) (Value, bool) {
	switch op {
	case OpAdd:
		return FloatValue(a + b), true
	case OpSub:
		return FloatValue(a - b), true
	case OpMul:
		return FloatValue(a * b), true
	case OpDiv:
		if b == 0 {
			return Null, false // let arith() raise buzz's float divide-by-zero error
		}
		return FloatValue(a / b), true
	case OpLess:
		return BoolValue(a < b), true
	case OpLessEqual:
		return BoolValue(a <= b), true
	case OpGreater:
		return BoolValue(a > b), true
	case OpGreaterEqual:
		return BoolValue(a >= b), true
	case OpEqual:
		return BoolValue(a == b), true
	case OpNotEqual:
		return BoolValue(a != b), true
	}
	return Null, false // OpMod (no float modulo) → applyBinop reports the error
}

// applyBinop dispatches a fused binary op (OpBinLC) to the same semantics
// the unfused opcode would run. It is the polymorphic fallback the fused handler
// uses once its inline int fast path misses; keeping it here means the fused op
// and the standalone OpAdd/OpLess/… handlers can never drift apart.
func applyBinop(vm *VM, op OpCode, left, right Value) (Value, error) {
	switch op {
	case OpAdd, OpSub, OpMul, OpDiv, OpMod:
		return arith(vm, op, left, right) // arith handles str/list concat for OpAdd
	case OpLess, OpLessEqual, OpGreater, OpGreaterEqual:
		return compare(vm, op, left, right)
	case OpEqual:
		return BoolValue(valuesEqual(left, right)), nil
	case OpNotEqual:
		return BoolValue(!valuesEqual(left, right)), nil
	default:
		return Null, fmt.Errorf("buzz: bad fused op %d", op)
	}
}

// compare evaluates <, >, <=, >= for numbers and strings.
func compare(vm *VM, op OpCode, left, right Value) (Value, error) {
	// Fast path: int vs int
	if left.tag() == tagInt && right.tag() == tagInt {
		a, b := int64(left.num()), int64(right.num())
		var result bool
		switch op {
		case OpLess:
			result = a < b
		case OpLessEqual:
			result = a <= b
		case OpGreater:
			result = a > b
		default: // OpGreaterEqual
			result = a >= b
		}
		return BoolValue(result), nil
	}

	// String comparison
	if left.tag() == tagStr && right.tag() == tagStr {
		order := strings.Compare(vm.asStr(left).V, vm.asStr(right).V)
		return BoolValue(cmpResult(op, order)), nil
	}

	// Float promotion
	leftFloat, leftOk := asNumeric(left)
	rightFloat, rightOk := asNumeric(right)
	if !leftOk || !rightOk {
		return Null, fmt.Errorf("buzz: cannot compare %s and %s", left.buzzKind(), right.buzzKind())
	}
	var order int
	switch {
	case leftFloat < rightFloat:
		order = -1
	case leftFloat > rightFloat:
		order = 1
	}
	return BoolValue(cmpResult(op, order)), nil
}

func cmpResult(op OpCode, order int) bool {
	switch op {
	case OpLess:
		return order < 0
	case OpGreater:
		return order > 0
	case OpLessEqual:
		return order <= 0
	default: // OpGreaterEqual
		return order >= 0
	}
}

func asNumeric(v Value) (float64, bool) {
	switch v.tag() {
	case tagInt:
		return float64(int64(v.num())), true
	case tagFloat:
		return math.Float64frombits(v.num()), true
	default:
		return 0, false
	}
}

func asInt(v Value) (int64, bool) {
	switch v.tag() {
	case tagInt:
		return int64(v.num()), true
	case tagFloat:
		return int64(math.Float64frombits(v.num())), true
	default:
		return 0, false
	}
}

// indexGet evaluates obj[idx] for lists (int) and maps (string key).
// Note: map keys are always stringified, so m[1] and m["1"] collide.
// indexGet evaluates obj[idx]. When optional is set (the checked subscript form
// obj[?idx]), an out-of-bounds list/str index yields null instead of an error.
func indexGet(vm *VM, obj, idx Value, optional bool) (Value, error) {
	switch obj.tag() {
	case tagList:
		list := vm.asList(obj)
		i, ok := asInt(idx)
		if !ok {
			return Null, fmt.Errorf("buzz: list index must be an int, got %s", idx.buzzKind())
		}
		if i < 0 || int(i) >= len(list.Items) {
			if optional {
				return Null, nil
			}
			return Null, fmt.Errorf("buzz: list index %d out of range (len %d)", i, len(list.Items))
		}
		return list.Items[i], nil
	case tagMap:
		m := vm.asMap(obj)
		if v, ok := m.get(idx.String()); ok {
			return v, nil
		}
		return Null, nil
	default:
		return Null, fmt.Errorf("buzz: cannot index %s", obj.buzzKind())
	}
}

// errImmutable reports an in-place mutation of an immutable value. Buzz
// collections and objects are immutable unless built with `mut`.
func errImmutable(kind string) error {
	return fmt.Errorf("buzz: cannot mutate immutable %s (declare it with `mut`)", kind)
}

// setIndex evaluates obj[idx] = val for lists and maps.
func setIndex(vm *VM, obj, idx, val Value) error {
	switch obj.tag() {
	case tagList:
		list := vm.asList(obj)
		if !list.Mut {
			return errImmutable("list")
		}
		i, ok := asInt(idx)
		if !ok {
			return fmt.Errorf("buzz: list index must be an int, got %s", idx.buzzKind())
		}
		if i < 0 || int(i) >= len(list.Items) {
			return fmt.Errorf("buzz: list index %d out of range (len %d)", i, len(list.Items))
		}
		list.Items[i] = val
		return nil
	case tagMap:
		m := vm.asMap(obj)
		if !m.Mut {
			return errImmutable("map")
		}
		m.set(idx.String(), val)
		return nil
	default:
		return fmt.Errorf("buzz: cannot index-assign %s", obj.buzzKind())
	}
}

// setMember evaluates obj.name = val for maps and object instances.
func setMember(vm *VM, obj Value, name string, val Value) error {
	switch obj.tag() {
	case tagMap:
		m := vm.asMap(obj)
		if !m.Mut {
			return errImmutable("map")
		}
		m.set(name, val)
		return nil
	case tagObject:
		inst := vm.asObject(obj)
		if !inst.Mut {
			return errImmutable("object")
		}
		if i := inst.Def.fieldIndex(name); i >= 0 {
			inst.Fields[i] = val
			return nil
		}
		return fmt.Errorf("buzz: object %s has no field %q", inst.Def.Name, name)
	default:
		return fmt.Errorf("buzz: cannot set field on %s", obj.buzzKind())
	}
}

// getMember resolves obj.name.
func getMember(vm *VM, obj Value, name string) (Value, error) {
	switch obj.tag() {
	case tagList:
		if m := listMethod(vm, obj, name); m != Null {
			return m, nil
		}
		return Null, nil
	case tagMap:
		m := vm.asMap(obj)
		// Map builtin methods take priority over stored keys with the same name.
		if bm := mapMethod(vm, obj, name); bm != Null {
			return bm, nil
		}
		if v, ok := m.get(name); ok {
			return v, nil
		}
		return Null, nil
	case tagStr:
		if m := strMethod(vm, obj, name); m != Null {
			return m, nil
		}
		return Null, nil
	case tagFib:
		if m := fibMethod(vm, obj, name); m != Null {
			return m, nil
		}
		return Null, nil
	case tagPat:
		if m := patMethod(vm, obj, name); m != Null {
			return m, nil
		}
		return Null, nil
	case tagObject:
		instance := vm.asObject(obj)
		if i := instance.Def.fieldIndex(name); i >= 0 {
			return instance.Fields[i], nil
		}
		if method, ok := instance.Def.method(name); ok {
			bound := *method
			bound.This = obj
			return vm.allocFun(&bound), nil
		}
		return Null, nil
	case tagObjectDef:
		return Null, nil
	case tagEnumDef:
		enumDef := vm.asEnumDef(obj)
		for _, c := range enumDef.Cases {
			if c == name {
				return vm.allocEnumVal(&enumValObj{Enum: enumDef.Name, Case: name}), nil
			}
		}
		return Null, fmt.Errorf("buzz: enum %s has no case %q", enumDef.Name, name)
	case tagEnumVal:
		if name == "name" {
			return StrValue(vm.asEnumVal(obj).Case), nil
		}
		return Null, nil
	default:
		return Null, nil
	}
}

// listMethod returns a bound DirectValue for the named built-in List method,
// or Null if name is not a known list method.
func listMethod(vm *VM, list Value, name string) Value {
	lo := vm.asList(list)
	// In-place mutators require a mutable list (immutable by default).
	switch name {
	case "append", "insert", "remove", "pop", "fill":
		if !lo.Mut {
			return DirectValue("list."+name, func(context.Context, []Value) (Value, error) {
				return Null, errImmutable("list")
			})
		}
	}
	switch name {
	case "len":
		return DirectValue("list.len", func(_ context.Context, _ []Value) (Value, error) {
			return IntValue(int64(len(lo.Items))), nil
		})
	case "append":
		return DirectValue("list.append", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.append: requires a value argument")
			}
			lo.Items = append(lo.Items, args[0])
			return Null, nil
		})
	case "insert":
		return DirectValue("list.insert", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 2 || !args[0].IsInt() {
				return Null, fmt.Errorf("list.insert: requires (int index, T value)")
			}
			idx := int(args[0].AsInt())
			if idx < 0 || idx > len(lo.Items) {
				return Null, fmt.Errorf("list.insert: index %d out of range [0,%d]", idx, len(lo.Items))
			}
			lo.Items = append(lo.Items, Null)
			copy(lo.Items[idx+1:], lo.Items[idx:])
			lo.Items[idx] = args[1]
			return args[1], nil
		})
	case "remove":
		return DirectValue("list.remove", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsInt() {
				return Null, fmt.Errorf("list.remove: requires int index")
			}
			idx := int(args[0].AsInt())
			if idx < 0 || idx >= len(lo.Items) {
				return Null, fmt.Errorf("list.remove: index %d out of range", idx)
			}
			removed := lo.Items[idx]
			lo.Items = append(lo.Items[:idx], lo.Items[idx+1:]...)
			return removed, nil
		})
	case "pop":
		return DirectValue("list.pop", func(_ context.Context, _ []Value) (Value, error) {
			if len(lo.Items) == 0 {
				return Null, nil
			}
			last := lo.Items[len(lo.Items)-1]
			lo.Items = lo.Items[:len(lo.Items)-1]
			return last, nil
		})
	case "sub":
		return DirectValue("list.sub", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsInt() {
				return Null, fmt.Errorf("list.sub: requires (int start, int? len)")
			}
			start := int(args[0].AsInt())
			if start < 0 {
				start = 0
			}
			if start > len(lo.Items) {
				return ListValue(nil), nil
			}
			end := len(lo.Items)
			if len(args) >= 2 && args[1].IsInt() {
				length := int(args[1].AsInt())
				if length < 0 {
					length = 0
				}
				if start+length < end {
					end = start + length
				}
			}
			cp := make([]Value, end-start)
			copy(cp, lo.Items[start:end])
			return ListValue(cp), nil
		})
	case "indexOf":
		return DirectValue("list.indexOf", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.indexOf: requires a value argument")
			}
			for i, it := range lo.Items {
				if it.RawEqual(args[0]) {
					return IntValue(int64(i)), nil
				}
			}
			return Null, nil
		})
	case "join":
		return DirectValue("list.join", func(_ context.Context, args []Value) (Value, error) {
			sep := ""
			if len(args) >= 1 && args[0].IsStr() {
				sep = args[0].AsString()
			}
			parts := make([]string, len(lo.Items))
			for i, it := range lo.Items {
				parts[i] = it.String()
			}
			return StrValue(strings.Join(parts, sep)), nil
		})
	case "forEach":
		return DirectValue("list.forEach", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.forEach: requires a callback function")
			}
			cb := args[0]
			for i, it := range lo.Items {
				if _, err := callValue(vm, ctx, cb, []Value{IntValue(int64(i)), it}); err != nil {
					return Null, err
				}
			}
			return Null, nil
		})
	case "map":
		return DirectValue("list.map", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.map: requires a callback function")
			}
			cb := args[0]
			items := lo.Items
			out := make([]Value, len(items))
			for i, it := range items {
				v, err := callValue(vm, ctx, cb, []Value{IntValue(int64(i)), it})
				if err != nil {
					return Null, err
				}
				out[i] = v
			}
			return ListValue(out), nil
		})
	case "filter":
		return DirectValue("list.filter", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.filter: requires a callback function")
			}
			cb := args[0]
			var out []Value
			for i, it := range lo.Items {
				v, err := callValue(vm, ctx, cb, []Value{IntValue(int64(i)), it})
				if err != nil {
					return Null, err
				}
				if v.Bool() {
					out = append(out, it)
				}
			}
			return ListValue(out), nil
		})
	case "reduce":
		return DirectValue("list.reduce", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 2 {
				return Null, fmt.Errorf("list.reduce: requires (callback, initial)")
			}
			cb := args[0]
			acc := args[1]
			for i, it := range lo.Items {
				v, err := callValue(vm, ctx, cb, []Value{IntValue(int64(i)), it, acc})
				if err != nil {
					return Null, err
				}
				acc = v
			}
			return acc, nil
		})
	case "sort":
		return DirectValue("list.sort", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.sort: requires a comparator callback")
			}
			cb := args[0]
			cp := make([]Value, len(lo.Items))
			copy(cp, lo.Items)
			var sortErr error
			sort.SliceStable(cp, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				v, err := callValue(vm, ctx, cb, []Value{cp[i], cp[j]})
				if err != nil {
					sortErr = err
					return false
				}
				return v.Bool()
			})
			if sortErr != nil {
				return Null, sortErr
			}
			return ListValue(cp), nil
		})
	case "reverse":
		return DirectValue("list.reverse", func(_ context.Context, _ []Value) (Value, error) {
			cp := make([]Value, len(lo.Items))
			for i, v := range lo.Items {
				cp[len(lo.Items)-1-i] = v
			}
			return ListValue(cp), nil
		})
	case "fill":
		return DirectValue("list.fill", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("list.fill: requires a value argument")
			}
			for i := range lo.Items {
				lo.Items[i] = args[0]
			}
			return list, nil
		})
	case "clone", "cloneMutable", "cloneImmutable":
		// cloneMutable yields a mutable copy; clone/cloneImmutable an immutable one.
		mut := name == "cloneMutable"
		return DirectValue("list."+name, func(_ context.Context, _ []Value) (Value, error) {
			cp := make([]Value, len(lo.Items))
			copy(cp, lo.Items)
			return heapValue(tagList, &listObj{Items: cp, Mut: mut}), nil
		})
	}
	return Null
}

// mapMethod returns a bound DirectValue for the named built-in Map method,
// or Null if name is not a known map method.
func mapMethod(vm *VM, m Value, name string) Value {
	mp := vm.asMap(m)
	// In-place mutators require a mutable map (immutable by default).
	if name == "remove" && !mp.Mut {
		return DirectValue("map.remove", func(context.Context, []Value) (Value, error) {
			return Null, errImmutable("map")
		})
	}
	switch name {
	case "size":
		return DirectValue("map.size", func(_ context.Context, _ []Value) (Value, error) {
			return IntValue(int64(len(mp.Keys))), nil
		})
	case "remove":
		return DirectValue("map.remove", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsStr() {
				return Null, fmt.Errorf("map.remove: requires a str key argument")
			}
			key := args[0].AsString()
			for i, k := range mp.Keys {
				if k == key {
					removed := mp.Vals[i]
					mp.Keys = append(mp.Keys[:i], mp.Keys[i+1:]...)
					mp.Vals = append(mp.Vals[:i], mp.Vals[i+1:]...)
					return removed, nil
				}
			}
			return Null, nil
		})
	case "keys":
		return DirectValue("map.keys", func(_ context.Context, _ []Value) (Value, error) {
			out := make([]Value, len(mp.Keys))
			for i, k := range mp.Keys {
				out[i] = StrValue(k)
			}
			return ListValue(out), nil
		})
	case "values":
		return DirectValue("map.values", func(_ context.Context, _ []Value) (Value, error) {
			out := make([]Value, len(mp.Vals))
			copy(out, mp.Vals)
			return ListValue(out), nil
		})
	case "forEach":
		return DirectValue("map.forEach", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("map.forEach: requires a callback function")
			}
			cb := args[0]
			for i, k := range mp.Keys {
				if _, err := callValue(vm, ctx, cb, []Value{StrValue(k), mp.Vals[i]}); err != nil {
					return Null, err
				}
			}
			return Null, nil
		})
	case "filter":
		return DirectValue("map.filter", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("map.filter: requires a callback function")
			}
			cb := args[0]
			out := NewMap()
			for i, k := range mp.Keys {
				v, err := callValue(vm, ctx, cb, []Value{StrValue(k), mp.Vals[i]})
				if err != nil {
					return Null, err
				}
				if v.Bool() {
					out.MapSet(k, mp.Vals[i])
				}
			}
			return out, nil
		})
	case "reduce":
		return DirectValue("map.reduce", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 2 {
				return Null, fmt.Errorf("map.reduce: requires (callback, initial)")
			}
			cb := args[0]
			acc := args[1]
			for i, k := range mp.Keys {
				v, err := callValue(vm, ctx, cb, []Value{StrValue(k), mp.Vals[i], acc})
				if err != nil {
					return Null, err
				}
				acc = v
			}
			return acc, nil
		})
	case "clone", "cloneMutable", "cloneImmutable":
		// cloneMutable yields a mutable copy; clone/cloneImmutable an immutable one.
		mut := name == "cloneMutable"
		return DirectValue("map."+name, func(_ context.Context, _ []Value) (Value, error) {
			nm := newMapObj()
			nm.Mut = mut
			for i, k := range mp.Keys {
				nm.set(k, mp.Vals[i])
			}
			return vm.allocMap(nm), nil
		})
	case "diff":
		return DirectValue("map.diff", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsMap() {
				return Null, fmt.Errorf("map.diff: requires a map argument")
			}
			other := args[0]
			out := NewMap()
			for i, k := range mp.Keys {
				if _, ok := other.MapGet(k); !ok {
					out.MapSet(k, mp.Vals[i])
				}
			}
			return out, nil
		})
	case "intersect":
		return DirectValue("map.intersect", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsMap() {
				return Null, fmt.Errorf("map.intersect: requires a map argument")
			}
			other := args[0]
			out := NewMap()
			for i, k := range mp.Keys {
				if _, ok := other.MapGet(k); ok {
					out.MapSet(k, mp.Vals[i])
				}
			}
			return out, nil
		})
	case "sort":
		return DirectValue("map.sort", func(ctx context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("map.sort: requires a comparator callback")
			}
			cb := args[0]
			type pair struct {
				k string
				v Value
			}
			pairs := make([]pair, len(mp.Keys))
			for i, k := range mp.Keys {
				pairs[i] = pair{k, mp.Vals[i]}
			}
			var sortErr error
			sort.SliceStable(pairs, func(i, j int) bool {
				if sortErr != nil {
					return false
				}
				v, err := callValue(vm, ctx, cb, []Value{StrValue(pairs[i].k), StrValue(pairs[j].k)})
				if err != nil {
					sortErr = err
					return false
				}
				return v.Bool()
			})
			if sortErr != nil {
				return Null, sortErr
			}
			out := NewMap()
			for _, p := range pairs {
				out.MapSet(p.k, p.v)
			}
			return out, nil
		})
	}
	return Null
}

// strMethod returns a bound DirectValue for the named built-in String method,
// or Null if name is not a known string method.
func strMethod(vm *VM, s Value, name string) Value {
	sobj := vm.asStr(s)
	str := sobj.V
	switch name {
	case "len":
		return DirectValue("str.len", func(_ context.Context, _ []Value) (Value, error) {
			return IntValue(int64(utf8.RuneCountInString(str))), nil
		})
	case "upper":
		return DirectValue("str.upper", func(_ context.Context, _ []Value) (Value, error) {
			return StrValue(strings.ToUpper(str)), nil
		})
	case "lower":
		return DirectValue("str.lower", func(_ context.Context, _ []Value) (Value, error) {
			return StrValue(strings.ToLower(str)), nil
		})
	case "trim":
		return DirectValue("str.trim", func(_ context.Context, _ []Value) (Value, error) {
			return StrValue(strings.TrimSpace(str)), nil
		})
	case "byte":
		return DirectValue("str.byte", func(_ context.Context, args []Value) (Value, error) {
			at := 0
			if len(args) >= 1 && args[0].IsInt() {
				at = int(args[0].AsInt())
			}
			runes := []rune(str)
			if at < 0 || at >= len(runes) {
				return Null, fmt.Errorf("str.byte: index %d out of range", at)
			}
			return IntValue(int64(runes[at])), nil
		})
	case "indexOf":
		return DirectValue("str.indexOf", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsStr() {
				return Null, fmt.Errorf("str.indexOf: requires a str needle argument")
			}
			idx := strings.Index(str, args[0].AsString())
			if idx < 0 {
				return Null, nil
			}
			// Return byte offset converted to rune index.
			return IntValue(int64(utf8.RuneCountInString(str[:idx]))), nil
		})
	case "startsWith":
		return DirectValue("str.startsWith", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsStr() {
				return Null, fmt.Errorf("str.startsWith: requires a str argument")
			}
			return BoolValue(strings.HasPrefix(str, args[0].AsString())), nil
		})
	case "endsWith":
		return DirectValue("str.endsWith", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsStr() {
				return Null, fmt.Errorf("str.endsWith: requires a str argument")
			}
			return BoolValue(strings.HasSuffix(str, args[0].AsString())), nil
		})
	case "replace":
		return DirectValue("str.replace", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 2 || !args[0].IsStr() || !args[1].IsStr() {
				return Null, fmt.Errorf("str.replace: requires (str needle, str with)")
			}
			return StrValue(strings.Replace(str, args[0].AsString(), args[1].AsString(), 1)), nil
		})
	case "split":
		return DirectValue("str.split", func(_ context.Context, args []Value) (Value, error) {
			sep := ""
			if len(args) >= 1 && args[0].IsStr() {
				sep = args[0].AsString()
			}
			var parts []string
			if sep == "" {
				parts = strings.Fields(str)
			} else {
				parts = strings.Split(str, sep)
			}
			out := make([]Value, len(parts))
			for i, p := range parts {
				out[i] = StrValue(p)
			}
			return ListValue(out), nil
		})
	case "sub":
		return DirectValue("str.sub", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsInt() {
				return Null, fmt.Errorf("str.sub: requires (int start, int? len)")
			}
			start := int(args[0].AsInt())
			if start < 0 {
				start = 0
			}
			// Fast path: for a pure-ASCII string a rune index equals a byte index,
			// so the substring is a direct byte slice - no []rune copy of the whole
			// string per call. The result is identical to the rune path below.
			if sobj.isASCII() {
				if start >= len(str) {
					return StrValue(""), nil
				}
				end := len(str)
				if len(args) >= 2 && args[1].IsInt() {
					length := int(args[1].AsInt())
					if length < 0 {
						length = 0
					}
					if start+length < end {
						end = start + length
					}
				}
				return StrValue(str[start:end]), nil
			}
			runes := []rune(str)
			if start >= len(runes) {
				return StrValue(""), nil
			}
			end := len(runes)
			if len(args) >= 2 && args[1].IsInt() {
				length := int(args[1].AsInt())
				if length < 0 {
					length = 0
				}
				if start+length < end {
					end = start + length
				}
			}
			return StrValue(string(runes[start:end])), nil
		})
	case "repeat":
		return DirectValue("str.repeat", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 || !args[0].IsInt() {
				return Null, fmt.Errorf("str.repeat: requires int n")
			}
			n := int(args[0].AsInt())
			if n < 0 {
				n = 0
			}
			return StrValue(strings.Repeat(str, n)), nil
		})
	case "encodeBase64":
		return DirectValue("str.encodeBase64", func(_ context.Context, _ []Value) (Value, error) {
			return StrValue(base64.StdEncoding.EncodeToString([]byte(str))), nil
		})
	case "decodeBase64":
		return DirectValue("str.decodeBase64", func(_ context.Context, _ []Value) (Value, error) {
			b, err := base64.StdEncoding.DecodeString(str)
			if err != nil {
				return Null, fmt.Errorf("str.decodeBase64: %w", err)
			}
			return StrValue(string(b)), nil
		})
	case "hex":
		return DirectValue("str.hex", func(_ context.Context, _ []Value) (Value, error) {
			return StrValue(hex.EncodeToString([]byte(str))), nil
		})
	case "bin":
		return DirectValue("str.bin", func(_ context.Context, _ []Value) (Value, error) {
			b, err := hex.DecodeString(str)
			if err != nil {
				return Null, fmt.Errorf("str.bin: %w", err)
			}
			return StrValue(string(b)), nil
		})
	case "utf8Len":
		return DirectValue("str.utf8Len", func(_ context.Context, _ []Value) (Value, error) {
			return IntValue(int64(utf8.RuneCountInString(str))), nil
		})
	case "utf8Valid":
		return DirectValue("str.utf8Valid", func(_ context.Context, _ []Value) (Value, error) {
			return BoolValue(utf8.ValidString(str)), nil
		})
	case "utf8Codepoints":
		return DirectValue("str.utf8Codepoints", func(_ context.Context, _ []Value) (Value, error) {
			runes := []rune(str)
			out := make([]Value, 0, len(runes))
			for _, r := range runes {
				if unicode.IsGraphic(r) || r == '\n' || r == '\t' {
					out = append(out, StrValue(string(r)))
				}
			}
			return ListValue(out), nil
		})
	}
	return Null
}

// fibMethod returns a bound DirectValue for the named built-in Fiber method,
// or Null if name is not a known fiber method.
func fibMethod(vm *VM, fib Value, name string) Value {
	fo := vm.asFib(fib)
	switch name {
	case "over":
		return DirectValue("fib.over", func(_ context.Context, _ []Value) (Value, error) {
			return BoolValue(fo.status == fibDone), nil
		})
	case "cancel":
		return DirectValue("fib.cancel", func(_ context.Context, _ []Value) (Value, error) {
			fo.status = fibDone
			return Null, nil
		})
	case "isMain":
		return DirectValue("fib.isMain", func(_ context.Context, _ []Value) (Value, error) {
			return False, nil // host-side fiber objects are never the main fiber
		})
	}
	return Null
}

// callValue invokes a Buzz callable (direct or fun) with args.
// Used by higher-order list/map methods (forEach, map, filter, reduce, sort).
func callValue(vm *VM, ctx context.Context, callee Value, args []Value) (Value, error) {
	switch callee.tag() {
	case tagDirect:
		return vm.asDirect(callee).Fn(ctx, args)
	case tagFun:
		callVM := NewVM(ctx)
		if err := callVM.Call(callee, args); err != nil {
			return Null, err
		}
		return callVM.Exec()
	default:
		return Null, fmt.Errorf("buzz: value of kind %q is not callable", callee.buzzKind())
	}
}

var arithSymbols = [...]string{
	OpAdd: "+",
	OpSub: "-",
	OpMul: "*",
	OpDiv: "/",
	OpMod: "%",
}

func arithSymbol(op OpCode) string {
	if int(op) < len(arithSymbols) && arithSymbols[op] != "" {
		return arithSymbols[op]
	}
	return "?"
}

// Hot/cold split for Exec's arithmetic and comparison cases [A4].
// Each method is //go:noinline so fmt.Errorf, type switches, and string/float
// paths never appear in Exec's inlined code graph — keeping Exec's stack frame
// small and its I-cache footprint tight for the hot dispatch loop.
// The int fast paths remain inline in Exec; these cover everything else.

//go:noinline
func (vm *VM) slowAdd(left, right Value) (Value, error) { return arith(vm, OpAdd, left, right) }

//go:noinline
func (vm *VM) slowSub(left, right Value) (Value, error) { return arith(vm, OpSub, left, right) }

//go:noinline
func (vm *VM) slowMul(left, right Value) (Value, error) { return arith(vm, OpMul, left, right) }

//go:noinline
func (vm *VM) slowDiv(left, right Value) (Value, error) { return arith(vm, OpDiv, left, right) }

//go:noinline
func (vm *VM) slowMod(left, right Value) (Value, error) { return arith(vm, OpMod, left, right) }

//go:noinline
func (vm *VM) slowEqual(left, right Value) bool { return valuesEqual(left, right) }

//go:noinline
func (vm *VM) slowCompare(op OpCode, left, right Value) (Value, error) {
	return compare(vm, op, left, right)
}
