package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAnnot_Primitives(t *testing.T) {
	for _, name := range []string{"int", "double", "str", "bool", "null", "void", "any"} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, name, ParseAnnot(name).TypeName())
		})
	}
}

func TestParseAnnot_EmptyReturnsAny(t *testing.T) {
	assert.Equal(t, "any", ParseAnnot("").TypeName())
}

func TestParseAnnot_ListType(t *testing.T) {
	assert.Equal(t, "[str]", ParseAnnot("[str]").TypeName())
}

func TestParseAnnot_MapType(t *testing.T) {
	assert.Equal(t, "{str:int}", ParseAnnot("{str:int}").TypeName())
}

func TestParseAnnot_FuncType(t *testing.T) {
	assert.Equal(t, "fun(int)str", ParseAnnot("fun(int)str").TypeName())
}

func TestParseAnnot_NamedType(t *testing.T) {
	assert.Equal(t, "MyRecord", ParseAnnot("MyRecord").TypeName())
}

func TestCompat_AnyIsCompatWithAnything(t *testing.T) {
	assert.True(t, Compat(Any, Int), "Compat(Any, Int)")
	assert.True(t, Compat(Str, Any), "Compat(Str, Any)")
}

func TestCompat_NullIsCompatWithAnything(t *testing.T) {
	assert.True(t, Compat(Null, Str), "Compat(Null, Str)")
}

func TestCompat_SameTypeName(t *testing.T) {
	assert.True(t, Compat(Int, Int), "Compat(Int, Int)")
	assert.True(t, Compat(Str, Str), "Compat(Str, Str)")
}

func TestCompat_DifferentTypes(t *testing.T) {
	assert.False(t, Compat(Int, Str), "Compat(Int, Str)")
	assert.False(t, Compat(Bool, Double), "Compat(Bool, Double)")
}

// TestCompat_NilFuncReturn guards the structural recursion against nil leaves. A
// function type with no declared return (fun(any)) has Ret == nil; before the nil
// guard, comparing two such types nested in a map panicked with a nil deref.
func TestCompat_NilFuncReturn(t *testing.T) {
	funAny := func() *FuncType { return &FuncType{Params: []Type{Any}} }
	require.Nil(t, funAny().Ret, "setup: fun(any) should have nil Ret")
	assert.True(t, Compat(funAny(), funAny()), "Compat(fun(any), fun(any))")
	// The original crash shape: {str: fun(fun(any)) bool} compared to itself.
	mapOfFunc := func() Type {
		return &MapType{Key: Str, Val: &FuncType{
			Params: []Type{funAny()},
			Ret:    Bool,
		}}
	}
	assert.True(t, Compat(mapOfFunc(), mapOfFunc()), "Compat over nested nil-return func types")
}

func TestCompat_ContainerElementAny(t *testing.T) {
	listAny := &ListType{Elem: Any}
	listDouble := &ListType{Elem: Double}
	listStr := &ListType{Elem: Str}

	// The top-level Any-escape rule applies element-wise: [any] <-> [double].
	assert.True(t, Compat(listAny, listDouble), "Compat([any], [double])")
	assert.True(t, Compat(listDouble, listAny), "Compat([double], [any])")
	// Concrete element mismatches are still rejected.
	assert.False(t, Compat(listStr, listDouble), "Compat([str], [double])")

	mapAny := &MapType{Key: Str, Val: Any}
	mapDouble := &MapType{Key: Str, Val: Double}
	assert.True(t, Compat(mapAny, mapDouble), "Compat({str:any}, {str:double})")
}

func TestCompat_FuncTypes(t *testing.T) {
	f1 := &FuncType{Params: []Type{Int}, Ret: Str}
	f2 := &FuncType{Params: []Type{Int}, Ret: Str}
	f3 := &FuncType{Params: []Type{Str}, Ret: Int}

	assert.True(t, Compat(f1, f2), "Compat(identical FuncTypes)")
	assert.False(t, Compat(f1, f3), "Compat(different FuncTypes)")
}
