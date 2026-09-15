package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// serverCheckDrift is the worker for the check-drift job: it notices, without blocking
// anything, that HEAD left something stale (generated output, formatting, or both) and
// hands over the commands that fix it in place. Normally reached via `magus job run
// check-drift`, which is all the post-commit and pre-push hooks installed by
// installDriftHooks ever call; see that function and types.DriftHookInstaller for why
// the hook itself does none of this work.
//
// Two classes, two different cheapness stories:
//
//   - generated output: the same rule this repo already states as policy (regenerate in
//     the same commit as the source change that invalidated the output), read back as a
//     question: did HEAD change a declared SOURCE without also changing that project's
//     declared OUTPUT. Pure glob/diff comparison, no execution. Reuses MGS4006
//     (types.StaleGeneratedOutput), the code this exact condition means everywhere else
//     it fires.
//   - formatting: charmless `format` was checked first and does not answer this: its
//     ctx.modifiesExistingFiles globs are deliberately excluded from the drift-gate's
//     declared-output hash (see MGS4007's docs on why: "so nothing snapshots, replays, or
//     cleans them"), so nothing there ever compares pre/post bytes for them. This class
//     runs `gofmt -l`, read-only, scoped to exactly the commit's changed Go files that
//     format's own declaration governs (types.ChainUpdates, itself read-time, no
//     execution, narrows the file list before gofmt ever runs). Reuses MGS4009
//     (types.UnformattedCommit): a sibling of MGS4006, not a duplicate of lint's own
//     formatting check: lint answers "is this file formatted, right now, anywhere in
//     the tree"; this answers "did this commit leave a file it touched unformatted".
func serverCheckDrift(ctx context.Context, root string, args []string) error {
	if _, err := cmdParse("server "+job.NameCheckDrift, args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server check-drift")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Notice when HEAD left generated output or formatting stale. This is the")
			fmt.Fprintln(os.Stderr, "worker for `"+hint.JobRun.With(job.NameCheckDrift)+"`; prefer that form.")
		}
	}); err != nil {
		return err
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameCheckDrift, err)
	}
	res, err := vcs.Resolve(ctx, m.Root(), "", m.VCSOptions())
	if err != nil {
		return fmt.Errorf("server %s: %w", job.NameCheckDrift, err)
	}
	if res.VCS == nil || res.Source == types.VCSSourceDefault {
		return nil // unversioned tree: nothing to notice
	}
	// The root project's OWN format target, read-time only: its ctx.modifiesExistingFiles
	// declaration walked through ctx.needs (types.ChainUpdates), same as the runner folds
	// it into a real run's Step.Updates. A workspace with no "format" target (or none at
	// root) yields an empty set, and the formatting class simply never fires: no error,
	// no gofmt call.
	var formatGlobs []string
	if p := m.Get("."); p != nil {
		formatGlobs = types.ChainUpdates(p, "format", m.Get)
	}
	notice, ok, err := checkDriftForCommit(ctx, m.Root(), res.VCS, m.ClassifyFiles, formatGlobs, realGofmtList)
	if err != nil {
		slog.WarnContext(ctx, "server check-drift: could not check HEAD for stale output", slog.String("error", err.Error()))
		// Best-effort: a broken check must not be mistaken for a failed commit or push.
		return nil
	}
	if !ok {
		return nil
	}
	fmt.Fprintln(os.Stderr, notice)
	noteDriftDesktop(ctx, notice)
	return nil
}

