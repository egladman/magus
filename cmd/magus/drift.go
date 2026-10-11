package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive/tty"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
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
//
// Run for pre-push, it also counts the hunks of the pushed ranges that no read mark covers
// (pushedUnread), the same count `magus diff --unread` filters to, and raises the desktop
// notice for them even when nothing drifted. After a commit it never counts them: a notice
// per commit would be noise.
func serverCheckDrift(ctx context.Context, root string, args []string) error {
	hook, err := parseCheckDriftArgs(args)
	if err != nil {
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
	var formatGlobs []types.Glob
	if p := m.Get("."); p != nil {
		formatGlobs = types.ChainUpdates(p, "format", m.Get)
	}
	notice, ok, err := checkDriftForCommit(ctx, m.Root(), res.VCS, m.ClassifyFiles, formatGlobs, realGofmtList)
	if err != nil {
		slog.With(attr.Component("check-drift")).WarnContext(ctx, "could not check HEAD for stale output", attr.Error(err))
		// Best-effort: a broken check must not be mistaken for a failed commit or push.
		ok = false
	}
	var lines []string
	if ok {
		lines = append(lines, notice)
		slog.WarnContext(ctx, notice, attr.Notice(""))
	}
	if hook.Hook == job.DriftHookPrePush {
		remote := hook.Remote
		if remote == "" {
			remote = m.ReviewOrigin(ctx).Remote
		}
		log := slog.With(attr.Component("check-drift"))
		for _, u := range pushedUnread(ctx, res, m.Root(), m.CacheDir(), remote, hook.Pushes) {
			attrs := []any{attr.Notice(""), attr.Next(u.next)}
			if u.err != nil {
				attrs = append(attrs, attr.Why(u.why), attr.Error(u.err))
			}
			log.WarnContext(ctx, u.msg, attrs...)
			lines = append(lines, u.msg)
		}
	}
	if len(lines) > 0 {
		noteJobDesktop(ctx, job.NameCheckDrift, strings.Join(lines, "\n"))
	}
	return nil
}

// parseCheckDriftArgs reads the worker's flags, the ones [job.DriftHook.Argv] writes. With
// none it is a commit's run, which is what a drift section an older magus installed calls.
func parseCheckDriftArgs(args []string) (job.DriftHook, error) {
	var h job.DriftHook
	_, err := cmdParse("server "+job.NameCheckDrift, args, func(fs *flag.FlagSet) {
		fs.StringVar(&h.Hook, "hook", job.DriftHookPostCommit, "The hook this run is for: post-commit or pre-push")
		fs.StringVar(&h.Remote, "remote", "", "The remote a push goes to")
		fs.Func("push", "One ref a push sends, as <remote object>:<local object>; repeatable", func(v string) error {
			remote, local, found := strings.Cut(v, ":")
			if !found {
				return fmt.Errorf("want <remote object>:<local object>, got %q", v)
			}
			h.Pushes = append(h.Pushes, job.DriftPush{Remote: remote, Local: local})
			return nil
		})
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus server check-drift [--hook <name>] [--remote <name>] [--push <remote>:<local>]...")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Notice when HEAD left generated output or formatting stale, and for a push, count")
			fmt.Fprintln(os.Stderr, "the unread hunks of each pushed range. This is the worker for")
			fmt.Fprintln(os.Stderr, "`"+hint.JobRun.With(job.NameCheckDrift)+"`; prefer that form.")
		}
	})
	return h, err
}

// pushedUnread is the unread notice of each range a push sends: what the remote held against what
// is sent, or the remote's default branch against it for a ref the remote does not have yet. Git
// only, since only git's hook names the objects. A range that cannot be read, or has nothing
// unread, adds no notice.
func pushedUnread(ctx context.Context, res types.VCSResolution, root, cacheDir, remote string, pushes []job.DriftPush) []unreadNotice {
	rr, ok := res.VCS.(types.RangeReporter)
	if !ok || res.Name != "git" || len(pushes) == 0 {
		return nil
	}
	if remote == "" {
		remote = "origin"
	}
	viewed, verr := changeset.NewStore(cacheDir).LoadViewed()
	var notices []unreadNotice
	for _, p := range pushes {
		base, label := p.Remote, short(p.Remote)
		if strings.Trim(p.Remote, "0") == "" {
			base = remote + "/HEAD"
			label = base
		}
		patch, err := rr.RangeDiff(ctx, root, base, p.Local, nil)
		if err != nil {
			slog.With(attr.Component("check-drift")).DebugContext(ctx, "pushed range unreadable", slog.String("base", base), attr.Error(err))
			continue
		}
		if n, ok := unreadRangeNotice(label+"..."+short(p.Local), patch, viewed, verr); ok {
			notices = append(notices, n)
		}
	}
	return notices
}

// hookStdin is what git wrote the hook running this command, and nothing when a person typed
// the command at a terminal, where reading would wait for input that never comes.
func hookStdin() io.Reader {
	if tty.StdinIsTerminal() {
		return strings.NewReader("")
	}
	return os.Stdin
}

