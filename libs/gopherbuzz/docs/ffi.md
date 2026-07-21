# FFI reference

gopherbuzz calls into C shared libraries at runtime with no cgo, no build-time
toolchain, and no embedded Zig compiler. It runs on
[purego](https://github.com/ebitengine/purego), which builds an ABI-correct call
stub (the right registers for ints, floats, and pointers) for a symbol resolved
with `dlopen`/`dlsym`.

A [runnable C example](../examples/ffi-c/) accompanies this reference.

## Relationship to upstream Buzz

Upstream Buzz's FFI is **Zig-ABI native**: `zdef` takes Zig source and answers
`sizeOf`/`alignOf` through the Zig compiler's comptime reflection embedded in
the runtime. gopherbuzz is pure Go and has no Zig, so it _binds_ C-ABI-natively
through purego. `zdef` accepts **both declaration dialects**, sniffed per
call:

```buzz
zdef("libm", "fn sqrt(x: f64) f64;");      // upstream-Buzz Zig declarations
zdef("libm", "double sqrt(double x);");    // C prototypes
```

The `ffi` module's type-name arguments likewise accept both spellings
(`"i64"`/`"int64_t"`, `"f64"`/`"double"`, `"*anyopaque"`/`"void*"`, ...), and
`extern struct` declarations are lowered to their C layout by gopherbuzz
itself (see below). The remaining divergence is the engine: no embedded Zig
compiler, so comptime-sized Zig types are out of scope and binding follows
the C ABI. See [Limitations](#limitations).

### Zig dialect mapping

| Zig type                                  | FFI kind                                              |
| ----------------------------------------- | ----------------------------------------------------- |
| `bool`, `void`                            | bool / return-only void                               |
| `i8…i64`, `isize`, `c_int`, `c_long`, …   | int                                                   |
| `u8…u64`, `usize`, `c_uint`, …            | int (unsigned)                                        |
| `f32`, `f64`                              | float                                                 |
| `[*:0]const u8`                           | str                                                   |
| `*T`, `?*T`, `[*]T`, `**T`                | opaque address (int)                                  |
| `CGPoint`/`NSPoint`/`CGSize` (return)     | [double] of [x, y]                                    |
| `CGRect`/`NSRect` (return)                | [double] of [x, y, w, h]                              |
| `var name: *anyopaque;`                   | extern data symbol: the loaded pointer                |
| `var name: anyopaque;`                    | extern data symbol: the symbol's own address          |
| `const Name = extern struct { f: T, … };` | binds `Name` to its C layout `{size, align, offsets}` |

A declared struct works as a `*Name` parameter (by reference, upstream's
own struct-passing rule) and, when its fields are exactly two or four `f64`,
as a by-value return (the CGPoint/CGRect register paths). A Zig `extern
struct`'s layout _is_ the C layout by definition, so the portable layout
engine computes it without an embedded Zig compiler. Multiline declaration
blocks read best as backtick raw strings, exactly like upstream:

```buzz
final lib = zdef("sqlite3", `
    const Rec = extern struct { id: c_int, score: f64 };
    fn rec_init(r: *Rec, id: c_int, score: f64) void;
`);
final rec = ffi.alloc(lib.Rec["size"]);
lib.rec_init(rec, 7, 9.5);
```

## Platform support

`zdef`, `ffi.callback`, and the symbol-binding half of FFI work on the OS/arch
combinations purego supports (darwin, freebsd, netbsd, and linux on
386/amd64/arm/arm64/loong64/ppc64le/riscv64). On any other target, including
WebAssembly, parsing still succeeds but `zdef` returns a clear "unsupported"
error instead of failing to compile. The memory and layout helpers
(`ffi.alloc`/`read`/`write`/`sizeOf`/`structLayout`, ...) are pure Go and work
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

### Data symbols (`extern`)

A declaration without parentheses, introduced by `extern`, binds a **data
symbol** instead of a function. The value is resolved and read once at `zdef()`
time and lands in the returned map under the variable's name:

```buzz
final cf = zdef("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
    "extern void *kCFBooleanTrue;"
    + "extern struct __CFDictKeyCBs kCFTypeDictionaryKeyCallBacks;");
cf.kCFBooleanTrue;               // the CFBooleanRef itself (an int address)
cf.kCFTypeDictionaryKeyCallBacks; // the *address of* the struct, for &-style args
```

The declared type picks the binding mode:

| Declared as                                  | Bound value                                    |
| -------------------------------------------- | ---------------------------------------------- |
| `extern void *name` (any non-`char` pointer) | the pointer stored at the symbol               |
| `extern const char *name`                    | that pointer followed to a Buzz str            |
| `extern int name`, `extern double name`, ... | the scalar, loaded at its C width              |
| anything else (`extern struct Foo name`)     | the symbol's own address (what C's `&name` is) |

Real C APIs hide required arguments in global constants. `kCFBooleanTrue` has
no create function, and run-loop modes and dictionary callbacks are exported
variables. With a function-only FFI, you would have to scavenge those values
from other calls' results.

### Supported C types

| C type(s)                                                            | Buzz value | Notes                                                                                                                                         |
| -------------------------------------------------------------------- | ---------- | --------------------------------------------------------------------------------------------------------------------------------------------- |
| `void`                                                               | —          | return only                                                                                                                                   |
| `bool`, `_Bool`                                                      | bool       |                                                                                                                                               |
| `char`, `short`, `int`, `long`, `long long`, `intN_t`, `size_t`, ... | int        | passed/returned as a 64-bit int                                                                                                               |
| `unsigned …`, `uintN_t`                                              | int        |                                                                                                                                               |
| `float`                                                              | float      | 32-bit at the boundary                                                                                                                        |
| `double`                                                             | float      |                                                                                                                                               |
| `char*`, `const char*`                                               | str        | auto null-terminated; a returned `char*` is copied to a Buzz str                                                                              |
| `void*`, `int*`, `T*`, `struct Foo*`                                 | int        | an **opaque machine address** (see below)                                                                                                     |
| `CGPoint`, `NSPoint`, `CGSize`, `NSSize` (return only)               | [double]   | a two-double struct returned by value, e.g. `CGEventGetLocation`; amd64/arm64. As a parameter, declare two `double`s instead (same registers) |

Every non-`char*` pointer (`void*`, `int*`, `struct Foo*`, a function pointer)
is an opaque address represented as an int. You obtain such addresses from
`ffi.alloc` (for memory you own) or `ffi.callback` (for a function pointer), and
pass them straight into the call.

> Note: prior to this support, a `T*` parameter was silently downgraded to the
> pointee scalar `T`, passing a _value_ where C expected an _address_. Pointer
> parameters now always marshal as addresses.

## The `ffi` module

```buzz
import "ffi";
```

### Type metadata

| Function                           | Returns                                                     |
| ---------------------------------- | ----------------------------------------------------------- |
| `ffi.sizeOf(ctype: str)`           | size of a C type, in bytes                                  |
| `ffi.alignOf(ctype: str)`          | alignment of a C type, in bytes                             |
| `ffi.sizeOfStruct(fields: [str])`  | size of a C struct with those field types                   |
| `ffi.alignOfStruct(fields: [str])` | alignment of that struct                                    |
| `ffi.structLayout(fields: [str])`  | `{ size, align, offsets: [int] }`                           |
| `ffi.cstr(s: str)`                 | `s` (identity; Buzz strings are already valid `char*` here) |

`structLayout` applies the standard C rule (each field at the next multiple of
its alignment, total size rounded up to the struct alignment) so you can place
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

| Function                                | Effect                                                           |
| --------------------------------------- | ---------------------------------------------------------------- |
| `ffi.alloc(size: int)`                  | reserve `size` zeroed bytes, return their address                |
| `ffi.free(addr: int)`                   | release a block from `ffi.alloc`                                 |
| `ffi.write(addr, offset, ctype, value)` | store a scalar (`value` is float for `float`/`double`, else int) |
| `ffi.read(addr, offset, ctype)`         | load a scalar (float for `float`/`double`, else int)             |

`read`/`write` are **bounds-checked against the allocation** and only operate on
memory `ffi.alloc` returned. Dereferencing an arbitrary address C handed back
(e.g. a struct pointer C itself owns) is intentionally unsupported. That line
separates a C ABI we can keep memory-safe from one that can segfault the host.

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
integer/pointer/bool (or `void`) result**; `float`/`double` are rejected. This
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
  malformed prototype _are_ surfaced as ordinary Buzz errors.

## Embedding on an unsupported platform

`zdef`'s platform boundary is the exported `buzz.FFIProvider` interface. An
embedder targeting a platform purego does not cover can implement it and install
it with `buzz.RegisterFFIProvider` before running any Buzz code; the C-decl
parser, value constructors, and type model are all exported for that purpose.
