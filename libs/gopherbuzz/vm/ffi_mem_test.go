package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCTypeLayout(t *testing.T) {
	check := func(name string, wantSize, wantAlign int) {
		size, align, ok := CTypeLayout(name)
		require.Truef(t, ok, "CTypeLayout(%q) ok=false", name)
		assert.Equalf(t, wantSize, size, "CTypeLayout(%q) size", name)
		assert.Equalf(t, wantAlign, align, "CTypeLayout(%q) align", name)
	}

	check("bool", 1, 1)
	check("char", 1, 1)
	check("int8_t", 1, 1)
	check("short", 2, 2)
	check("int", 4, 4)
	check("unsigned int", 4, 4)
	check("int64_t", 8, 8)
	check("long long", 8, 8)
	check("float", 4, 4)
	check("double", 8, 8)
	check("char*", ptrSize, ptrSize)
	check("void*", ptrSize, ptrSize)
	check("int*", ptrSize, ptrSize)
	check("const char *", ptrSize, ptrSize)

	_, _, ok := CTypeLayout("frobnicate")
	assert.False(t, ok, "CTypeLayout(unknown) ok=true, want false")
}

func TestStructLayout(t *testing.T) {
	// struct { int32 id; double score } -> [0, 8], size 16, align 8.
	size, align, offsets, err := StructLayout([]string{"int", "double"})
	require.NoError(t, err)
	assert.Equal(t, 16, size)
	assert.Equal(t, 8, align)
	assert.Equal(t, []int{0, 8}, offsets)

	// struct { char; int } -> char at 0, int at 4 (3 bytes pad), size 8.
	size, _, offsets, err = StructLayout([]string{"char", "int"})
	require.NoError(t, err)
	assert.Equal(t, 4, offsets[1], "char,int layout: int offset")
	assert.Equal(t, 8, size, "char,int layout: size")

	_, _, _, err = StructLayout([]string{"int", "bogus"})
	assert.Error(t, err, "StructLayout with bad field type: want error")
}

func TestAllocReadWriteRoundTrip(t *testing.T) {
	addr, err := AllocFFI(16)
	require.NoError(t, err)
	defer func() { _ = FreeFFI(addr) }()

	require.NoError(t, WriteScalar(addr, 0, "int", 0x7fffffff, 0, false))
	require.NoError(t, WriteScalar(addr, 8, "double", 0, 2.71828, true))

	i, _, _, err := ReadScalar(addr, 0, "int")
	require.NoError(t, err)
	assert.Equal(t, int64(0x7fffffff), i)

	_, f, isF, err := ReadScalar(addr, 8, "double")
	require.NoError(t, err)
	assert.True(t, isF, "read double isFloat")
	assert.Equal(t, 2.71828, f)
}

func TestReadScalarSignExtension(t *testing.T) {
	addr, err := AllocFFI(8)
	require.NoError(t, err)
	defer func() { _ = FreeFFI(addr) }()

	// Store -1 as int8 (0xFF) and confirm signed/unsigned reads differ.
	require.NoError(t, WriteScalar(addr, 0, "int8_t", -1, 0, false))

	i, _, _, _ := ReadScalar(addr, 0, "int8_t")
	assert.Equal(t, int64(-1), i, "signed int8_t read")

	i, _, _, _ = ReadScalar(addr, 0, "uint8_t")
	assert.Equal(t, int64(255), i, "unsigned uint8_t read")
}

func TestMemBoundsAndFreeErrors(t *testing.T) {
	addr, err := AllocFFI(4)
	require.NoError(t, err)

	// Out-of-bounds write is rejected, not a segfault.
	assert.Error(t, WriteScalar(addr, 0, "double", 0, 1, true), "OOB write: want error")

	// Reading a never-allocated address is rejected.
	_, _, _, err = ReadScalar(addr+1<<20, 0, "int")
	assert.Error(t, err, "read of foreign address: want error")

	require.NoError(t, FreeFFI(addr), "first free")

	// Double free is an error, not a panic.
	assert.Error(t, FreeFFI(addr), "double free: want error")

	_, err = AllocFFI(0)
	assert.Error(t, err, "alloc(0): want error")
}

// marshalStructArg / unmarshalStructArg had no test of any kind, which is how a
// bool field silently never reaching C survived. They are reachable in normal use
// only through a real zdef'd call into a C library, so they are exercised directly
// here: the registries they read are plain package-level maps.
func withDeclaredStruct(t *testing.T, name string, fields []string) {
	t.Helper()
	declaredFieldTypes[name] = fields
	t.Cleanup(func() {
		delete(declaredFieldTypes, name)
		delete(declaredAggregates, name)
	})
}

// TestMarshalStructArgRoundTripsBool pins both directions of the bool defect. The
// marshal switch handled only tagInt and tagFloat, so a bool field was never
// written and every bool reached C as the zero AllocFFI left - false, however the
// script set it. The read-back then wrote IntValue over the field, so a round trip
// also changed the field's TYPE from bool to int.
func TestMarshalStructArgRoundTripsBool(t *testing.T) {
	withDeclaredStruct(t, "Flags", []string{"bool", "i32", "bool"})

	def := &objectDefObj{Name: "Flags"}
	inst := &objectInst{
		Def:    def,
		Fields: []Value{BoolValue(true), IntValue(7), BoolValue(false)},
	}

	sa, err := marshalStructArg(inst)
	require.NoError(t, err, "marshalStructArg")
	t.Cleanup(func() { _ = FreeFFI(sa.addr) })

	// Read the block back exactly as a C callee would see it: a true bool must be
	// 1 in the field's own width, not the zero the allocation started at.
	got, _, _, err := ReadScalar(sa.addr, sa.offsets[0], "bool")
	require.NoError(t, err, "ReadScalar field 0")
	assert.Equal(t, int64(1), got, "a true bool must reach C as 1, not the zero AllocFFI left")

	got2, _, _, err := ReadScalar(sa.addr, sa.offsets[2], "bool")
	require.NoError(t, err, "ReadScalar field 2")
	assert.Equal(t, int64(0), got2, "a false bool is 0")

	require.NoError(t, unmarshalStructArg(sa), "unmarshalStructArg")
	assert.True(t, inst.Fields[0].IsBool(), "a bool field must come back a bool, not an int")
	assert.True(t, inst.Fields[0].AsBool(), "and must keep its value")
	assert.True(t, inst.Fields[2].IsBool(), "including a false one")
	assert.False(t, inst.Fields[2].AsBool(), "which stays false")
	assert.Equal(t, int64(7), inst.Fields[1].AsInt(), "the int field is unaffected")
}
