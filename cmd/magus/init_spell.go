package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/egladman/magus/internal/scaffold"
)

// initSpellCommand implements `magus init spell <name>`: scaffold a new spell at
// spells/<name>/spell.buzz with the mgs_ contract stubbed, every function
// documented inline, and a runnable test block. It is the authoring on-ramp the
// bundled spells never provided: a new spell starts from a working, tested
// example instead of a blank file and a hunt through docs/spells.md.
func initSpellCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init spell", flag.ContinueOnError)
	bindDisplayFlags(fs)
	force := fs.Bool("force", false, "Overwrite an existing spell.buzz")
	dir := fs.String("dir", "spells", "Parent directory the spell package is created under")
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus init spell <name> [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Scaffold a new spell at <dir>/<name>/spell.buzz with the mgs_ contract")
		fmt.Fprintln(os.Stderr, "stubbed, each function documented inline, and a runnable test block.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("init spell: expected exactly one spell name")
	}
	pkgDir, path, err := scaffold.WriteSpell(ctx, *dir, fs.Arg(0), *force)
	if err != nil {
		return err
	}
	scaffold.PrintSpellNextSteps(ctx, fs.Arg(0), pkgDir, path)
	return nil
}
