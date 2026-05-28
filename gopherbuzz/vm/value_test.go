package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/gopherbuzz/vm"
)

func TestValueConstructorsAndPredicates(t *testing.T) {
	noop := func(_ context.Context, _ []vm.Value) (vm.Value, error) { return vm.NullValue(), nil }

	tests := []struct {
		name    string
		v       vm.Value
		isInt   bool
		isFloat bool
		isBool  bool
		isStr   bool
		isList  bool
		isMap   bool
		isNull  bool
		isDirect bool
		kind    string
	}{
		{
			name:   "IntValue",
			v:      vm.IntValue(42),
			isInt:  true,
			kind:   "int",
		},
		{
			name:    "FloatValue",
			v:       vm.FloatValue(3.14),
			isFloat: true,
			kind:    "double",
		},
		{
			name:   "BoolValue true",
			v:      vm.BoolValue(true),
			isBool: true,
			kind:   "bool",
		},
		{
			name:   "BoolValue false",
			v:      vm.BoolValue(false),
			isBool: true,
			kind:   "bool",
		},
		{
			name:  "StrValue",
			v:     vm.StrValue("hello"),
			isStr: true,
			kind:  "str",
		},
		{
			name:   "ListValue",
			v:      vm.ListValue(nil),
			isList: true,
			kind:   "list",
		},
		{
			name:  "NewMap",
			v:     vm.NewMap(),
			isMap: true,
			kind:  "map",
		},
		{
			name:   "NullValue",
			v:      vm.NullValue(),
			isNull: true,
			kind:   "null",
		},
		{
			name:     "DirectValue",
			v:        vm.DirectValue("myfn", noop),
			isDirect: true,
			kind:     "direct",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.v.IsInt(); got != tc.isInt {
				t.Errorf("IsInt() = %v, want %v", got, tc.isInt)
			}
			if got := tc.v.IsFloat(); got != tc.isFloat {
				t.Errorf("IsFloat() = %v, want %v", got, tc.isFloat)
			}
			if got := tc.v.IsBool(); got != tc.isBool {
				t.Errorf("IsBool() = %v, want %v", got, tc.isBool)
			}
			if got := tc.v.IsStr(); got != tc.isStr {
				t.Errorf("IsStr() = %v, want %v", got, tc.isStr)
			}
			if got := tc.v.IsList(); got != tc.isList {
				t.Errorf("IsList() = %v, want %v", got, tc.isList)
			}
			if got := tc.v.IsMap(); got != tc.isMap {
				t.Errorf("IsMap() = %v, want %v", got, tc.isMap)
			}
			if got := tc.v.IsNull(); got != tc.isNull {
				t.Errorf("IsNull() = %v, want %v", got, tc.isNull)
			}
			if got := tc.v.IsDirect(); got != tc.isDirect {
				t.Errorf("IsDirect() = %v, want %v", got, tc.isDirect)
			}
			if got := tc.v.Kind(); got != tc.kind {
				t.Errorf("Kind() = %q, want %q", got, tc.kind)
			}
		})
	}
}

func TestValueAsInt(t *testing.T) {
	v := vm.IntValue(99)
	if got := v.AsInt(); got != 99 {
		t.Errorf("AsInt() = %d, want 99", got)
	}
}

func TestValueAsIntNegative(t *testing.T) {
	v := vm.IntValue(-7)
	if got := v.AsInt(); got != -7 {
		t.Errorf("AsInt() = %d, want -7", got)
	}
}

func TestValueAsFloat(t *testing.T) {
	v := vm.FloatValue(2.718)
	if got := v.AsFloat(); got != 2.718 {
		t.Errorf("AsFloat() = %v, want 2.718", got)
	}
}

func TestValueAsBool(t *testing.T) {
	if !vm.BoolValue(true).AsBool() {
		t.Error("BoolValue(true).AsBool() = false, want true")
	}
	if vm.BoolValue(false).AsBool() {
		t.Error("BoolValue(false).AsBool() = true, want false")
	}
}

func TestValueAsString(t *testing.T) {
	v := vm.StrValue("world")
	if got := v.AsString(); got != "world" {
		t.Errorf("AsString() = %q, want %q", got, "world")
	}
}

func TestValueListItems(t *testing.T) {
	items := []vm.Value{vm.IntValue(1), vm.IntValue(2), vm.IntValue(3)}
	v := vm.ListValue(items)
	got := v.ListItems()
	if len(got) != 3 {
		t.Fatalf("ListItems() len = %d, want 3", len(got))
	}
	for i, item := range got {
		if item.AsInt() != int64(i+1) {
			t.Errorf("ListItems()[%d].AsInt() = %d, want %d", i, item.AsInt(), i+1)
		}
	}
}

func TestValueListItemsNil(t *testing.T) {
	v := vm.ListValue(nil)
	if got := v.ListItems(); got != nil {
		t.Errorf("ListItems() on nil-backed list = %v, want nil", got)
	}
}

func TestValueString(t *testing.T) {
	tests := []struct {
		v    vm.Value
		want string
	}{
		{vm.NullValue(), "null"},
		{vm.BoolValue(true), "true"},
		{vm.BoolValue(false), "false"},
		{vm.IntValue(42), "42"},
		{vm.StrValue("hi"), "hi"},
		{vm.ListValue([]vm.Value{vm.IntValue(1), vm.IntValue(2)}), "[1, 2]"},
	}
	for _, tc := range tests {
		got := tc.v.String()
		if got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
	}
}

func TestValueRawEqual(t *testing.T) {
	// Scalars with same tag and payload must be equal.
	if !vm.IntValue(5).RawEqual(vm.IntValue(5)) {
		t.Error("IntValue(5).RawEqual(IntValue(5)) = false, want true")
	}
	if vm.IntValue(5).RawEqual(vm.IntValue(6)) {
		t.Error("IntValue(5).RawEqual(IntValue(6)) = true, want false")
	}
	if !vm.NullValue().RawEqual(vm.NullValue()) {
		t.Error("NullValue().RawEqual(NullValue()) = false, want true")
	}
	if !vm.BoolValue(true).RawEqual(vm.BoolValue(true)) {
		t.Error("BoolValue(true).RawEqual(BoolValue(true)) = false, want true")
	}
	if vm.BoolValue(true).RawEqual(vm.BoolValue(false)) {
		t.Error("BoolValue(true).RawEqual(BoolValue(false)) = true, want false")
	}
	// Different types are not raw-equal even for the same numeric payload.
	if vm.IntValue(0).RawEqual(vm.NullValue()) {
		t.Error("IntValue(0).RawEqual(NullValue()) = true, want false")
	}
}
