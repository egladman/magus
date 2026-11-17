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
	finding := driftFinding{staleProjects: types.StaleSourceProjects(files)}

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

// driftFinding is everything CheckDriftForCommit learned about one commit.
type driftFinding struct {
	// staleProjects: MGS4006 (types.StaleGeneratedOutput): a declared source changed
	// with no matching declared-output change in the same commit.
	staleProjects []string
}

func (f driftFinding) empty() bool { return len(f.staleProjects) == 0 }

// buildDriftNotice renders the notice for a commit that left generated output
// stale. It ends with the one VCS command that folds a fix into hash, if any.
//
// Pure: the caller has already decided the finding and the amend-safety case.
func buildDriftNotice(hash string, f driftFinding, kase driftNoticeCase) string {
	var b strings.Builder
	if len(f.staleProjects) > 0 {
		projects := joinProjects(f.staleProjects)
		fmt.Fprintf(&b, "[%s] commit %s left generated output stale (%s changed with no matching regeneration).\n",
			types.StaleGeneratedOutput, hash, projects)
		fmt.Fprintf(&b, "Regenerate: magus run generate:rw %s\n", projects)
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
