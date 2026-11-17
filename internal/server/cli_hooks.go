package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RefreshVCSHooks rewrites each hook section magus already installed, and never a first
// one: `magus init --vcs` does, so a repository holding neither (or jj, which has no
// section reader) gets one notice naming that command. Best-effort: a failure is logged,
// never fatal to starting the server.
func RefreshVCSHooks(ctx context.Context) {
	cwd, err := os.Getwd()
	if err != nil {
		slog.With(attr.Component("server start")).WarnContext(ctx, "could not read the working directory; no VCS hook refreshed", attr.Error(err))
		return
	}
	res, err := vcs.Resolve(ctx, cwd, "", types.VCSOptions{})
	if err != nil || res.VCS == nil {
		return
	}
	root, err := res.VCS.Root(ctx, cwd)
	if err != nil {
		root = cwd
	}
	refresh, drift, err := installedVCSHooks(ctx, res.Name, root)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return
	}
	if err != nil {
		slog.With(attr.Component("server start")).WarnContext(ctx, "could not read the installed VCS hooks; none refreshed", attr.Error(err))
		return
	}
	if !refresh && !drift {
		slog.InfoContext(ctx, fmt.Sprintf("no magus VCS hooks are installed, so history changes do not reconcile the graph and generated drift goes unnoticed; `%s` installs them", hint.Init.With("--vcs", res.Name)), attr.Notice(""), attr.Component("magus"))
		return
	}
	if refresh {
		refreshVCSHook(ctx, res.VCS, res.Name, root, types.VCSDriver.InstallRefreshHook, hint.JobRun.With("sync-graph"),
			"could not refresh VCS refresh hook",
			"refreshed %s refresh hook(s) [%s]")
	}
	if drift {
		refreshVCSHook(ctx, res.VCS, res.Name, root, types.VCSDriver.InstallDriftHook, hint.JobRun.With(job.NameCheckDrift),
			"could not refresh VCS drift-notice hook",
			"refreshed %s drift-notice hook(s) [%s]")
	}
}

// installedVCSHooks reports which managed sections magus installed in root under the
// named VCS. A VCS with no section reader reports neither.
func installedVCSHooks(ctx context.Context, name, root string) (refresh, drift bool, err error) {
	switch name {
	case "git":
		if _, refresh, err = vcs.ReadGitRefreshHook(ctx, root); err != nil {
			return false, false, err
		}
		_, drift, err = vcs.ReadGitDriftHook(ctx, root)
	case "hg":
		if _, refresh, err = vcs.ReadHgRefreshHook(root); err != nil {
			return false, false, err
		}
		_, drift, err = vcs.ReadHgDriftHook(root)
	case "sl":
		if _, refresh, err = vcs.ReadSaplingRefreshHook(root); err != nil {
			return false, false, err
		}
		_, drift, err = vcs.ReadSaplingDriftHook(root)
	}
	return refresh, drift, err
}

// refreshVCSHook rewrites one hook kind's managed sections in root. refreshedFmt takes the
// VCS name and the hook labels.
func refreshVCSHook(ctx context.Context, driver types.VCSDriver, name, root string, install func(types.VCSDriver, context.Context, string, string) ([]string, error), command, failed, refreshedFmt string) {
	changed, err := install(driver, ctx, root, command)
	if errors.Is(err, types.ErrVCSUnsupported) {
		return // this VCS has no hook support
	}
	if err != nil {
		slog.With(attr.Component("server start")).WarnContext(ctx, failed, attr.Error(err))
		return
	}
	if len(changed) > 0 {
		slog.InfoContext(ctx, fmt.Sprintf(refreshedFmt, name, strings.Join(changed, ", ")), attr.Notice(""), attr.Component("magus"))
	}
}
