package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// serverCheckDrift is the worker for the check-drift job: it notices, without blocking
// anything, that HEAD left generated output stale (MGS4006).
// The hooks call it through `magus job run check-drift`.
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
	notice, ok, err := changeset.CheckDriftForCommit(ctx, m.Root(), res.VCS, m.ClassifyFiles)
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
		for _, u := range changeset.PushedUnread(ctx, res, m.Root(), m.CacheDir(), remote, hook.Pushes) {
			attrs := []any{attr.Notice(""), attr.Next(u.Next)}
			if u.Err != nil {
				attrs = append(attrs, attr.Why(u.Why), attr.Error(u.Err))
			}
			log.WarnContext(ctx, u.Msg, attrs...)
			lines = append(lines, u.Msg)
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
	_, err := commandParse("server "+job.NameCheckDrift, args, func(fs *flag.FlagSet) {
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
			fmt.Fprintln(os.Stderr, "Notice when HEAD left generated output stale, and for a push, count")
			fmt.Fprintln(os.Stderr, "the unread hunks of each pushed range. This is the worker for")
			fmt.Fprintf(os.Stderr, "`%s`; prefer that form.\n", hint.JobRun.With(job.NameCheckDrift))
		}
	})
	return h, err
}

// hookStdin is what git wrote the hook running this command, and nothing when a person typed
// the command at a terminal, where reading would wait for input that never comes.
func hookStdin() io.Reader {
	if termkit.StdinIsTerminal() {
		return strings.NewReader("")
	}
	return os.Stdin
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

// compat(until: no cmd file calls checkDriftJobArgv): server.go still names it.
func checkDriftJobArgv(hookArgs []string, stdin io.Reader) []string {
	return changeset.CheckDriftJobArgv(hookArgs, stdin)
}
