package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/selfupdate"
	"github.com/egladman/magus/libs/clikit/exit"
)

// selfCommand is the dispatcher for `magus self <subcommand>`.
//
// This file carries NO build tag, and that is the point. `self` is the noun for
// "things magus does to its own installation", and only the UPDATER is what
// `-tags noselfupdate` removes. The dispatcher used to be written twice, once per
// tag, so every subcommand had to be added to both files and a subcommand that has
// nothing to do with updating still disappeared from the stub if you forgot. The
// tagged files now supply one function each (selfUpdateCommand), and everything else
// lives here and works in both builds.
func selfCommand(ctx context.Context, _ string, args []string) error {
	if len(args) == 0 {
		selfCommandUsage()
		return exit.Usagef("magus self: subcommand required (want %s)", selfSubcommands())
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		selfCommandUsage()
		return nil
	case "update":
		return selfUpdateCommand(ctx, rest)
	case "install-shorthand":
		return installShorthandCommand(rest)
	case "place":
		return selfPlaceCommand(ctx, rest)
	case "completion":
		return completion(rest)
	case "man":
		return manCommand(rest)
	default:
		selfCommandUsage()
		return exit.Usagef("magus self: unknown subcommand %q (want %s)", sub, selfSubcommands())
	}
}

// selfSubcommands names what this build actually offers, so the error a
// noselfupdate binary prints does not advertise a subcommand it will refuse.
func selfSubcommands() string {
	if selfUpdateCompiled {
		return "update, install-shorthand, place, completion, man"
	}
	return "install-shorthand, place, completion, man"
}

func selfCommandUsage() {
	update := "update magus to the latest release (replaces running binary)"
	if !selfUpdateCompiled {
		update = "not available (built with -tags noselfupdate)"
	}
	fmt.Fprintf(os.Stderr, `Usage: magus self <subcommand> [flags]

Subcommands:
  update               %s
  install-shorthand    symlink mgs to the running binary
  place                replace the running binary with a newer stamped build
  completion           print a bash, zsh, fish or powershell completion script
  man                  install the man pages embedded in this binary

To bootstrap a workspace, use: magus init
Run 'magus self <subcommand> --help' for subcommand flags.
`, update)
}

// selfPlaceCommand implements `magus self place <binary>`. The target gets a new inode:
// on macOS an in-place rewrite leaves a stale code signature and the next exec is killed.
func selfPlaceCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self place", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = selfPlaceUsage
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("magus self place: want one binary to place, got %d arguments", fs.NArg())
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	placed, err := selfupdate.Place(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, selfPlaceOutput{Commit: placed.Commit, BuildDate: placed.BuildDate, Path: placed.Path}, []string{placed.Path})
	}
	fmt.Printf("Placed %s (built %s) at %s\n", placed.Commit, placed.BuildDate, placed.Path)
	return nil
}

type selfPlaceOutput struct {
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	Path      string `json:"path"`
}

func selfPlaceUsage() {
	fmt.Fprint(os.Stderr, `Usage: magus self place <binary>

Replace the running magus binary with <binary>, which must report a clean
commit stamp newer than the running build. The file is renamed into place,
never overwritten, so macOS does not kill the next exec.
`)
}
