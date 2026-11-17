package settle

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// PreserveBeforeRemoval captures dir's uncommitted work and says so. A
// capture that fails stops the removal, since the work would be lost.
// A missing dir holds no work to capture.
func PreserveBeforeRemoval(ctx context.Context, dir string, res types.VCSResolution) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("vcs checkout --remove: %w", err)
	}
	if _, err := os.Stat(abs); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("vcs checkout --remove: not removing %s, it cannot be read: %w", abs, err)
	}
	cp, captured, err := vcs.PreserveChanged(ctx, abs, res)
	if err != nil {
		return fmt.Errorf("vcs checkout --remove: not removing %s, its work could not be preserved: %w", abs, err)
	}
	switch {
	case captured:
		slog.InfoContext(ctx, fmt.Sprintf("preserved the uncommitted work in %s as %s (%s)", abs, cp.Preserved, cp.VCS), attr.Notice(""))
	case cp.Preserved != "":
		slog.InfoContext(ctx, fmt.Sprintf("the uncommitted work in %s is already preserved as %s (%s)", abs, cp.Preserved, cp.VCS), attr.Notice(""))
	}
	return nil
}

// NewReviewCheckout is a review checkout of the workspace at root that runs
// exe, the running magus, and puts checkouts under $XDG_CACHE_HOME/magus/tmp.
func NewReviewCheckout(root, exe, version string, drv types.VCSDriver) (job.ReviewCheckout, error) {
	cache, err := xdg.CacheDir()
	if err != nil {
		return job.ReviewCheckout{}, fmt.Errorf("vcs checkout: no cache directory for the checkout: %w", err)
	}
	return job.ReviewCheckout{
		Root:     root,
		Base:     filepath.Join(cache, "magus", "tmp"),
		Driver:   drv,
		Installs: installProjectsIn(version),
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
func installProjectsIn(version string) func(ctx context.Context, dir string) ([]string, error) {
	return func(ctx context.Context, dir string) ([]string, error) {
		ws, err := magus.Inspect(ctx, dir, magus.WithVersion(version))
		if err != nil {
			return nil, fmt.Errorf("load the checked-out workspace: %w", err)
		}
		return job.InstallTargets(ws.All()), nil
	}
}

// PrintCheckout is the text reading of rec: the path, what readied it, and
// how to remove it.
func PrintCheckout(w io.Writer, rec job.ReviewCheckoutRecord) {
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
