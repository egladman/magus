//go:build !buzz_safe && !buzz_unsafe

// cross-cutting: a build-tag constant for bytecode_test.go; the tag split needs two files

package buzz

// jitTagged: see the companion file for the buzz_safe/buzz_unsafe case.
const jitTagged = false
