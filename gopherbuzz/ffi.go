package buzz

// FFI public surface for the buzz package.
//
// The implementation lives in magus/buzz/vm (ffi.go, ffi_purego.go).
// This file re-exports the types and functions that form the public API of
// this package, keeping backward compatibility for callers that import buzz
// (not buzz/vm) directly.

import vmpackage "github.com/egladman/gopherbuzz/vm"

// CType is a C type from the zdef() declaration subset.
type CType = vmpackage.CType

// C type constants.
const (
	CVoid        = vmpackage.CVoid
	CBool        = vmpackage.CBool
	CInt         = vmpackage.CInt
	CUint        = vmpackage.CUint
	CFloat       = vmpackage.CFloat
	CDouble      = vmpackage.CDouble
	CCharPtr     = vmpackage.CCharPtr
	CVoidPtr     = vmpackage.CVoidPtr
	CUnsupported = vmpackage.CUnsupported
)

// CParam is one parameter of a C function signature.
type CParam = vmpackage.CParam

// CFuncSig is a parsed C function prototype.
type CFuncSig = vmpackage.CFuncSig

// FFIProvider binds parsed C function signatures from a shared library into
// callable Buzz values.
type FFIProvider = vmpackage.FFIProvider

// RegisterFFIProvider installs p as the FFI backend used by zdef().
var RegisterFFIProvider = vmpackage.RegisterFFIProvider

// GetFFIProvider returns the currently installed FFI provider.
var GetFFIProvider = vmpackage.GetFFIProvider

// SetFFIProvider sets the FFI provider (accepts nil, for tests).
var SetFFIProvider = vmpackage.SetFFIProvider

// ParseCDecls parses one or more C function prototypes separated by semicolons.
var ParseCDecls = vmpackage.ParseCDecls

// FFI memory and C-ABI type metadata, backing the `ffi` std module. These are
// portable (no cgo, no purego) — see vm/ffi_mem.go.
var (
	// CTypeLayout returns the size and alignment in bytes of a C type name.
	CTypeLayout = vmpackage.CTypeLayout
	// StructLayout computes size, alignment, and field offsets of a C struct.
	StructLayout = vmpackage.StructLayout
	// AllocFFI pins n zeroed bytes at a fixed address and returns it.
	AllocFFI = vmpackage.AllocFFI
	// FreeFFI releases a block previously returned by AllocFFI.
	FreeFFI = vmpackage.FreeFFI
	// ReadScalar reads a C scalar from an alloc block at addr+offset.
	ReadScalar = vmpackage.ReadScalar
	// WriteScalar writes a C scalar into an alloc block at addr+offset.
	WriteScalar = vmpackage.WriteScalar
	// MakeCallback wraps a Buzz function as a C function pointer (its address).
	MakeCallback = vmpackage.MakeCallback
)
