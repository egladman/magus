//go:build !selfmanage

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// selfManageCompiled is false when the binary was built without -tags
// selfmanage: `self update`/`self install` are unavailable (only `self init`).
const selfManageCompiled = false

func selfCmd(_ context.Context, _ string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			selfCmdUsage()
			return nil
		}
	}
	fs := flag.NewFlagSet("self", flag.ContinueOnError)
	fs.Usage = func() {}
	_ = fs.Parse(args)
	return errors.New("magus was compiled without self-manage support; rebuild with -tags selfmanage to enable")
}

func selfCmdUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self <update|install> [flags]")
	fmt.Fprintln(os.Stderr, "  update, install: not available (magus was compiled without -tags selfmanage)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To bootstrap a workspace, use: magus init")
}
