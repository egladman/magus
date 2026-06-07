package vm

// FFI memory and C-ABI type metadata — the portable half of the ffi std module.
//
// zdef() (ffi.go) lets Buzz *call* C; this file lets Buzz *exchange memory* with
// C without cgo or an embedded Zig compiler. It backs ffi.alloc/free/read*/write*
// and ffi.sizeOf/alignOf/sizeOfStruct/alignOfStruct.
//
// How it stays safe
// ─────────────────
// A C function that fills an out-parameter (e.g. `void stat(const char*, Stat*)`)
// needs a real, stable machine address to write through. We get one without
// leaving Go's GC behind: ffi.alloc pins a Go []byte with runtime.Pinner so its
// backing array cannot move, and hands C the address of byte 0. Because the
// pinned slice and the C side are the *same* bytes, anything C writes is visible
// when Buzz reads the slice back — no copy, no unsafe deref of a foreign address.
//
// read*/write* therefore operate only on memory this package allocated (looked up
// in a registry), using binary.NativeEndian on the retained slice. Dereferencing
// an arbitrary address that C handed back (a returned struct pointer it owns) is
// deliberately *not* supported: that is the line between "C ABI we can make safe"
// and "segfault the host". By-reference structs — the script allocs, C fills,
// the script reads — stay on the safe side of that line and match how upstream
// Buzz passes structs ("always by reference right now").
//
// This file is pure Go and compiles on every target (including wasm); it does not
// depend on purego. Only the symbol-binding half of FFI (ffi_purego.go) is gated.

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"strings"
	"sync"
	"unsafe"
)

// ptrSize is the width of a machine pointer (and of long/size_t on the LP64 unix
// targets purego covers) in bytes.
const ptrSize = int(unsafe.Sizeof(uintptr(0)))

// CTypeLayout returns the size and alignment, in bytes, of a C type named by the
// zdef() subset (the same spellings parseCType accepts) on the current platform.
// ok is false for an unrecognized name. A trailing '*' denotes a pointer of
// machine-pointer width. long/size_t follow the LP64 convention (pointer width);
// on LLP64 (Windows) long is narrower — noted in the ffi reference.
func CTypeLayout(name string) (size, align int, ok bool) {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "const ")
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, "*") {
		return ptrSize, ptrSize, true
	}
	switch name {
	case "bool", "_Bool", "char", "signed char", "unsigned char", "int8_t", "uint8_t":
		return 1, 1, true
	case "short", "short int", "unsigned short", "unsigned short int", "int16_t", "uint16_t":
		return 2, 2, true
	case "int", "signed", "signed int", "unsigned", "unsigned int", "int32_t", "uint32_t":
		return 4, 4, true
	case "long long", "long long int", "unsigned long long", "unsigned long long int", "int64_t", "uint64_t":
		return 8, 8, true
	case "long", "long int", "unsigned long", "unsigned long int",
		"size_t", "ssize_t", "ptrdiff_t", "intptr_t", "uintptr_t":
		return ptrSize, ptrSize, true
	case "float":
		return 4, 4, true
	case "double":
		return 8, 8, true
	default:
		return 0, 0, false
	}
}

// structLayout computes the size, alignment, and per-field byte offsets of a C
// struct whose fields have the given type names, applying the standard C rule:
// each field starts at the next multiple of its alignment, and the struct's size
// is rounded up to its overall alignment. This mirrors what a C compiler lays out
// for a plain `struct { ... }` of scalar/pointer fields, so a Buzz script can
// alloc(size), write each field at its offset, and pass the address to C.
func StructLayout(fieldTypes []string) (size, align int, offsets []int, err error) {
	offsets = make([]int, len(fieldTypes))
	for i, ft := range fieldTypes {
		fsize, falign, ok := CTypeLayout(ft)
		if !ok {
			return 0, 0, nil, fmt.Errorf("buzz: ffi: unknown C type %q in struct field %d", ft, i)
		}
		size = roundUp(size, falign)
		offsets[i] = size
		size += fsize
		if falign > align {
			align = falign
		}
	}
	if align == 0 {
		align = 1
	}
	size = roundUp(size, align)
	return size, align, offsets, nil
}

func roundUp(n, to int) int {
	if to <= 1 {
		return n
	}
	if r := n % to; r != 0 {
		return n + (to - r)
	}
	return n
}

// ---- pinned-memory registry ----

type pinnedBuf struct {
	data []byte
	pin  runtime.Pinner
}

// memRegistry maps the base address of a live ffi.alloc block to its pinned
// backing slice. Guarded by a mutex because, unlike the single-Session operand
// stack, allocations may outlive a call and be touched from another Session's
// goroutine; the lock is off any hot path.
var (
	memMu       sync.Mutex
	memRegistry = map[uintptr]*pinnedBuf{}
)