// checkDriftForCommit is the VCS-facing half of serverCheckDrift, kept separate so it can
// be exercised against a real repository without needing a full magus workspace:
// classify and gofmtList are the two things that need one (turning changed paths into
// their declared source/output role, and asking a real gofmt binary about a file), and a
// test supplies its own instead of loading a workspace or shelling a real tool.
//
// It returns ok=false, with no error, for every ordinary reason there is nothing to say:
// no parent commit (the repository's first commit), nothing changed, or nothing that
// changed lands in either class.
func checkDriftForCommit(
	ctx context.Context,
	root string,
	driver types.VCSDriver,
	classify func(context.Context, []string) ([]types.FileEntry, error),
	formatGlobs []string,
	gofmtList func(ctx context.Context, root string, files []string) ([]string, error),
) (notice string, ok bool, err error) {
	before, err := driver.Metadata(ctx, root)
	if err != nil {
		return "", false, fmt.Errorf("vcs metadata: %w", err)
	}
	commitID := before.ID

	paths, err := driver.ChangedFiles(ctx, root, driver.ParentRef())
	if err != nil {
		// No parent (the first commit in the repository) is the ordinary case this
		// covers; a real VCS failure looks the same from here, so either way there is
		// nothing safe to say about drift.
		return "", false, nil //nolint:nilerr // see above
	}
	if len(paths) == 0 {
		return "", false, nil
	}

	files, err := classify(ctx, paths)
	if err != nil {
		return "", false, fmt.Errorf("classify changed files: %w", err)
	}
	finding := driftFinding{staleProjects: types.StaleSourceProjects(files)}

	if goFiles := formatGovernedGoFiles(paths, formatGlobs); len(goFiles) > 0 {
		unformatted, gerr := gofmtList(ctx, root, goFiles)
		if gerr != nil {
			// Best-effort, same posture as the desktop notification: a broken gofmt
			// probe must not hide a real generated-output finding and must not fail
			// the job. It is logged by the caller if this bubbles up, but here it just
			// means the formatting class stays silent for this run.
			slog.WarnContext(ctx, "check-drift: gofmt probe failed", slog.String("error", gerr.Error()))
		} else {
			finding.unformatted = unformatted
		}
	}

	if finding.empty() {
		return "", false, nil
	}

	kase := driftPushed
	if pr, isReporter := driver.(types.PushStatusReporter); isReporter {
		if pushed, known, perr := pr.CommitPushed(ctx, root, commitID); perr == nil && known && !pushed {
			after, merr := driver.Metadata(ctx, root)
			stillHead := merr == nil && after.ID == commitID
			if stillHead {
				kase = driftUnpushedHead
			} else {
				kase = driftUnpushedNotHead
			}
		}
		// perr != nil, or known == false: the reporter could not tell. kase stays
		// driftPushed, the safe assumption types.PushStatusReporter documents.
	}

	return buildDriftNotice(commitID, finding, kase), true, nil
}

// formatGovernedGoFiles narrows paths to the commit's changed Go files that the format
// target's own ctx.modifiesExistingFiles declares governed (updateGlobs, from
// types.ChainUpdates). Go only: dprint/Markdown formatting is a real gap this pass does
// not close (see the report), and narrowing first is what keeps gofmt -l from ever
// running when nothing changed calls for it.
func formatGovernedGoFiles(paths, updateGlobs []string) []string {
	if len(updateGlobs) == 0 {
		return nil
	}
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") && types.MatchesAnyGlob(updateGlobs, p) {
			out = append(out, p)
		}
	}
	return out
}

// realGofmtList runs `gofmt -l`, read-only (no -w), against exactly the given files:
// never the whole tree, and never a write, so it cannot race a concurrent agent's edits
// the way a formatting write could. It is the one live check checkDriftForCommit
// performs; everything else is decided from a diff and a glob comparison.
func realGofmtList(ctx context.Context, root string, files []string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "gofmt", append([]string{"-l"}, files...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gofmt -l: %w", err)
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return nil, nil
	}
	return strings.Split(trimmed, "\n"), nil
}

// noteDriftDesktop raises a best-effort desktop notification carrying the same notice
// already printed to stderr. The stderr line is the durable, testable record (job output
// a dashboard or run log shows); the desktop alert is what makes "at the moment it
// happens" true for a job the daemon runs out of band, matching how `magus session
// notify --desktop` already tells a person something without touching their terminal.
// Never fails the job: a missing notifier (no osascript, no notify-send) is exactly the
// case raiseDesktopNotification already swallows.
func noteDriftDesktop(ctx context.Context, notice string) {
	ev := types.Event{
		Message: notice,
		Outcome: types.OutcomeDiagnostic,
		Source:  types.EventSource{Kind: "magus", Sub: job.NameCheckDrift},
	}
	normalizeEvent(&ev)
	_ = raiseDesktopNotification(ctx, ev)
}

