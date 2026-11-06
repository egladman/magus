package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsCheckoutCommand implements `magus vcs checkout <rev> [--fetch <ref>]
// [--no-bootstrap]` and `magus vcs checkout --remove <dir>`.
func vcsCheckoutCommand(ctx context.Context, root string, args []string) error {
	var flags *flaggen.VCSCheckoutFlags
	pos, err := commandParse("vcs checkout", args, func(fs *flag.FlagSet) {
		flags = flaggen.BindVCSCheckout(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus vcs checkout <rev> [--fetch <ref>] [--no-bootstrap] | --remove <dir>")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Check out one revision under $XDG_CACHE_HOME/magus/tmp for review, then run")
			fmt.Fprintln(os.Stderr, "the workspace's declared install, harness install and checkout.bootstrap hook.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags:")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	switch {
	case flags.Remove != "" && len(pos) > 0:
		return exit.Usagef("vcs checkout: --remove takes no revision (got %q)", pos[0])
	case flags.Remove == "" && len(pos) != 1:
		return exit.Usagef("vcs checkout: takes exactly one revision, got %d", len(pos))
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return fmt.Errorf("vcs checkout: %w", err)
	}
	if res.VCS == nil {
		return fmt.Errorf("vcs checkout: no version control answers in %s", ws.Root())
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("vcs checkout: locate the running magus: %w", err)
	}
	review, err := newReviewCheckout(ws.Root(), exe, res.VCS)
	if err != nil {
		return err
	}
	if flags.Remove != "" {
		if err := review.Remove(ctx, flags.Remove); err != nil {
			return err
		}
		fmt.Printf("removed %s\n", flags.Remove)
		return nil
	}
	rec, err := review.Checkout(ctx, pos[0], flags.Fetch, !flags.Bootstrap)
	if rec.Path != "" {
		if emitErr := emitCheckout(rec); emitErr != nil {
			return errors.Join(err, emitErr)
		}
	}
	return err
}

// emitCheckout renders rec: the structured formats get the record, the
// terminal its text reading.
func emitCheckout(rec job.ReviewCheckoutRecord) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, rec)
	case outputName:
		return emitNames(os.Stdout, []string{rec.Path})
	}
	printCheckout(os.Stdout, rec)
	return nil
}

// newReviewCheckout is a review checkout of the workspace at root that runs
// exe, the running magus, and puts checkouts under $XDG_CACHE_HOME/magus/tmp.
func newReviewCheckout(root, exe string, drv types.VCSDriver) (job.ReviewCheckout, error) {
	cache, err := xdg.CacheDir()
	if err != nil {
		return job.ReviewCheckout{}, fmt.Errorf("vcs checkout: no cache directory for the checkout: %w", err)
	}
	return job.ReviewCheckout{
		Root:     root,
		Base:     filepath.Join(cache, "magus", "tmp"),
		Driver:   drv,
		Installs: installProjectsIn,
		Magus: func(ctx context.Context, dir string, args ...string) error {
			cmd := exec.CommandContext(ctx, exe, args...)
			// stdout carries the record, so a step's own output goes to stderr.
			cmd.Dir, cmd.Stdout, cmd.Stderr = dir, os.Stderr, os.Stderr
			return cmd.Run()
		},
	}, nil
}

// installProjectsIn loads the workspace checked out at dir, which runs its
// magusfiles, and lists the projects declaring an install target.
func installProjectsIn(ctx context.Context, dir string) ([]string, error) {
	ws, err := magus.Inspect(ctx, dir, magus.WithVersion(version))
	if err != nil {
		return nil, fmt.Errorf("load the checked-out workspace: %w", err)
	}
	return job.InstallTargets(ws.All()), nil
}

// printCheckout is the text reading of rec: the path, what readied it, and
// how to remove it.
func printCheckout(w io.Writer, rec job.ReviewCheckoutRecord) {
	fmt.Fprintf(w, "checked out %s at %s (%s)\n", job.ShortRevision(rec.Revision), rec.Path, rec.VCS)
	b := rec.Bootstrap
	if !b.Ran {
		fmt.Fprintf(w, "bootstrap: %s\n", b.HookNote)
		fmt.Fprintf(w, "remove: %s\n", rec.Remove)
		return
	}
	fmt.Fprintln(w, "bootstrap: only what this workspace declares, after the checkout:")
	if len(b.Install) == 0 {
		fmt.Fprintln(w, "  install: no project declares an install target")
	} else {
		fmt.Fprintf(w, "  install: %s\n", strings.Join(b.Install, ", "))
	}
	if b.Harnesses {
		fmt.Fprintln(w, "  harnesses: magus agent harness install")
	}
	fmt.Fprintf(w, "  hook: %s\n", cmp.Or(b.Hook, b.HookNote))
	fmt.Fprintf(w, "remove: %s\n", rec.Remove)
}
