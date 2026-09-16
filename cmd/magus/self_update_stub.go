//go:build noselfupdate

package main

import (
	"context"
	"errors"
	"flag"
)

// selfUpdateCompiled is false when the binary was built with -tags
// noselfupdate: `self update` is unavailable. Everything else under the `self`
// noun still is - see self.go.
const selfUpdateCompiled = false

func selfUpdateCmd(_ context.Context, args []string) error {
	fs := flag.NewFlagSet("self update", flag.ContinueOnError)
	// Bound even though this build errors out regardless: a caller passing -s
	// should get the real "compiled without self-update support" message, not a
	// flag-parse error that hides it.
	bindDisplayFlags(fs)
	fs.Usage = func() {}
	_ = fs.Parse(args)
	return errors.New("magus was compiled without self-update support; rebuild without -tags noselfupdate to enable")
}
