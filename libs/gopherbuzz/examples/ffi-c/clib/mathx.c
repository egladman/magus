/* mathx.c — a tiny C library demonstrating the three FFI patterns Buzz can
 * drive: a scalar call, pointer out-parameters, and a callback. Compile with:
 *
 *     cc -shared -fPIC -o libmathx.so mathx.c      # Linux
 *     cc -shared -fPIC -o libmathx.dylib mathx.c   # macOS
 *
 * (The runner in main.go does this for you.)
 */

/* Scalar in, scalar out — the simplest case. */
int add(int a, int b) { return a + b; }

/* Out-parameters: results are written through caller-provided int* pointers.
 * The Buzz side allocates the memory with ffi.alloc and reads it back. */
void divmod(int a, int b, int *q, int *r) {
    *q = a / b;
    *r = a % b;
}

/* Callback + pointer array: apply f to each element of xs and sum the results.
 * f arrives as a C function pointer that Buzz supplies via ffi.callback. */
int apply_each(int *xs, int n, int (*f)(int)) {
    int total = 0;
    for (int i = 0; i < n; i++) total += f(xs[i]);
    return total;
}
