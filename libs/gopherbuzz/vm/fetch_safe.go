//go:build buzz_safe

package vm

// fetch returns code[ip] with the normal bounds check. This is the verification
// twin of the unsafe pointer-indexed fetch in fetch_unsafe.go: under
// -tags buzz_safe a chunk that somehow violated the "every chunk ends in a
// terminating return" invariant panics with a clear index-out-of-range here,
// rather than the unsafe build reading a stray element. See fetch_unsafe.go for
// the full invariant and safety argument.
func fetch(code []Instr, ip int) Instr { return code[ip] }

// vget returns s[i] with the normal bounds check — the verification twin of the
// unchecked load in fetch_unsafe.go. Under -tags buzz_safe a violated load
// invariant panics with index-out-of-range here. See fetch_unsafe.go.
func vget(s []Value, i int) Value { return s[i] }
