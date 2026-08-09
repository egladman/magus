package main

import (
	"context"
	"fmt"
	"os"
)

// selfCmd is the dispatcher for `magus self <subcommand>`.
//
// This file carries NO build tag, and that is the point. `self` is the noun for
// "things magus does to its own installation", and only the UPDATER is what
// `-tags noselfupdate` removes. The dispatcher used to be written twice, once per
// tag, so every subcommand had to be added to both files and a subcommand that has
// nothing to do with updating still disappeared from the stub if you forgot. The
// tagged files now supply one function each - selfUpdateCmd - and everything else
// lives here and works in both builds.
func selfCmd(ctx context.Context, _ string, args []string) error {
	if len(args) == 0 {
		selfCmdUsage()
		return usagef("magus self: subcommand required (want %s)", selfSubcommands())
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		selfCmdUsage()
		return nil
	case "update":
		return selfUpdateCmd(ctx, rest)
	case "refresh":
		return selfRefreshCmd(ctx, rest)
	case "registry":
		return selfRegistryCmd(ctx, rest)
	case "install-shorthand":
		return installShorthandCmd(rest)
	default:
		selfCmdUsage()
		return usagef("magus self: unknown subcommand %q (want %s)", sub, selfSubcommands())
	}
}

// selfSubcommands names what this build actually offers, so the error a
// noselfupdate binary prints does not advertise a subcommand it will refuse.
func selfSubcommands() string {
	if selfUpdateCompiled {
		return "update, refresh, registry, install-shorthand"
	}
	return "refresh, registry, install-shorthand"
}

func selfCmdUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	if selfUpdateCompiled {
		fmt.Fprintln(os.Stderr, "  update               update magus to the latest release (replaces running binary)")
	} else {
		fmt.Fprintln(os.Stderr, "  update               not available (built with -tags noselfupdate)")
	}
	fmt.Fprintln(os.Stderr, "  refresh              fetch and cache the registry (a data file; does not upgrade magus)")
	fmt.Fprintln(os.Stderr, "  registry             what is cached, how old, and from where")
	fmt.Fprintln(os.Stderr, "  install-shorthand    symlink mgs to the running binary")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "To bootstrap a workspace, use: magus init")
	fmt.Fprintln(os.Stderr, "Run 'magus self <subcommand> --help' for subcommand flags.")
}
