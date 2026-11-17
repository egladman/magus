package job

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
)

// Disk is what a fork asks of the disk: the cache package's probes, which this package
// cannot import, handed in by the CLI.
type Disk struct {
	// Below reports the free bytes under dir and whether they are under floorBytes.
	Below func(dir string, floorBytes int64) (free int64, below bool, err error)
	// FullCause names why a build's output says the disk filled, "" when it does not.
	FullCause func(output, dir string) string
	// ReclaimSnippet is the Buzz that reclaims disk in the foreground.
	ReclaimSnippet string
}

// parentCheckout binds a child row forked from outside its parent's checkout to that
// checkout, when the parent's fork made or adopted it, and refuses one whose parent was only
// taken there, naming it. inherited is false when the row has no parent, or the parent works
// here or nowhere yet.
func parentCheckout(ctx context.Context, store *Store, here string, row types.Declaration) (types.Declaration, bool, error) {
	if row.Parent == "" {
		return row, false, nil
	}
	there, bound, err := store.ParentCheckout(here, row.Parent)
	switch {
	case err != nil || there == "":
		return row, false, err
	case !bound:
		return row, false, exit.Usagef("magus job fork: %s's parent %s is held in %s, not here in %s, so its holder could not reach the parent's tree: fork %s from %s, or pass --checkout <dir>",
			row.ID, row.Parent, there, here, row.ID, there)
	}
	slog.InfoContext(ctx, fmt.Sprintf("%s's parent %s is bound to %s, so %s is taken there too", row.ID, row.Parent, there, row.ID), attr.Notice("note"))
	row.Checkout = there
	return row, true, nil
}

// ForkCheckout settles the checkout row is taken in when it names none. A fork run inside a
// checkout a job's fork made adopts that checkout, so the new row's holder is bound there and
// not refused as a sibling. An integration row forked from any other linked checkout is
// refused: its holder merges in a checkout of its own, and the forker's tree is not one.
func ForkCheckout(ctx context.Context, store *Store, root string, row types.Declaration) (types.Declaration, error) {
	if row.Checkout != "" || root == "" {
		return row, nil
	}
	here, err := filepath.Abs(root)
	if err != nil {
		return row, err
	}
	if child, inherited, err := parentCheckout(ctx, store, here, row); inherited || err != nil {
		return child, err
	}
	owned, err := store.OwnedCheckout(here)
	if err != nil {
		return row, err
	}
	if owned != "" {
		// A checkpoint off this head is work in another checkout. Adopting
		// this one would grade it at the wrong commit.
		head, at, err := CheckpointAtHead(ctx, owned, row.Checkpoint)
		switch {
		case err != nil:
			return row, exit.Usagef("magus job fork: whether %s's checkpoint %s is the head of %s cannot be read (%s): pass --checkout <dir>, the checkout the work is in", row.ID, row.Checkpoint, owned, err)
		case !at:
			return row, exit.Usagef("magus job fork: %s names the checkpoint %s, and %s, the checkout a job's fork made that this fork would adopt, stands at %.12s: pass --checkout <dir>, the checkout the work is in", row.ID, row.Checkpoint, owned, head)
		}
		slog.InfoContext(ctx, fmt.Sprintf("the fork runs inside %s, a checkout a job's fork made, so %s is taken there too", owned, row.ID), attr.Notice("note"))
		row.Checkout = owned
		return row, nil
	}
	if len(row.Integrates) == 0 {
		return row, nil
	}
	linked, err := store.LinkedCheckout(ctx, here)
	if err != nil {
		return row, exit.Usagef("magus job fork: whether %s is a linked checkout cannot be read (%s), so the checkout %s merges in is unknown: pass --checkout <dir>", here, err, row.ID)
	}
	if linked {
		return row, exit.Usagef("magus job fork: %s is an integration job forked from %s, the forker's own linked checkout, where its holder would be bound: pass --checkout <dir>, a new directory the fork makes or the linked checkout the integrator runs in", row.ID, here)
	}
	return row, nil
}

// ProvisionCheckout readies a checkout the fork made or moved: it reuses a
// ./magus built at the checkout's base, else the script builds one. It then
// installs only the projects row touches. reused says no build ran.
func ProvisionCheckout(ctx context.Context, store *Store, ws Workspace, disk Disk, exe, forker string, row types.Declaration, wt types.JobWorktree) (reused bool, err error) {
	placed, err := store.PlaceBuiltBinary(ctx, exe, wt.Base, wt.Root)
	if err != nil {
		return false, err
	}
	if placed != "" {
		slog.InfoContext(ctx, fmt.Sprintf("reused %s, already built at %.12s", placed, wt.Base), attr.Notice("note"))
	}
	if ws.Unavailable != "" {
		slog.InfoContext(ctx, fmt.Sprintf("the workspace did not load (%s), so every project's dependencies are installed", ws.Unavailable), attr.Notice("note"))
		return placed != "", bootstrapCheckout(ctx, disk, exe, forker, wt.Root)
	}
	var declared types.Job
	row.Apply(&declared)
	install := InstallProjects(forker, wt.Root, declared, ws.Projects)
	return placed != "", bootstrapCheckout(ctx, disk, exe, forker, wt.Root, "--install", install)
}

