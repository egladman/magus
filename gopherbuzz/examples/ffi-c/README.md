# C FFI example

A runnable demonstration of calling C from Buzz with `zdef()` and the `ffi`
module — no cgo, no Zig.

```sh
go run .
```

```
add(40, 2) = 42
divmod(17, 5) = 3 rem 2
sum of squares 1..4 = 30
```

Requires a C compiler (`cc`/`clang`/`gcc`) on `PATH` and a
[purego](https://github.com/ebitengine/purego)-supported platform. `main.go`
compiles [`clib/mathx.c`](clib/mathx.c) into a shared library and runs
[`demo.buzz`](demo.buzz) against it.

## What it shows

| Pattern | C signature | Buzz |
|---|---|---|
| Scalar call | `int add(int, int)` | `lib.add(40, 2)` |
| Out-parameters | `void divmod(int, int, int*, int*)` | `ffi.alloc` → call → `ffi.read` |
| Callback + pointer array | `int apply_each(int*, int, int(*)(int))` | `ffi.callback(square, "int", ["int"])` |

Pointer parameters (`int *q`, `int (*f)(int)`) are declared in the `zdef` string
the same way you would in C; Buzz treats every non-`char*` pointer as an opaque
machine address. The script owns the memory it passes to C: `ffi.alloc` pins a
buffer, hands C its address, and `ffi.read`/`ffi.write` move scalars in and out.
See [`../../docs/ffi.md`](../../docs/ffi.md) for the full reference.
