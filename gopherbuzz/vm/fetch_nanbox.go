//go:build !buzz_safe && !buzz_unsafe

package vm

// Default (nanbox) build: Value is a plain uint64 — no heap pointer, no GC
// write barrier. The checked s[i] form is used here: the compiler can prove
// the bounds on the hot-path loads (OpGetLocal etc.) in the same way as the
// safe build, and since Value is pointerless there is no barrier to elide
// anyway. If profiling reveals that bounds checks are measurable, promote to
// the unsafe form with -tags buzz_unsafe (the GC-safety argument from
// fetch_unsafe.go still holds for a pointerless type).
func fetch(code []Instr, ip int) Instr { return code[ip] }
func vget(s []Value, i int) Value      { return s[i] }
