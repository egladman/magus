//go:build buzz_safe || buzz_unsafe

// cross-cutting: a build-tag constant for bytecode_test.go; the tag split needs two files

package buzz

// jitTagged reports whether an alternate Value representation is selected, which
// deliberately withholds the JIT backends - see the !buzz_safe && !buzz_unsafe
// clauses on vm/jit_{amd64,arm64}.go.
const jitTagged = true
