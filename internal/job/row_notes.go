package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// ConsoleSkew names a server running a different build from this binary, or "" when they
// match or either side is unstamped. The console that server serves is its own build, so
// what the link opens may not know what this binary just wrote.
func ConsoleSkew(serverVersion, cliVersion string) string {
	if serverVersion == "" || cliVersion == "" || serverVersion == cliVersion {
		return ""
	}
	return fmt.Sprintf("the console serves build %s; this command uses build %s",
		serverVersion, cliVersion)
}

// InflightScope keeps the caller's changes, or every change when all is set. Never nil,
// so an empty scope encodes as [].
func InflightScope(changes []job.InflightChange, all bool) []job.InflightChange {
	if all {
		return append([]job.InflightChange{}, changes...)
	}
	mine := []job.InflightChange{}
	for _, c := range changes {
		if c.Mine {
			mine = append(mine, c)
		}
	}
	return mine
}

// UnfinishedIntegration is the write paths a re-fork of integration row id holds when its
// branches have nothing left to merge: the stored row's, while some goal of it is unmet, so
// a follow-up fixing what the merge left red keeps the row. nil when there is no such row or
// every goal is met.
func UnfinishedIntegration(ctx context.Context, store *Store, row job.Declaration, observe Observer) ([]string, error) {
	rows, err := store.List()
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(rows, func(r job.Job) bool { return r.ID == row.ID })
	if i < 0 || len(rows[i].Integrates) == 0 || len(rows[i].Paths.Write) == 0 {
		return nil, nil
	}
	status, err := GradeGates(ctx, store, row.ID, observe)
	if err != nil {
		return nil, exit.Usagef("magus job fork: %s's goals cannot be graded, so whether it is finished is unknown: %s", row.ID, err)
	}
	if !slices.ContainsFunc(status.Gates, func(g job.VerificationStatus) bool { return !g.Verified }) {
		return nil, nil
	}
	return slices.Clone(rows[i].Paths.Write), nil
}

// ExitedBindingNote tells an exited holder the guard still grades its writes
// by the job's paths. A notice, so stdout stays the record.
const ExitedBindingNote = "You stay bound to its write paths: a write outside them is denied until your orchestrator widens the job."

// UnloadableMagusfile is err, a checkout's failed load, as the one line a kept checkout's
// reason holds: a load failure renders the whole spell, which would bury the checkout's line.
func UnloadableMagusfile(err error) error {
	if diag := (*types.DiagnosticError)(nil); errors.As(err, &diag) {
		return fmt.Errorf("this magus cannot load its magusfile (%s)", string(diag.Code))
	}
	first, _, _ := strings.Cut(err.Error(), "\n")
	return errors.New(first)
}

// NoteForkJudgedAsUpdate says when id is live, so its fork is judged only on
// what it adds. A fork of the same record after the row ends is judged whole.
func NoteForkJudgedAsUpdate(ctx context.Context, store *Store, id string) bool {
	live, err := store.RowLive(id)
	if err != nil {
		slog.InfoContext(ctx, fmt.Sprintf("whether %s is live could not be read", id), attr.Error(err), attr.Notice("note"))
		return false
	}
	if live {
		slog.InfoContext(ctx, fmt.Sprintf("%s is live, so this fork is judged only on what it adds; once it ends, the same record is judged whole", id), attr.Notice("note"))
	}
	return live
}

// NoteForkRowEnded explains a refusal that the validation before the build did
// not list: the row ended meanwhile, so the store write judged it whole.
func NoteForkRowEnded(ctx context.Context, store *Store, id string, wasLive bool) {
	if !wasLive {
		return
	}
	if live, err := store.RowLive(id); err == nil && !live {
		slog.InfoContext(ctx, fmt.Sprintf("%s ended while this fork ran, so it is judged whole as a new job, not as an update", id), attr.Notice("note"))
	}
}

// FailedProvision is err, from readying wt, with what became of wt.
// Only a checkout this fork made is removed. An adopted one stays,
// and so does one an earlier fork of the job made.
func FailedProvision(ctx context.Context, store *Store, wt job.Worktree, made bool, err error) error {
	if !made {
		return fmt.Errorf("%w: %s stays as it was, since this fork did not make it", err, wt.Root)
	}
	if derr := store.DiscardWorktree(ctx, wt); derr != nil {
		return errors.Join(err, fmt.Errorf("removing %s, which this fork made: %w", wt.Root, derr))
	}
	return fmt.Errorf("%w: this fork made %s, so it removed it", err, wt.Root)
}

