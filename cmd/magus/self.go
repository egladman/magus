package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/types"
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
	default:
		selfCommandUsage()
		return exit.Usagef("magus self: unknown subcommand %q (want %s)", sub, selfSubcommands())
	}
}

// selfSubcommands names what this build actually offers, so the error a
// noselfupdate binary prints does not advertise a subcommand it will refuse.
func selfSubcommands() string {
	if selfUpdateCompiled {
		return "update, install-shorthand, place"
	}
	return "install-shorthand, place"
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
	source := fs.Arg(0)

	repl, err := readPlaceStamp(ctx, source)
	if err != nil {
		return err
	}
	if err := types.MagusBuildFromContext(ctx).AcceptsReplacement(repl); err != nil {
		return fmt.Errorf("magus self place: %w", err)
	}
	target, err := runningBinaryPath()
	if err != nil {
		return err
	}
	if err := placeInto(source, target); err != nil {
		return fmt.Errorf("magus self place: %w", err)
	}
	fmt.Printf("Placed %s (built %s) at %s\n", repl.Commit, repl.BuildDate, target)
	return nil
}

func readPlaceStamp(ctx context.Context, source string) (types.BuildStamp, error) {
	var stamp types.BuildStamp
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, source, "version", "-o", "json")
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return stamp, fmt.Errorf("magus self place: %s did not report its version: %w", source, err)
	}
	if err := json.Unmarshal(out.Bytes(), &stamp); err != nil {
		return stamp, fmt.Errorf("magus self place: %s reported a version this magus cannot read: %w", source, err)
	}
	return stamp, nil
}

func placeInto(source, target string) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(target, body, 0o755)
}

func selfPlaceUsage() {
	fmt.Fprint(os.Stderr, `Usage: magus self place <binary>

Replace the running magus binary with <binary>, which must report a clean
commit stamp newer than the running build. The file is renamed into place,
never overwritten, so macOS does not kill the next exec.
`)
}
