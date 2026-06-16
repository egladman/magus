package types

import (
	"testing"
)

func TestParseAnnot_Primitives(t *testing.T) {
	cases := []struct {
		input    string
		wantName string
	}{
		{"int", "int"},
		{"double", "double"},
		{"str", "str"},
		{"bool", "bool"},
		{"null", "null"},
		{"void", "void"},
		{"any", "any"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := ParseAnnot(tc.input)
			if got.TypeName() != tc.wantName {
				t.Errorf("ParseAnnot(%q).TypeName() = %q, want %q", tc.input, got.TypeName(), tc.wantName)
			}
		})
	}
}

func TestParseAnnot_EmptyReturnsAny(t *testing.T) {
	got := ParseAnnot("")
	if got.TypeName() != "any" {
		t.Errorf("ParseAnnot(\"\").TypeName() = %q, want %q", got.TypeName(), "any")
	}
}

func TestParseAnnot_ListType(t *testing.T) {
	got := ParseAnnot("[str]")
	if got.TypeName() != "[str]" {
		t.Errorf("ParseAnnot(\"[str]\").TypeName() = %q, want %q", got.TypeName(), "[str]")
	}
}

func TestParseAnnot_MapType(t *testing.T) {
	got := ParseAnnot("{str:int}")
	if got.TypeName() != "{str:int}" {
		t.Errorf("ParseAnnot(\"{str:int}\").TypeName() = %q, want %q", got.TypeName(), "{str:int}")
	}
}

func TestParseAnnot_FuncType(t *testing.T) {
	got := ParseAnnot("fun(int)str")
	if got.TypeName() != "fun(int)str" {
		t.Errorf("ParseAnnot(\"fun(int)str\").TypeName() = %q, want %q", got.TypeName(), "fun(int)str")
	}
}

func TestParseAnnot_NamedType(t *testing.T) {
	got := ParseAnnot("MyRecord")
	if got.TypeName() != "MyRecord" {
		t.Errorf("ParseAnnot(\"MyRecord\").TypeName() = %q, want %q", got.TypeName(), "MyRecord")
	}
}

func TestCompat_AnyIsCompatWithAnything(t *testing.T) {
	if !Compat(Any, Int) {
		t.Error("Compat(Any, Int) = false, want true")
	}
	if !Compat(Str, Any) {
		t.Error("Compat(Str, Any) = false, want true")
	}
}

func TestCompat_NullIsCompatWithAnything(t *testing.T) {
	if !Compat(Null, Str) {
		t.Error("Compat(Null, Str) = false, want true")
	}
}

func TestCompat_SameTypeName(t *testing.T) {
	if !Compat(Int, Int) {
		t.Error("Compat(Int, Int) = false, want true")
	}
	if !Compat(Str, Str) {
		t.Error("Compat(Str, Str) = false, want true")
	}
}

func TestCompat_DifferentTypes(t *testing.T) {
	if Compat(Int, Str) {
		t.Error("Compat(Int, Str) = true, want false")
	}
	if Compat(Bool, Double) {
		t.Error("Compat(Bool, Float) = true, want false")
	}
}

// TestCompat_NilFuncReturn guards the structural recursion against nil leaves. A
// function type with no declared return (fun(any)) has Ret == nil; before the nil
// guard, comparing two such types nested in a map panicked with a nil deref.
func TestCompat_NilFuncReturn(t *testing.T) {
	funAny := func() *FuncType { return &FuncType{Params: []Type{Any}} }
	if funAny().Ret != nil {
		t.Fatal("setup: fun(any) should have nil Ret")
	}
	if !Compat(funAny(), funAny()) {
		t.Error("Compat(fun(any), fun(any)) = false, want true")
	}
	// The original crash shape: {str: fun(fun(any)) bool} compared to itself.
	mapOfFunc := func() Type {
		return &MapType{Key: Str, Val: &FuncType{
			Params: []Type{funAny()},
			Ret:    Bool,
		}}
	}
	if !Compat(mapOfFunc(), mapOfFunc()) {
		t.Error("Compat over nested nil-return func types = false, want true")
	}
}

func TestCompat_ContainerElementAny(t *testing.T) {
	listAny := &ListType{Elem: Any}
	listDouble := &ListType{Elem: Double}
	listStr := &ListType{Elem: Str}

	// The top-level Any-escape rule applies element-wise: [any] <-> [double].
	if !Compat(listAny, listDouble) {
		t.Error("Compat([any], [double]) = false, want true")
	}
	if !Compat(listDouble, listAny) {
		t.Error("Compat([double], [any]) = false, want true")
	}
	// Concrete element mismatches are still rejected.
	if Compat(listStr, listDouble) {
		t.Error("Compat([str], [double]) = true, want false")
	}

	mapAny := &MapType{Key: Str, Val: Any}
	mapDouble := &MapType{Key: Str, Val: Double}
	if !Compat(mapAny, mapDouble) {
		t.Error("Compat({str:any}, {str:double}) = false, want true")
	}
}

func TestCompat_FuncTypes(t *testing.T) {
	f1 := &FuncType{Params: []Type{Int}, Ret: Str}
	f2 := &FuncType{Params: []Type{Int}, Ret: Str}
	f3 := &FuncType{Params: []Type{Str}, Ret: Int}

	if !Compat(f1, f2) {
		t.Error("Compat(identical FuncTypes) = false, want true")
	}
	if Compat(f1, f3) {
		t.Error("Compat(different FuncTypes) = true, want false")
	}
}
