package types_test

import (
	"testing"

	"github.com/egladman/gopherbuzz/types"
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
			got := types.ParseAnnot(tc.input)
			if got.TypeName() != tc.wantName {
				t.Errorf("ParseAnnot(%q).TypeName() = %q, want %q", tc.input, got.TypeName(), tc.wantName)
			}
		})
	}
}

func TestParseAnnot_EmptyReturnsAny(t *testing.T) {
	got := types.ParseAnnot("")
	if got.TypeName() != "any" {
		t.Errorf("ParseAnnot(\"\").TypeName() = %q, want %q", got.TypeName(), "any")
	}
}

func TestParseAnnot_ListType(t *testing.T) {
	got := types.ParseAnnot("[str]")
	if got.TypeName() != "[str]" {
		t.Errorf("ParseAnnot(\"[str]\").TypeName() = %q, want %q", got.TypeName(), "[str]")
	}
}

func TestParseAnnot_MapType(t *testing.T) {
	got := types.ParseAnnot("{str:int}")
	if got.TypeName() != "{str:int}" {
		t.Errorf("ParseAnnot(\"{str:int}\").TypeName() = %q, want %q", got.TypeName(), "{str:int}")
	}
}

func TestParseAnnot_FuncType(t *testing.T) {
	got := types.ParseAnnot("fun(int)str")
	if got.TypeName() != "fun(int)str" {
		t.Errorf("ParseAnnot(\"fun(int)str\").TypeName() = %q, want %q", got.TypeName(), "fun(int)str")
	}
}

func TestParseAnnot_NamedType(t *testing.T) {
	got := types.ParseAnnot("MyRecord")
	if got.TypeName() != "MyRecord" {
		t.Errorf("ParseAnnot(\"MyRecord\").TypeName() = %q, want %q", got.TypeName(), "MyRecord")
	}
}

func TestCompat_AnyIsCompatWithAnything(t *testing.T) {
	if !types.Compat(types.Any, types.Int) {
		t.Error("Compat(Any, Int) = false, want true")
	}
	if !types.Compat(types.Str, types.Any) {
		t.Error("Compat(Str, Any) = false, want true")
	}
}

func TestCompat_NullIsCompatWithAnything(t *testing.T) {
	if !types.Compat(types.Null, types.Str) {
		t.Error("Compat(Null, Str) = false, want true")
	}
}

func TestCompat_SameTypeName(t *testing.T) {
	if !types.Compat(types.Int, types.Int) {
		t.Error("Compat(Int, Int) = false, want true")
	}
	if !types.Compat(types.Str, types.Str) {
		t.Error("Compat(Str, Str) = false, want true")
	}
}

func TestCompat_DifferentTypes(t *testing.T) {
	if types.Compat(types.Int, types.Str) {
		t.Error("Compat(Int, Str) = true, want false")
	}
	if types.Compat(types.Bool, types.Double) {
		t.Error("Compat(Bool, Float) = true, want false")
	}
}

func TestCompat_FuncTypes(t *testing.T) {
	f1 := &types.FuncType{Params: []types.Type{types.Int}, Ret: types.Str}
	f2 := &types.FuncType{Params: []types.Type{types.Int}, Ret: types.Str}
	f3 := &types.FuncType{Params: []types.Type{types.Str}, Ret: types.Int}

	if !types.Compat(f1, f2) {
		t.Error("Compat(identical FuncTypes) = false, want true")
	}
	if types.Compat(f1, f3) {
		t.Error("Compat(different FuncTypes) = true, want false")
	}
}
