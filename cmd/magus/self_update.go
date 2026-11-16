//go:build !noselfupdate

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/selfupdate"
	"github.com/egladman/magus/libs/termkit"
)

// confirmInstall asks the caller to approve an install unless yes is set, and
// returns a non-nil error when they declined or when stdin is not a terminal
// and --yes was not given: a script piping this command must see a nonzero
// exit whenever nothing was installed, not the exit code of a prompt that
// silently returned nil after "aborted".
func confirmInstall(out io.Writer, in io.Reader, isTerminal, yes bool, version, host, targetPath string) error {
	if yes {
		return nil
	}
	if !isTerminal {
		return fmt.Errorf("non-interactive terminal: use --%s (-%s) to confirm the update", flaggen.FlagSelfUpdateYes, flaggen.FlagSelfUpdateY)
	}
	fmt.Fprintf(out, "Install magus %s from %s -> %s? [y/N] ", version, host, targetPath)
	var answer string
	_, scanErr := fmt.Fscanln(in, &answer)
	if scanErr != nil || strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return fmt.Errorf("declined: magus %s was not installed", version)
	}
	return nil
}

// selfUpdateCompiled is true when the binary includes self-update support
// (the default; disable with -tags noselfupdate), enabling `self update`.
const selfUpdateCompiled = true

// selfUpdateCommand implements `magus self update`; selfupdate.Update holds the flow.
func selfUpdateCommand(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("self update", flag.ContinueOnError)
	bindDisplayFlags(fs)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: magus self update [flags]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Download the latest stable magus release, verify its Ed25519 signature and")
		fmt.Fprintln(os.Stderr, "SHA-256 hash, then atomically replace the running binary.")
		fmt.Fprintln(os.Stderr, "Without --bin-dir the running binary is replaced in place.")
		fmt.Fprintln(os.Stderr, "A prerelease (v0.5.0-rc.1) is never chosen automatically; name it with --version.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Discovery reads the release index at the site's public/release/index.json.")
		fmt.Fprintln(os.Stderr, "Override with MAGUS_UPDATE_URL to use a private update channel.")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "Flags:")
		printOwnDefaults(fs)
	}
	// -y and --yes are one switch, which the registry expresses with AliasOf.
	uf := flaggen.BindSelfUpdate(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	format, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	structured := format.Format != outputText

	prompt := io.Writer(os.Stdout)
	if structured {
		prompt = os.Stderr
	}
	res, err := selfupdate.Update(ctx, selfupdate.Request{
		Current: version,
		Version: uf.Version,
		BinDir:  uf.BinDir,
		Check:   uf.Check,
		DryRun:  uf.DryRun,
		Force:   uf.Force,
		Confirm: func(release, host, target string) error {
			return confirmInstall(prompt, os.Stdin, termkit.StdinIsTerminal(), uf.Yes, release, host, target)
		},
	})
	if err != nil {
		return err
	}
	if structured {
		return emitRecord(format, selfUpdateOutput{Current: res.Current, Latest: res.Latest, Path: res.Path, Outcome: res.Outcome}, []string{res.Latest})
	}
	switch res.Outcome {
	case selfupdate.OutcomeChecked:
		selfupdate.PrintUpdateStatus(res.Latest, res.Current)
	case selfupdate.OutcomeDryRun:
		fmt.Printf("dry-run: would install magus %s -> %s\n", res.Latest, res.Path)
	default:
		fmt.Printf("magus %s installed to %s\n", res.Latest, res.Path)
	}
	return nil
}

type selfUpdateOutput struct {
	Current string `json:"current"`
	Latest  string `json:"latest"`
	Path    string `json:"path,omitempty"`
	Outcome string `json:"outcome"`
}
