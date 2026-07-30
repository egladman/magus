//go:build noselfupdate

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
)

// selfUpdateCompiled is false when the binary was built with -tags
// noselfupdate: `self update` is unavailable.
const selfUpdateCompiled = false

func selfCmd(_ context.Context, _ string, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help", "help":
			selfCmdUsage()
			return nil
		case "install-shorthand":
			// install-shorthand only symlinks mgs to the binary already on disk,
			// so it stays available in builds that compiled the updater out.
			return installShorthandCmd(args[1:])
		}
	}
	fs := flag.NewFlagSet("self", flag.ContinueOnError)
	fs.Usage = func() {}
	_ = fs.Parse(args)
	return errors.New("magus was compiled without self-update support; rebuild without -tags noselfupdate to enable")
}

func selfCmdUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "  update:            not available (magus was compiled with -tags noselfupdate)")
	fmt.Fprintln(os.Stderr, "  install-shorthand: symlink mgs to the running binary")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To bootstrap a workspace, use: magus init")
}