// driftNoticeCase names which of the three amend-safety branches a stale commit falls
// into (see serverCheckDrift): which VCS command, if any, safely folds a fix into the
// commit that caused it to drift.
//
// The fork is never guessed at: a caller that cannot answer "is this commit pushed"
// treats that the same as driftPushed (see types.PushStatusReporter), because the one
// unrecoverable mistake here is rewriting history that already left the repository.
type driftNoticeCase int

const (
	// driftUnpushedHead: the commit is unpublished and still the tip. Amending it is
	// unambiguous and safe.
	driftUnpushedHead driftNoticeCase = iota
	// driftUnpushedNotHead: the commit is unpublished but something now sits on top of
	// it. A plain amend would rewrite the wrong commit (HEAD, not the drifted one); a
	// fixup targeted at its hash, folded with an autosquash rebase, rewrites the right
	// one.
	driftUnpushedNotHead
	// driftPushed: the commit already left the repository (or its status could not be
	// determined; see types.PushStatusReporter). No rewrite is offered.
	driftPushed
)

// driftFinding is everything checkDriftForCommit learned about one commit, in the two
// classes the notice distinguishes. Either may be empty; buildDriftNotice omits an empty
// class's paragraph rather than printing it hollow.
type driftFinding struct {
	// staleProjects: MGS4006 (types.StaleGeneratedOutput): a declared source changed
	// with no matching declared-output change in the same commit.
	staleProjects []string
	// unformatted: files gofmt -l reported, scoped to the commit's changed Go files
	// that the format target's own ctx.modifiesExistingFiles declares governed (see
	// checkDriftForCommit). MGS4009 (types.UnformattedCommit): a sibling of MGS4006,
	// not a duplicate of lint's own formatting check: this asks whether THIS COMMIT
	// left a file it touched unformatted, not whether a file is formatted right now.
	unformatted []string
}

func (f driftFinding) empty() bool { return len(f.staleProjects) == 0 && len(f.unformatted) == 0 }

// buildDriftNotice renders the notice for a commit that left something stale: one
// paragraph per class that actually fired (generated output, formatting), each with its
// own remedy command, followed by the ONE VCS command (if any) that folds a fix into
// hash, the commit that caused it. The amend-safety decision is per COMMIT, not per
// class, so it appears once regardless of how many classes fired.
//
// Pure: no VCS or filesystem call happens here, so both the findings and the
// amend-safety case must already be decided by the caller. This keeps the exact
// wordings testable without a real repository or a real gofmt.
func buildDriftNotice(hash string, f driftFinding, kase driftNoticeCase) string {
	var b strings.Builder
	if len(f.staleProjects) > 0 {
		projects := joinProjects(f.staleProjects)
		fmt.Fprintf(&b, "[%s] commit %s left generated output stale (%s changed with no matching regeneration).\n",
			types.StaleGeneratedOutput, hash, projects)
		fmt.Fprintf(&b, "Regenerate: magus run generate:rw %s\n", projects)
	}
	if len(f.unformatted) > 0 {
		lead := "[%s] commit %s left formatting stale (gofmt would reformat): %s\n"
		if len(f.staleProjects) > 0 {
			lead = "[%s] commit %s also left formatting stale (gofmt would reformat): %s\n"
		}
		fmt.Fprintf(&b, lead, types.UnformattedCommit, hash, strings.Join(f.unformatted, ", "))
		fmt.Fprintln(&b, "Reformat: magus run format:rw .")
	}
	switch kase {
	case driftUnpushedHead:
		fmt.Fprintf(&b, "Then fold it into %s (unpushed, still HEAD):\n  git commit --amend --no-edit", hash)
	case driftUnpushedNotHead:
		fmt.Fprintf(&b, "Then fold it into %s (unpushed, but no longer HEAD):\n  git commit --fixup=%s && GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash %s^",
			hash, hash, hash)
	default: // driftPushed
		fmt.Fprintf(&b, "%s is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead.", hash)
	}
	return b.String()
}

// joinProjects renders the drifted project set for both the regenerate command's
// argument list and the notice's prose, so the two can never name a different set.
func joinProjects(projects []string) string {
	return strings.Join(projects, " ")
}