// AllocFFI reserves n zeroed bytes that stay at a fixed address until FreeFFI,
// returning that address. The address is suitable to pass to a C function
// expecting void*/T* (a zdef pointer parameter). n must be > 0.
func AllocFFI(n int) (uintptr, error) {
	if n <= 0 {
		return 0, fmt.Errorf("buzz: ffi: alloc size must be > 0, got %d", n)
	}
	pb := &pinnedBuf{data: make([]byte, n)}
	pb.pin.Pin(&pb.data[0])
	addr := uintptr(unsafe.Pointer(&pb.data[0]))
	memMu.Lock()
	memRegistry[addr] = pb
	memMu.Unlock()
	return addr, nil
}

// FreeFFI releases a block previously returned by AllocFFI, unpinning its memory.
// Freeing an unknown address is an error (double free or a foreign pointer).
func FreeFFI(addr uintptr) error {
	memMu.Lock()
	defer memMu.Unlock()
	pb, ok := memRegistry[addr]
	if !ok {
		return fmt.Errorf("buzz: ffi: free of unknown address %#x (not from ffi.alloc, or already freed)", addr)
	}
	pb.pin.Unpin()
	delete(memRegistry, addr)
	return nil
}

// slotFor returns the [offset, offset+size) window of the alloc block at addr,
// erroring if addr is not a live allocation or the window is out of bounds. This
// is the bounds check that keeps read*/write* from straying outside memory we own.
func slotFor(addr uintptr, offset, size int) ([]byte, error) {
	if offset < 0 {
		return nil, fmt.Errorf("buzz: ffi: negative offset %d", offset)
	}
	memMu.Lock()
	pb, ok := memRegistry[addr]
	memMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("buzz: ffi: address %#x is not a live ffi.alloc block", addr)
	}
	if offset+size > len(pb.data) {
		return nil, fmt.Errorf("buzz: ffi: read/write at offset %d size %d exceeds %d-byte block", offset, size, len(pb.data))
	}
	return pb.data[offset : offset+size], nil
}

// WriteScalar stores an integer or floating value of the named C type into the
// alloc block at addr+offset, using native byte order. Floating types interpret
// f; integer types interpret i (callers pass whichever the type implies).
func WriteScalar(addr uintptr, offset int, ctype string, i int64, f float64, isFloat bool) error {
	size, _, ok := CTypeLayout(ctype)
	if !ok {
		return fmt.Errorf("buzz: ffi: unknown C type %q", ctype)
	}
	slot, err := slotFor(addr, offset, size)
	if err != nil {
		return err
	}
	switch ctype {
	case "float":
		binary.NativeEndian.PutUint32(slot, math.Float32bits(float32(f)))
	case "double":
		binary.NativeEndian.PutUint64(slot, math.Float64bits(f))
	default:
		u := uint64(i)
		if isFloat {
			u = uint64(int64(f))
		}
		putUintN(slot, u, size)
	}
	return nil
}

// ReadScalar loads a value of the named C type from addr+offset. It returns the
// value as an int64 (integers, sign-extended for signed widths) or float64
// (float/double); isFloat says which the caller should read.
func ReadScalar(addr uintptr, offset int, ctype string) (i int64, f float64, isFloat bool, err error) {
	size, _, ok := CTypeLayout(ctype)
	if !ok {
		return 0, 0, false, fmt.Errorf("buzz: ffi: unknown C type %q", ctype)
	}
	slot, err := slotFor(addr, offset, size)
	if err != nil {
		return 0, 0, false, err
	}
	switch ctype {
	case "float":
		return 0, float64(math.Float32frombits(binary.NativeEndian.Uint32(slot))), true, nil
	case "double":
		return 0, math.Float64frombits(binary.NativeEndian.Uint64(slot)), true, nil
	default:
		u := getUintN(slot, size)
		return signExtend(u, size, isSignedCType(ctype)), 0, false, nil
	}
}

func isSignedCType(name string) bool {
	switch name {
	case "char", "signed char", "short", "short int", "int", "signed", "signed int",
		"long", "long int", "long long", "long long int",
		"int8_t", "int16_t", "int32_t", "int64_t", "ssize_t", "ptrdiff_t", "intptr_t":
		return true
	default:
		return false
	}
}

func putUintN(b []byte, v uint64, n int) {
	switch n {
	case 1:
		b[0] = byte(v)
	case 2:
		binary.NativeEndian.PutUint16(b, uint16(v))
	case 4:
		binary.NativeEndian.PutUint32(b, uint32(v))
	default:
		binary.NativeEndian.PutUint64(b, v)
	}
}

func getUintN(b []byte, n int) uint64 {
	switch n {
	case 1:
		return uint64(b[0])
	case 2:
		return uint64(binary.NativeEndian.Uint16(b))
	case 4:
		return uint64(binary.NativeEndian.Uint32(b))
	default:
		return binary.NativeEndian.Uint64(b)
	}
}

func signExtend(u uint64, size int, signed bool) int64 {
	if !signed || size >= 8 {
		return int64(u)
	}
	bits := uint(size * 8)
	shift := 64 - bits
	return int64(u<<shift) >> shift
}
