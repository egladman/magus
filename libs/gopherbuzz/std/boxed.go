package std

import "github.com/egladman/magus/libs/gopherbuzz/vm"

func boxedFloating(v vm.Value) vm.Value {
	if v.IsFloat() {
		return v
	}
	return vm.Null
}

func boxedFloatingValue(v vm.Value) vm.Value {
	if v.IsFloat() {
		return v
	}
	return vm.FloatValue(0)
}

func boxedString(v vm.Value) vm.Value {
	if v.IsString() {
		return v
	}
	return vm.Null
}

func boxedBoolean(v vm.Value) vm.Value {
	if v.IsBool() {
		return v
	}
	return vm.Null
}

func boxedInteger(v vm.Value) vm.Value {
	if v.IsInt() {
		return v
	}
	return vm.Null
}

func boxedStringValue(v vm.Value) vm.Value {
	if v.IsString() {
		return v
	}
	return vm.StringValue("")
}

func boxedBooleanValue(v vm.Value) vm.Value {
	if v.IsBool() {
		return v
	}
	return vm.False
}

func boxedIntegerValue(v vm.Value) vm.Value {
	if v.IsInt() {
		return v
	}
	return vm.IntValue(0)
}
