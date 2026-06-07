package vm

import "testing"

func TestCTypeLayout(t *testing.T) {
	cases := []struct {
		name        string
		size, align int
	}{
		{"bool", 1, 1},
		{"char", 1, 1},
		{"int8_t", 1, 1},
		{"short", 2, 2},
		{"int", 4, 4},
		{"unsigned int", 4, 4},
		{"int64_t", 8, 8},
		{"long long", 8, 8},
		{"float", 4, 4},
		{"double", 8, 8},
		{"char*", ptrSize, ptrSize},
		{"void*", ptrSize, ptrSize},
		{"int*", ptrSize, ptrSize},
		{"const char *", ptrSize, ptrSize},
	}
	for _, c := range cases {
		size, align, ok := CTypeLayout(c.name)
		if !ok {
			t.Errorf("CTypeLayout(%q) ok=false", c.name)
			continue
		}
		if size != c.size || align != c.align {
			t.Errorf("CTypeLayout(%q) = (%d,%d), want (%d,%d)", c.name, size, align, c.size, c.align)
		}
	}
	if _, _, ok := CTypeLayout("frobnicate"); ok {
		t.Error("CTypeLayout(unknown) ok=true, want false")
	}
}

func TestStructLayout(t *testing.T) {
	// struct { int32 id; double score } -> [0, 8], size 16, align 8.
	size, align, offsets, err := StructLayout([]string{"int", "double"})
	if err != nil {
		t.Fatal(err)
	}
	if size != 16 || align != 8 {
		t.Errorf("size,align = %d,%d want 16,8", size, align)
	}
	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 8 {
		t.Errorf("offsets = %v want [0 8]", offsets)
	}

	// struct { char; int } -> char at 0, int at 4 (3 bytes pad), size 8.
	size, _, offsets, err = StructLayout([]string{"char", "int"})
	if err != nil {
		t.Fatal(err)
	}
	if offsets[1] != 4 || size != 8 {
		t.Errorf("char,int layout offsets=%v size=%d, want [0 4] size 8", offsets, size)
	}

	if _, _, _, err := StructLayout([]string{"int", "bogus"}); err == nil {
		t.Error("StructLayout with bad field type: want error")
	}
}

func TestAllocReadWriteRoundTrip(t *testing.T) {
	addr, err := AllocFFI(16)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = FreeFFI(addr) }()

	if err := WriteScalar(addr, 0, "int", 0x7fffffff, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := WriteScalar(addr, 8, "double", 0, 2.71828, true); err != nil {
		t.Fatal(err)
	}
	i, _, _, err := ReadScalar(addr, 0, "int")
	if err != nil || i != 0x7fffffff {
		t.Errorf("read int = %d (err %v), want %d", i, err, 0x7fffffff)
	}
	_, f, isF, err := ReadScalar(addr, 8, "double")
	if err != nil || !isF || f != 2.71828 {
		t.Errorf("read double = %v (isFloat %v, err %v), want 2.71828", f, isF, err)
	}
}

func TestReadScalarSignExtension(t *testing.T) {
	addr, err := AllocFFI(8)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = FreeFFI(addr) }()

	// Store -1 as int8 (0xFF) and confirm signed/unsigned reads differ.
	if err := WriteScalar(addr, 0, "int8_t", -1, 0, false); err != nil {
		t.Fatal(err)
	}
	if i, _, _, _ := ReadScalar(addr, 0, "int8_t"); i != -1 {
		t.Errorf("signed int8_t read = %d, want -1", i)
	}
	if i, _, _, _ := ReadScalar(addr, 0, "uint8_t"); i != 255 {
		t.Errorf("unsigned uint8_t read = %d, want 255", i)
	}
}

func TestMemBoundsAndFreeErrors(t *testing.T) {
	addr, err := AllocFFI(4)
	if err != nil {
		t.Fatal(err)
	}
	// Out-of-bounds write is rejected, not a segfault.
	if err := WriteScalar(addr, 0, "double", 0, 1, true); err == nil {
		t.Error("OOB write: want error")
	}
	// Reading a never-allocated address is rejected.
	if _, _, _, err := ReadScalar(addr+1<<20, 0, "int"); err == nil {
		t.Error("read of foreign address: want error")
	}
	if err := FreeFFI(addr); err != nil {
		t.Fatalf("first free: %v", err)
	}
	// Double free is an error, not a panic.
	if err := FreeFFI(addr); err == nil {
		t.Error("double free: want error")
	}
	if _, err := AllocFFI(0); err == nil {
		t.Error("alloc(0): want error")
	}
}
