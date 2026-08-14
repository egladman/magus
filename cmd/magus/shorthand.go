package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/egladman/magus/cmd/magus/gen"
)

// shorthandName is the de facto short name for magus. Every shipped completion
// script already registers it alongside magus, so the symlink is the only part
// of the shorthand a user still has to set up by hand.
const shorthandName = "mgs"

// installShorthandCmd implements `magus self install-shorthand`: symlink mgs to
// the running binary.
//
// A symlink beats a shell alias because it also resolves in scripts, Makefiles,
// and anything else that is not an interactive shell. It survives an update:
// selfupdate resolves symlinks before it swaps, so the real binary is replaced
// underneath a link that keeps pointing at it.
func installShorthandCmd(args []string) error {
	fs := flag.NewFlagSet("self install-shorthand", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = installShorthandUsage
	sf := gen.BindSelfInstallShorthand(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("magus self install-shorthand: unexpected argument %q", fs.Arg(0))
	}

	target, err := runningBinaryPath()
	if err != nil {
		return err
	}

	name := shorthandName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	linkDir := sf.Dir
	if linkDir == "" {
		linkDir = filepath.Dir(target)
	}
	linkPath := filepath.Join(linkDir, name)

	if _, err := os.Lstat(linkPath); err == nil {
		if resolved, err := filepath.EvalSymlinks(linkPath); err == nil && resolved == target {
			fmt.Printf("%s already points at %s\n", linkPath, target)
			return nil
		}
		if !sf.Force {
			return fmt.Errorf("%s already exists (use --force to replace it)", linkPath)
		}
		if err := os.Remove(linkPath); err != nil {
			return fmt.Errorf("magus self install-shorthand: replace %s: %w", linkPath, err)
		}
	}

	if err := os.Symlink(target, linkPath); err != nil {
		if runtime.GOOS == "windows" {
			return fmt.Errorf("magus self install-shorthand: create %s: %w\n"+
				"  Windows creates symlinks only in Developer Mode or an elevated shell", linkPath, err)
		}
		return fmt.Errorf("magus self install-shorthand: create %s: %w", linkPath, err)
	}
	fmt.Printf("Installed the %s shorthand: %s -> %s\n", shorthandName, linkPath, target)
	return nil
}

// runningBinaryPath returns the real path of the running binary. os.Executable
// reports the as-invoked path on macOS, so running this through an existing mgs
// link would otherwise point the new link at a link. EvalSymlinks pins it to the
// real file. This does not borrow selfupdate.ResolveTargetPath because the
// shorthand has nothing to do with updating, and -tags noselfupdate builds -
// which still get install-shorthand - do not link that package in.
func runningBinaryPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("magus self install-shorthand: resolve executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved, nil
	}
	return exe, nil
}

func installShorthandUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self install-shorthand [--dir DIR] [--force]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Symlink the %s shorthand to the running magus binary.\n", shorthandName)
	fmt.Fprintln(os.Stderr, "Without --dir the link is created next to the binary itself.")
}