// bootstrapCheckout builds ./magus in dir, a checkout the fork made or
// adopted, from dir's own sources. exe runs the forker's copy of its
// checkout.bootstrap hook from forker. Progress goes to stderr, since the
// fork's stdout is its record.
func bootstrapCheckout(ctx context.Context, disk Disk, exe, forker, dir string, flags ...string) error {
	hook, err := CheckoutHook(forker)
	if err != nil {
		return fmt.Errorf("magus job fork: %w", err)
	}
	if hook == "" {
		return fmt.Errorf("magus job fork: %s holds a hook to build its own ./magus, and %s has %s", dir, forker, NoCheckoutHook)
	}
	args := append([]string{"buzz", hook, "--", "--checkout", dir}, flags...)
	cmd := exec.CommandContext(ctx, exe, args...)
	var output bytes.Buffer
	cmd.Dir, cmd.Stdout, cmd.Stderr = forker, progressWriter(ctx), io.MultiWriter(os.Stderr, &output)
	if err := cmd.Run(); err != nil {
		if full := disk.FullCause(output.String(), forker); full != "" {
			return fmt.Errorf("magus job fork: building ./magus in %s failed: %s: %w", dir, full, err)
		}
		return fmt.Errorf("magus job fork: building ./magus in %s from its own sources failed: `%s` from %s says why: %w",
			dir, CheckoutBootstrap(hook, dir), forker, err)
	}
	return nil
}

// progressWriter takes a script's progress lines. -q raises the log level
// past info, so it gets none.
func progressWriter(ctx context.Context) io.Writer {
	if !slog.Default().Enabled(ctx, slog.LevelInfo) {
		return io.Discard
	}
	return os.Stderr
}

// reclaimOnForkTimeout bounds the reclaim a fork waits for below the floor.
const reclaimOnForkTimeout = 5 * time.Minute

// RefuseForkOnFullDisk reclaims in the foreground when a fork making a new
// checkout finds the disk below floorBytes, says so, and refuses if it stays
// below. An adopted checkout already exists and writes nothing new.
func RefuseForkOnFullDisk(ctx context.Context, disk Disk, exe, root, checkout string, floorBytes int64) error {
	if _, err := os.Stat(checkout); err == nil {
		return nil
	}
	free, below, err := disk.Below(root, floorBytes)
	if err != nil {
		return fmt.Errorf("magus job fork: cannot read the disk holding %s, so a new checkout cannot be checked against disk.critical_free_mb: %w", root, err)
	}
	if !below {
		return nil
	}
	slog.InfoContext(ctx, fmt.Sprintf("%d MiB free is below disk.critical_free_mb; reclaiming first, for at most %s", free>>20, reclaimOnForkTimeout), attr.Notice("note"))
	if err := reclaimNow(ctx, disk, exe, root); err != nil {
		slog.InfoContext(ctx, "the reclaim stopped", attr.Error(err), attr.Notice("note"))
	}
	free, below, err = disk.Below(root, floorBytes)
	if err != nil {
		return fmt.Errorf("magus job fork: cannot read the disk holding %s after a reclaim: %w", root, err)
	}
	if !below {
		return nil
	}
	return types.DiagnosticErrorf(types.DiskFull, "%d MiB free is still below disk.critical_free_mb after a reclaim, and a new checkout would fill it. `%s` lists what it left for you", free>>20, types.ReclaimDiskCommand)
}

func reclaimNow(ctx context.Context, disk Disk, exe, root string) error {
	ctx, cancel := context.WithTimeout(ctx, reclaimOnForkTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "buzz", "-C", root, "-e", disk.ReclaimSnippet)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd.Run()
}

// EndCheckouts removes the checkouts the forks of jobs which ended made, restoring what
// magus wrote in them first, and prints what became of each one: removed, or kept and why.
// onlyNamed limits it to the jobs in ended. A dry run changes nothing.
func EndCheckouts(ctx context.Context, out io.Writer, store *Store, written WrittenPaths, dryRun, onlyNamed bool, ended ...string) error {
	if dryRun {
		return nil
	}
	end := store.EndCheckouts
	if onlyNamed {
		end = store.EndNamedCheckouts
	}
	changed, err := end(ctx, written, ended...)
	for _, row := range changed {
		PrintWorktree(out, row, false)
	}
	if err != nil {
		return fmt.Errorf("magus job: recording what became of a job's checkout: %w", err)
	}
	return nil
}
