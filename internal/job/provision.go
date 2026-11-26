package job

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/job"
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
func parentCheckout(ctx context.Context, store *Store, here string, row job.Declaration) (job.Declaration, bool, error) {
	if row.Parent == "" {
		return row, false, nil
	}
	there, bound, err := store.ParentCheckout(ctx, here, row.Parent)
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
func ForkCheckout(ctx context.Context, store *Store, root string, row job.Declaration) (job.Declaration, error) {
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
	owned, err := store.OwnedCheckout(ctx, here)
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

// ProvisionCheckout readies a checkout a fork made or moved, or exec adopted.
// It reuses a ./magus built at the checkout's base, else the script builds one.
// It then installs only the projects row touches. reused says no build ran.
// The caller runs [RunReadySteps] once the ./magus it pins stands.
func ProvisionCheckout(ctx context.Context, store *Store, ws Workspace, disk Disk, exe, forker string, row job.Job, wt job.Worktree) (reused bool, err error) {
	placed, err := store.PlaceBuiltBinary(ctx, wt.Base, wt.Root, exe, filepath.Join(forker, binaryName))
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
	install := InstallProjects(forker, wt.Root, row, ws.Projects)
	return placed != "", bootstrapCheckout(ctx, disk, exe, forker, wt.Root, "--install", install)
}

// RunReadySteps runs root's own checkout.ready steps in order with root's ./magus, read
// from root's magus.yaml, and returns each step that ran with its output ref. A failing
// step stops the rest and returns a [types.CheckoutReadyStepFailed] error beside the
// steps that passed. No declaration is said once; steps declared in a checkout with no
// ./magus are refused.
func RunReadySteps(ctx context.Context, root string) ([]job.ReadyStep, error) {
	cfg, err := config.LoadWorkspaceOnly(root)
	if err != nil {
		return nil, fmt.Errorf("reading checkout.ready in %s: %w", root, err)
	}
	if len(cfg.Checkout.Ready) == 0 {
		slog.InfoContext(ctx, fmt.Sprintf("%s declares no checkout.ready, so no step readies it", root), attr.Notice("note"))
		return nil, nil
	}
	exe := filepath.Join(root, binaryName)
	if _, err := os.Stat(exe); err != nil {
		return nil, fmt.Errorf("%s declares %d checkout.ready step(s) and holds no ./magus to run them, so none ran: %w", root, len(cfg.Checkout.Ready), err)
	}
	ran := make([]job.ReadyStep, 0, len(cfg.Checkout.Ready))
	for _, step := range cfg.Checkout.Ready {
		slog.InfoContext(ctx, fmt.Sprintf("readying %s: ./magus run %s %s", root, step.Target, step.Project), attr.Notice("note"))
		run, err := runReadyStep(ctx, exe, root, step)
		if err != nil {
			return ran, err
		}
		ran = append(ran, run)
	}
	return ran, nil
}

// readyStepError is a checkout.ready step that failed in Root. It reads and unwraps as
// its [types.CheckoutReadyStepFailed] diagnostic, which unwraps to Err.
type readyStepError struct {
	Root string
	Step config.ReadyStep
	// Ref is the output ref of the failed run, "" when it printed none.
	Ref string
	Err error
}

func (e *readyStepError) diagnostic() *types.DiagnosticError {
	ref := "it printed no output ref"
	if e.Ref != "" {
		ref = fmt.Sprintf("`./magus query output %s` holds its output", e.Ref)
	}
	return types.WrapDiagnostic(types.CheckoutReadyStepFailed, e.Err,
		"the checkout.ready step %[1]s %[2]s failed in %[3]s, so no later step ran (%[4]v): %[5]s, and `./magus run %[1]s %[2]s` reruns it there",
		e.Step.Target, e.Step.Project, e.Root, e.Err, ref)
}

func (e *readyStepError) Error() string { return e.diagnostic().Error() }

func (e *readyStepError) Unwrap() error { return e.diagnostic() }

// readyResult is the part of a `magus run -o jsonl` target result line a ready step
// reads. The report package declares the whole record and imports this one.
type readyResult struct {
	Type   string `json:"type"`
	Target string `json:"target"`
	Status string `json:"status"`
	Ref    string `json:"ref"`
}

// runReadyStep runs step with exe in root. Its ref is the failed target's when one
// failed, else step's own target's.
func runReadyStep(ctx context.Context, exe, root string, step config.ReadyStep) (job.ReadyStep, error) {
	cmd := exec.CommandContext(ctx, exe, "run", step.Target, step.Project, "-o", "jsonl")
	var out bytes.Buffer
	cmd.Dir, cmd.Stdout, cmd.Stderr = root, &out, progressWriter(ctx)
	runErr := cmd.Run()
	run := job.ReadyStep{Target: step.Target, Project: step.Project}
	var failed string
	for line := range bytes.Lines(out.Bytes()) {
		var r readyResult
		if json.Unmarshal(line, &r) != nil || r.Type != readyResultType || r.Ref == "" {
			continue
		}
		if r.Target == step.Target {
			run.Ref = r.Ref
		}
		if r.Status == readyFailed && failed == "" {
			failed = r.Ref
		}
	}
	if runErr == nil {
		return run, nil
	}
	return run, &readyStepError{Root: root, Step: step, Ref: cmp.Or(failed, run.Ref), Err: runErr}
}

// The report package's TypeTargetResult and failed status, which this package cannot import.
const (
	readyResultType = "run.target.result"
	readyFailed     = "failed"
)

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