// RowNotes is what the workspace says about a job's write paths that the row cannot: the
// outputs regenerated outside them, the ci targets over them that nothing the job is
// graded on runs, and a check too long to wait on in the foreground.
type RowNotes struct {
	RegeneratedOutside []RegeneratedOutput
	UnrunGates         []UnrunGate
	ForegroundOverrun  *ForegroundOverrun
}

// RefuseFork refuses the fork with every problem of v listed in full.
func RefuseFork(v types.JobValidation) error {
	var list strings.Builder
	RenderValidation(&list, v, false)
	return listedProblems{list: list.String(), err: v.Error()}
}

// RefuseApply refuses an apply with err and every problem of each of validations listed
// under its job.
func RefuseApply(validations []types.JobValidation, err error) error {
	var list strings.Builder
	for _, v := range validations {
		fmt.Fprintf(&list, "%s:\n", v.Job.ID)
		RenderValidation(&list, v, false)
	}
	return listedProblems{list: list.String(), err: err}
}

// JudgedMerge is row's merge as validation reads it. A job that works in its own checkout
// shares none, which the standing placeholder says before the worktree exists.
func JudgedMerge(row job.Declaration, defaultTimeout time.Duration) func(*job.Job) {
	merge := Declare(row, defaultTimeout)
	if row.Checkout == "" {
		return merge
	}
	return func(u *job.Job) {
		merge(u)
		u.Worktree = &job.Worktree{Root: row.Checkout}
	}
}

// listedProblems is a refusal whose text is its problems as [RenderValidation] lists
// them, so the one error record carries the whole list. It unwraps to the problems.
type listedProblems struct {
	list string
	err  error
}

func (e listedProblems) Error() string { return strings.TrimSuffix(e.list, "\n") }
func (e listedProblems) Unwrap() error { return e.err }
func (listedProblems) ExitCode() int   { return exit.Failure }

// RowNotesWith is row's notes over the workspace at root and its projects.
func RowNotesWith(ctx context.Context, root string, projects []*types.Project, row job.Job, foregroundMax func() (time.Duration, bool)) RowNotes {
	regenerated := RegeneratedOutside(root, row, projects, VCSIgnored(ctx, root))
	return RowNotes{
		RegeneratedOutside: regenerated,
		UnrunGates:         UnrunGates(root, row, projects, regenerated),
		ForegroundOverrun:  CheckOverrun(root, row, foregroundMax),
	}
}

// CheckOverrun is row's check when its last real run here took longer than a wired host
// lets a foreground call wait. The session store is read first, and the harnesses are
// consulted only for a check that has a recorded run.
func CheckOverrun(root string, row job.Job, foregroundMax func() (time.Duration, bool)) *ForegroundOverrun {
	dir, err := sessions.Dir(root)
	if err != nil {
		return nil
	}
	lastRun := func(project, target string) (time.Duration, bool) {
		return sessions.LastRunDuration(dir, project, target)
	}
	overrun, ok := OverrunsForeground(row, lastRun, foregroundMax)
	if !ok {
		return nil
	}
	return &overrun
}

// ForkOutput is `job fork`'s structured output: the row as stored, plus its notes. The
// embedded row flattens, so the wire shape is the job's own with deletion_candidates,
// referenced_outside, referenced_outside_gap, foreground_overrun and claim_notes added.
type ForkOutput struct {
	job.Job `yaml:",inline"`
	// DeletionCandidates are the unreferenced symbols the write paths cover: findings to
	// finish or delete, never goals.
	DeletionCandidates []types.UnreferencedEntry `json:"deletion_candidates,omitempty" yaml:"deletion_candidates,omitempty"`
	// ReferencedOutside are the files outside the write paths that use what they define.
	ReferencedOutside []ReferencedFile `json:"referenced_outside,omitempty" yaml:"referenced_outside,omitempty"`
	// ReferencedOutsideGap says why ReferencedOutside is unknown; empty when a symbol index
	// answered.
	ReferencedOutsideGap string `json:"referenced_outside_gap,omitempty" yaml:"referenced_outside_gap,omitempty"`
	// ForegroundOverrun is the check, when its last real run took longer than a wired host
	// lets a foreground call wait.
	ForegroundOverrun *ForegroundOverrun `json:"foreground_overrun,omitempty" yaml:"foreground_overrun,omitempty"`
	// ClaimNotes are ForkNotes for the row: a claim naming no declaration yet, and the
	// claim pair that would split a file a job in this checkout also writes.
	ClaimNotes []string `json:"claim_notes,omitempty" yaml:"claim_notes,omitempty"`
}
