# FFI reference

gopherbuzz can call into C shared libraries at runtime — no cgo, no build-time
toolchain, no embedded Zig compiler. It is implemented on
[purego](https://github.com/ebitengine/purego), which builds an ABI-correct call
stub (the right registers for ints, floats, and pointers) for a symbol resolved
with `dlopen`/`dlsym`.

A [runnable C example](../examples/ffi-c/) accompanies this reference.

## Relationship to upstream Buzz

Upstream Buzz's FFI is **Zig-ABI native**: `zdef` takes Zig source
(`fn hello(name: [*:0]const u8) void;`, `extern struct { … }`) and
`sizeOf`/`alignOf` are answered by the Zig compiler's comptime reflection
embedded in the runtime. gopherbuzz is pure Go and has no Zig, so it offers the
same *capabilities* expressed **C-ABI-natively**: `zdef` takes C prototypes, and
every type argument to the `ffi` module is a **C type-name string**
(`"int"`, `"double"`, `"char*"`, `"int64_t"`, …) rather than a Zig type.

This is a deliberate, permanent divergence — see [Limitations](#limitations).

## Platform support

`zdef`, `ffi.callback`, and the symbol-binding half of FFI work on the OS/arch
combinations purego supports (darwin, freebsd, netbsd, and linux on
386/amd64/arm/arm64/loong64/ppc64le/riscv64). On any other target — including
WebAssembly — parsing still succeeds but `zdef` returns a clear "unsupported"
error instead of failing to compile. The memory and layout helpers
(`ffi.alloc`/`read`/`write`/`sizeOf`/`structLayout`, …) are pure Go and work
everywhere.

## `zdef(libname, cdecl)` → map

Loads a shared library and binds the C functions declared in `cdecl`, returning a
map of function name → callable.

```buzz
final lib = zdef("libm", "double sqrt(double x);");
final r = lib.sqrt(9.0);   // 3.0
```

`libname` may be a full path or a bare name; a bare name is tried with the usual
`lib<name>.so[.N]` / `lib<name>.dylib` decorations. `cdecl` is one or more C
prototypes separated by `;`.

### Supported C types

| C type(s) | Buzz value | Notes |
|---|---|---|
| `void` | — | return only |
| `bool`, `_Bool` | bool | |
| `char`, `short`, `int`, `long`, `long long`, `intN_t`, `size_t`, … | int | passed/returned as a 64-bit int |
| `unsigned …`, `uintN_t` | int | |
| `float` | float | 32-bit at the boundary |
| `double` | float | |
| `char*`, `const char*` | str | auto null-terminated; a returned `char*` is copied to a Buzz str |
| `void*`, `int*`, `T*`, `struct Foo*` | int | an **opaque machine address** (see below) |

Every non-`char*` pointer — `void*`, `int*`, `struct Foo*`, a function pointer —
is an opaque address represented as an int. You obtain such addresses from
`ffi.alloc` (for memory you own) or `ffi.callback` (for a function pointer), and
pass them straight into the call.

> Note: prior to this support, a `T*` parameter was silently downgraded to the
> pointee scalar `T` — passing a *value* where C expected an *address*. Pointer
> parameters now always marshal as addresses.

## The `ffi` module

```buzz
import "ffi";
```

### Type metadata

| Function | Returns |
|---|---|
| `ffi.sizeOf(ctype: str)` | size of a C type, in bytes |
| `ffi.alignOf(ctype: str)` | alignment of a C type, in bytes |
| `ffi.sizeOfStruct(fields: [str])` | size of a C struct with those field types |
| `ffi.alignOfStruct(fields: [str])` | alignment of that struct |
| `ffi.structLayout(fields: [str])` | `{ size, align, offsets: [int] }` |
| `ffi.cstr(s: str)` | `s` (identity; Buzz strings are already valid `char*` here) |

`structLayout` applies the standard C rule — each field at the next multiple of
its alignment, total size rounded up to the struct alignment — so you can place
each field at its correct offset:

```buzz
// struct { int32_t id; double score; }  ->  size 16, offsets [0, 8]
final lay = ffi.structLayout(["int", "double"]);
```

### Memory

A C function that fills an out-parameter or a struct needs a real, stable address
to write through. `ffi.alloc` provides one by pinning a Go buffer (via
`runtime.Pinner`) so the garbage collector cannot move it; because the pinned
buffer and the C side are the same bytes, whatever C writes is visible when you
read it back.

| Function | Effect |
|---|---|
| `ffi.alloc(size: int)` | reserve `size` zeroed bytes, return their address |
| `ffi.free(addr: int)` | release a block from `ffi.alloc` |
| `ffi.write(addr, offset, ctype, value)` | store a scalar (`value` is float for `float`/`double`, else int) |
| `ffi.read(addr, offset, ctype)` | load a scalar (float for `float`/`double`, else int) |

`read`/`write` are **bounds-checked against the allocation** and only operate on
memory `ffi.alloc` returned. Dereferencing an arbitrary address C handed back
(e.g. a struct pointer C itself owns) is intentionally unsupported — that is the
line between a C ABI we can keep memory-safe and one that can segfault the host.

#### Out-parameter example

```buzz
final lib = zdef("./libmathx.so", "void divmod(int a, int b, int *q, int *r);");
final q = ffi.alloc(ffi.sizeOf("int"));
final r = ffi.alloc(ffi.sizeOf("int"));
lib.divmod(17, 5, q, r);
std.print("{ffi.read(q, 0, \"int\")} rem {ffi.read(r, 0, \"int\")}");  // 3 rem 2
ffi.free(q);
ffi.free(r);
```

#### Struct (by reference)

Structs are passed **by reference**: allocate `sizeOfStruct` bytes, write fields
at their `structLayout` offsets, pass the address to a `T*` parameter, then read
results back. This matches how upstream Buzz passes structs
("always by reference right now") and is portable.

```buzz
final lay = ffi.structLayout(["int", "double"]);
final rec = ffi.alloc(lay["size"]);
lib.rec_init(rec, 7, 9.5);                       // void rec_init(void *r, int id, double score)
final id    = ffi.read(rec, lay["offsets"][0], "int");
final score = ffi.read(rec, lay["offsets"][1], "double");
ffi.free(rec);
```

### Callbacks

`ffi.callback(fn, retType, paramTypes)` wraps a Buzz function as a C function
pointer (returned as an int address) so it can be passed where C expects a
callback. The matching `zdef` parameter is declared `void*`.

```buzz
final lib = zdef("./libmathx.so", "int apply_each(int *xs, int n, void *f);");
fun square(x: int) > int { return x * x; }
final cb = ffi.callback(square, "int", ["int"]);
lib.apply_each(xs, n, cb);
```

The callback ABI carries **integer/pointer/bool arguments and a single
integer/pointer/bool (or `void`) result** — `float`/`double` are rejected. This
covers comparators, predicates, and visitors. A C callback has nowhere to receive
a Buzz error, so if the wrapped function raises, the callback returns its zero
value. Callbacks are never freed (a purego constraint); at least 2000 may exist
per process.

## Limitations

- **No Zig types.** `sizeOf("i32")` etc. is not accepted; use C names (`"int32_t"`).
- **Structs are by reference, not by value.** purego's struct-by-value path is
  gated to darwin/linux amd64/arm64 and would be the only FFI feature that
  compiles but fails on otherwise-supported targets; by-reference is portable and
  matches upstream semantics.
- **No dereference of foreign addresses.** You read/write only memory you
  allocated. A function returning a pointer to memory it owns hands you an int
  address you can pass on, but not directly read through.
- **`long`/`size_t` follow LP64** (pointer width). On LLP64 (Windows) `long` is
  narrower; size it explicitly with `int32_t`/`int64_t` if it matters.
- **C faults are fatal.** A bad pointer or signature mismatch can crash the host
  process; a C segfault is not a recoverable Go panic. Bind-time errors from a
  malformed prototype *are* surfaced as ordinary Buzz errors.

## Embedding on an unsupported platform

`zdef`'s platform boundary is the exported `buzz.FFIProvider` interface. An
embedder targeting a platform purego does not cover can implement it and install
it with `buzz.RegisterFFIProvider` before running any Buzz code; the C-decl
parser, value constructors, and type model are all exported for that purpose.
