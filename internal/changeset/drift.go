package changeset

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// PushedUnread is the unread notice of each range a push sends: what the remote held against what
// is sent, or the remote's default branch against it for a ref the remote does not have yet. Git
// only, since only git's hook names the objects. A range that cannot be read, or has nothing
// unread, adds no notice.
func PushedUnread(ctx context.Context, res types.VCSResolution, root, cacheDir, remote string, pushes []job.DriftPush) []UnreadNotice {
	rr, ok := res.VCS.(types.RangeReporter)
	if !ok || res.Name != "git" || len(pushes) == 0 {
		return nil
	}
	if remote == "" {
		remote = "origin"
	}
	viewed, verr := NewStore(cacheDir).LoadViewed()
	var notices []UnreadNotice
	for _, p := range pushes {
		base, label := p.Remote, job.ShortRevision(p.Remote)
		if strings.Trim(p.Remote, "0") == "" {
			base = remote + "/HEAD"
			label = base
		}
		patch, err := rr.RangeDiff(ctx, root, base, p.Local, nil)
		if err != nil {
			slog.With(attr.Component("check-drift")).DebugContext(ctx, "pushed range unreadable", slog.String("base", base), attr.Error(err))
			continue
		}
		if n, ok := unreadRangeNotice(fmt.Sprintf("%s...%s", label, job.ShortRevision(p.Local)), patch, viewed, verr); ok {
			notices = append(notices, n)
		}
	}
	return notices
}

// CheckDriftJobArgv is the check-drift worker command for the hook that ran `job run
// check-drift`: hookArgs are the hook's name and git's own arguments to it, and stdin is what
// git wrote the hook. pre-push reads its pushed refs from stdin, one `<local ref> <local
// object> <remote ref> <remote object>` line each; a deletion sends nothing and adds no range.
// With no hook named, it is the plain worker command.
func CheckDriftJobArgv(hookArgs []string, stdin io.Reader) []string {
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

// UnreadNotice is what the drift job says about one pushed range: the message, the one command
// that reads it, and for a range whose read marks failed to load, the reason and the error.
type UnreadNotice struct {
	Msg, Next string
	Why       string
	Err       error
}

// unreadRangeNotice says how many hunks of patch no read mark covers. viewed holds the digests
// marked read and loadErr is the error from reading them: an unreadable store makes the notice
// say the read state is unknown, never that every hunk is unread. It reports false for a range
// with no hunks, or one read in full.
func unreadRangeNotice(rangeLabel, patch string, viewed []string, loadErr error) (UnreadNotice, bool) {
	parsed := ParseHunks(patch)
	total := 0
	for _, f := range parsed {
		total += len(f.Hunks)
	}
	if total == 0 {
		return UnreadNotice{}, false
	}
	next := hint.Diff.With("--unread", "--rev", rangeLabel)
	if loadErr != nil {
		return UnreadNotice{
			Msg:  fmt.Sprintf("read state unknown for the %d hunks of this range (%s)", total, rangeLabel),
			Next: next,
			Why:  "the read marks could not be read",
			Err:  loadErr,
		}, true
	}
	n := len(UnreadHunks(parsed, viewed))
	if n == 0 {
		return UnreadNotice{}, false
	}
	return UnreadNotice{Msg: fmt.Sprintf("%d of %d hunks of this range (%s) unread", n, total, rangeLabel), Next: next}, true
}

// CheckDriftForCommit is the VCS-facing half of serverCheckDrift, kept separate so it can
// be exercised against a real repository without needing a full magus workspace.
// classify turns changed paths into their declared source/output role.
// A test supplies its own instead of loading a workspace.
//
// It returns ok=false with no error when there is nothing to say.
func CheckDriftForCommit(
	ctx context.Context,
	root string,
	driver types.VCSDriver,
	classify func(context.Context, []string) ([]types.FileEntry, error),
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
	stale := types.StaleSourceProjects(files)
	if len(stale) == 0 {
		return "", false, nil
	}
	kase := foldCase(ctx, root, driver, commitID, func() bool {
		after, err := driver.Metadata(ctx, root)
		return err == nil && after.ID == commitID
	})
	return buildDriftNotice(driver, "commit "+commitID, commitID, stale, kase), true, nil
}

// foldCase is which fold hash takes. isTip is asked last, since the tip can move while
// classification runs. A backend that cannot tell whether hash is pushed reads as
// pushed, the safe side types.PushStatusReporter documents.
func foldCase(ctx context.Context, root string, driver types.VCSDriver, hash string, isTip func() bool) driftNoticeCase {
	pushed, known, err := driver.CommitPushed(ctx, root, hash)
	switch {
	case err != nil || !known || pushed:
		return driftPushed
	case isTip():
		return driftUnpushedHead
	default:
		return driftUnpushedNotHead
	}
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

// buildDriftNotice renders the MGS4006 notice for subject, which left the stale
// projects' generated output behind. It ends with id's own command that folds the fix
// into hash, or the follow-up commit published history takes.
func buildDriftNotice(id types.VCSIdentity, subject, hash string, stale []string, kase driftNoticeCase) string {
	var b strings.Builder
	projects := strings.Join(stale, " ")
	fmt.Fprintf(&b, "[%s] %s left generated output stale (%s changed with no matching regeneration).\n",
		types.StaleGeneratedOutput, subject, projects)
	fmt.Fprintf(&b, "Regenerate: magus run generate:rw %s\n", projects)
	b.WriteString(foldLine(id, hash, kase))
	return b.String()
}

// foldLine is the step after regenerating: id's command that folds the outputs into
// hash, or the follow-up commit published history takes.
func foldLine(id types.VCSIdentity, hash string, kase driftNoticeCase) string {
	if kase == driftPushed {
		return fmt.Sprintf("%s is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead.", hash)
	}
	tip := kase == driftUnpushedHead
	where := "unpushed, still the tip"
	if !tip {
		where = "unpushed, but no longer the tip"
	}
	fold, ok := id.FoldCommand(hash, tip)
	if !ok {
		return fmt.Sprintf("Then fold it into %s (%s). %s has no single command that folds below the tip: fold it by hand, or commit the fix as a follow-up commit.",
			hash, where, id.Name())
	}
	return fmt.Sprintf("Then fold it into %s (%s):\n  %s", hash, where, fold)
}