// checkDriftJobArgv is the check-drift worker command for the hook that ran `job run
// check-drift`: hookArgs are the hook's name and git's own arguments to it, and stdin is what
// git wrote the hook. pre-push reads its pushed refs from stdin, one `<local ref> <local
// object> <remote ref> <remote object>` line each; a deletion sends nothing and adds no range.
// With no hook named, it is the plain worker command.
func checkDriftJobArgv(hookArgs []string, stdin io.Reader) []string {
	entry, _ := job.Lookup(job.NameCheckDrift)
	if len(hookArgs) == 0 {
		return entry.Argv
	}
	h := job.DriftHook{Hook: hookArgs[0]}
	if h.Hook != job.DriftHookPrePush {
		return h.Argv()
	}
	if len(hookArgs) > 1 {
		h.Remote = hookArgs[1]
	}
	sc := bufio.NewScanner(stdin)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) != 4 || strings.Trim(f[1], "0") == "" {
			continue
		}
		h.Pushes = append(h.Pushes, job.DriftPush{Remote: f[3], Local: f[1]})
	}
	// A push to a bare URL names the URL where the remote goes, and the server admits no URL. A
	// new ref's range then reads against the review's own remote.
	if _, ok := job.ParseDriftHook(h.Argv()[len(entry.Argv):]); !ok {
		h.Remote = ""
	}
	return h.Argv()
}

// unreadNotice is what the drift job says about one pushed range: the message, the one command
// that reads it, and for a range whose read marks failed to load, the reason and the error.
type unreadNotice struct {
	msg, next string
	why       string
	err       error
}

// unreadRangeNotice says how many hunks of patch no read mark covers. viewed holds the digests
// marked read and loadErr is the error from reading them: an unreadable store makes the notice
// say the read state is unknown, never that every hunk is unread. It reports false for a range
// with no hunks, or one read in full.
func unreadRangeNotice(rangeLabel, patch string, viewed []string, loadErr error) (unreadNotice, bool) {
	parsed := changeset.ParseHunks(patch)
	total := 0
	for _, f := range parsed {
		total += len(f.Hunks)
	}
	if total == 0 {
		return unreadNotice{}, false
	}
	next := hint.Diff.With("--unread", "--rev", rangeLabel)
	if loadErr != nil {
		return unreadNotice{
			msg:  fmt.Sprintf("read state unknown for the %d hunks of this range (%s)", total, rangeLabel),
			next: next,
			why:  "the read marks could not be read",
			err:  loadErr,
		}, true
	}
	n := len(changeset.UnreadHunks(parsed, viewed))
	if n == 0 {
		return unreadNotice{}, false
	}
	return unreadNotice{msg: fmt.Sprintf("%d of %d hunks of this range (%s) unread", n, total, rangeLabel), next: next}, true
}

// checkDriftForCommit is the VCS-facing half of serverCheckDrift, kept separate so it can
// be exercised against a real repository without needing a full magus workspace.
// classify and gofmtList are the two things that need one: turning changed paths into
// their declared source/output role, and asking a real gofmt binary about a file. A
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
	formatGlobs []types.Glob,
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
			slog.With(attr.Component("check-drift")).WarnContext(ctx, "gofmt probe failed", attr.Error(gerr))
		} else {
			finding.unformatted = unformatted
		}
	}

	if finding.empty() {
		return "", false, nil
	}

	kase := driftPushed
	if pushed, known, perr := driver.CommitPushed(ctx, root, commitID); perr == nil && known && !pushed {
		after, merr := driver.Metadata(ctx, root)
		stillHead := merr == nil && after.ID == commitID
		if stillHead {
			kase = driftUnpushedHead
		} else {
			kase = driftUnpushedNotHead
		}
	}
	// perr != nil (an unsupported backend included), or known == false: the reporter
	// could not tell. kase stays driftPushed, the safe assumption types.PushStatusReporter
	// documents.

	return buildDriftNotice(commitID, finding, kase), true, nil
}

// formatGovernedGoFiles narrows paths to the commit's changed Go files that the format
// target's own ctx.modifiesExistingFiles declares governed (updateGlobs, from
// types.ChainUpdates). Go only: dprint/Markdown formatting is a real gap this pass does
// not close (see the report), and narrowing first is what keeps gofmt -l from ever
// running when nothing changed calls for it.
func formatGovernedGoFiles(paths []string, updateGlobs []types.Glob) []string {
	if len(updateGlobs) == 0 {
		return nil
	}
	var out []string
	for _, p := range paths {
		if strings.HasSuffix(p, ".go") && types.MatchGlobs(updateGlobs, p) {
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

// noteJobDesktop raises a best-effort desktop notification from the server job
// jobName, carrying the same notice already printed to stderr. The stderr line is the durable, testable record (job output
// a dashboard or run log shows); the desktop alert is what makes "at the moment it
// happens" true for a job the server runs out of band, matching how `magus session
// notify --desktop` already tells a person something without touching their terminal.
// Never fails the job: a missing notifier (no osascript, no notify-send) is exactly the
// case raiseDesktopNotification already swallows.
func noteJobDesktop(ctx context.Context, jobName, notice string) {
	ev := types.Event{
		Message: notice,
		Outcome: types.OutcomeDiagnostic,
		Source:  types.EventSource{Kind: "magus", Sub: jobName},
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
