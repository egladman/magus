package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// staleIndexProjects lists, workspace-relative and sorted, the projects whose built symbol
// index would be rebuilt for the current sources.
//
// Silent on every uncertainty, which is the same contract every other advisory on this
// command keeps. A project with NO index is deliberately not reported here: that is the
// gap probe's answer, printVerdict already renders it as "outside coverage", and one fact
// stated twice in two vocabularies teaches a reader to skip both.
//
// The verdict comes from the probe behind `magus server status`, so the
// banner and the status table agree. It is checked at the read. Only the
// concrete *magus.Magus exposes the cache.
func staleIndexProjects(ctx context.Context, root string) []string {
	return types.StaleIndexProjects(staleIndexes(ctx, root))
}

// staleIndexes is staleIndexProjects an index at a time: a project with a Go and a Buzz
// index has an entry for each one that is stale.
func staleIndexes(ctx context.Context, root string) []types.KnowledgeStaleIndex {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil || ws == nil {
		return nil
	}
	return findStaleIndexes(ctx, ws)
}

func findStaleIndexes(ctx context.Context, ws workspacegraph.ReadWorkspace) []types.KnowledgeStaleIndex {
	var m *magus.Magus
	switch w := ws.(type) {
	case *magus.Magus:
		m = w
	case *magus.LazyWorkspace:
		// Freshness is a cache question (see symbol_stamp.go): it keys the scip step, which
		// needs the evaluated workspace. A read the stored graph answered without opening
		// one takes the verdicts the last evaluation recorded while they provably still
		// hold (unchanged sources and index files, see workspacegraph.RecordedStaleIndexes), and
		// opens the workspace to judge afresh otherwise, as every read did before.
		if !w.Opened() {
			if stale, ok := workspacegraph.RecordedStaleIndexes(ctx, w, globalCfg); ok {
				return stale
			}
		}
		opened, err := w.Magus(ctx)
		if err != nil {
			return nil
		}
		m = opened
	default:
		return nil
	}
	var stale []types.KnowledgeStaleIndex
	for _, s := range m.SymbolIndexStatusByStamp(ctx) {
		if s.Freshness != types.SymbolIndexStale {
			continue
		}
		path := s.Project.Path
		if path == "" {
			path = "."
		}
		stale = append(stale, types.KnowledgeStaleIndex{
			Project: path, Language: s.Language, Op: s.Op, Changed: s.Changed, ChangedCount: s.ChangedCount,
		})
	}
	slices.SortFunc(stale, types.CompareStaleIndexes)
	return stale
}

// knowledgeStoreDir is the checkout at root's knowledge store: where graph builds take
// their lock and `job run sync-graph` records its last request, beside the guard index.
func knowledgeStoreDir(root string) (string, error) {
	cacheDir, err := magus.ResolveCacheDir(root, workspace.WithLoadedConfig(globalCfg))
	if err != nil {
		return "", err
	}
	return knowledge.StoreDir(cacheDir), nil
}

// observeSync gathers what this process can see of why the checkout at root's index is
// current or not. Every probe is local: a socket dial, one status round trip when a server
// answers, two git rev-parses for the hook, and three file reads.
func observeSync(ctx context.Context, root string) maintenance.SyncObservation {
	o := maintenance.SyncObservation{Version: version}
	if addr := resolveServerAddr(""); proc.SocketLive(ctx, addr) {
		if st, err := proc.QueryStatus(ctx, addr); err == nil {
			o.ServerLive, o.ServerVersion, o.ServerPID = true, st.Version, st.ParentPID
		}
	}
	hook, installed, err := vcs.ReadGitRefreshHook(ctx, root)
	o.HookChecked = err == nil
	if installed {
		o.HookCommand = hook.Command
		o.HookBinary, o.HookRunnable = maintenance.HookBinary(hook.Top, hook.Command)
	}
	if dir, err := knowledgeStoreDir(root); err == nil {
		if h, ok := maintenance.RunningGraphBuild(dir); ok {
			o.Building = &h
		}
		if r, ok, err := maintenance.ReadSyncRequest(dir); err == nil && ok {
			o.LastRequest = &r
		}
		if info, err := os.Stat(knowledge.GuardIndexPath(filepath.Dir(dir))); err == nil {
			o.IndexBuilt = info.ModTime()
		}
	}
	return o
}

// withIndexCause returns ans carrying indexCause when it was drawn from an index that is
// missing or behind, and unchanged otherwise. A cause already there is what
// the read itself did about that, which outranks a diagnosis.
func withIndexCause(ctx context.Context, root string, ans types.KnowledgeAnswer) types.KnowledgeAnswer {
	if ans.IndexBehind() && ans.IndexCause == nil {
		c := indexCause(ctx, root)
		ans.IndexCause = &c
	}
	return ans
}

// indexCause is the observed reason the checkout at root has an index missing or behind,
// with its remedy.
func indexCause(ctx context.Context, root string) types.KnowledgeIndexCause {
	return maintenance.DiagnoseSync(observeSync(ctx, resolveRootOrEmpty(root)), maintenance.Commands{
		GraphBuild:  hint.GraphBuild.String(),
		ServerStart: hint.ServerStart.String(),
		ServerStop:  hint.ServerStop.String(),
		JobRunSync:  hint.JobRun.With(job.NameSyncGraph),
	})
}
