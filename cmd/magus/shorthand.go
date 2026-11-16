package main

import (
	"flag"
	"fmt"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/selfupdate"
)

// installShorthandCommand implements `magus self install-shorthand`.
func installShorthandCommand(args []string) error {
	fs := flag.NewFlagSet("self install-shorthand", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.SetOutput(os.Stderr)
	fs.Usage = installShorthandUsage
	sf := flaggen.BindSelfInstallShorthand(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("magus self install-shorthand: unexpected argument %q", fs.Arg(0))
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	got, err := selfupdate.InstallShorthand(sf.Dir, sf.Force)
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitRecord(opts, selfInstallShorthandOutput{Link: got.Link, Target: got.Target, Created: got.Created}, []string{got.Link})
	}
	if !got.Created {
		fmt.Printf("%s already points at %s\n", got.Link, got.Target)
		return nil
	}
	fmt.Printf("Installed the %s shorthand: %s -> %s\n", selfupdate.ShorthandName, got.Link, got.Target)
	return nil
}

type selfInstallShorthandOutput struct {
	Link    string `json:"link"`
	Target  string `json:"target"`
	Created bool   `json:"created"`
}

func installShorthandUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus self install-shorthand [--dir DIR] [--force]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Symlink the %s shorthand to the running magus binary.\n", selfupdate.ShorthandName)
	fmt.Fprintln(os.Stderr, "Without --dir the link is created next to the binary itself.")
}
