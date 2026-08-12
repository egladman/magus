//go:build wasm

package std

import (
	"context"
	"fmt"
)

// checkRead and checkWrite are the sandbox gates every filesystem-touching Impl
// calls before doing anything. On the native build they live in fs.go and consult
// the policy on ctx; that file is //go:build !wasm, so the wasm build needs its own.
//
// They REFUSE rather than allow, and the reason is not that the sandbox denies it -
// there is no policy in a browser - but that there is no filesystem to reach at all.
// Returning nil would be defensible and is worse in practice: the call would sail
// through the gate and fail deeper in, as a raw Go error about a path that could
// never have existed, from a stack that says nothing about the browser.
//
// Failing here keeps a WASM-capable module honest. `crypto` is registered in the
// playground and carries sha256File/signFile; those now report what is actually
// wrong the moment they are called, in the wording the rest of the embedding uses
// (see gopherbuzz/std's unsupported()).
//
// This is deliberately NOT a types.DiagnosticErrorf: MGS2001/MGS2002 mean "the
// sandbox denied a path", and reusing them here would tell a browser user to adjust
// a policy that is not in play.
func checkRead(_ context.Context, path string) error {
	return errNoFilesystem("read", path)
}

func checkWrite(_ context.Context, path string) error {
	return errNoFilesystem("write", path)
}

func errNoFilesystem(op, path string) error {
	return fmt.Errorf("fs %s %q: not supported in the magus/buzz embedding: the wasm build has no filesystem", op, path)
}
