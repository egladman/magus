package interp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// ScriptWorkspace is the workspace a script reads, as the opener found it. The root
// package opens workspaces and this package cannot import it, so the caller adapts
// what it opened to these three reads.
type ScriptWorkspace struct {
	Repo         types.WorkspaceRepository
	CacheDir     string
	ApplySandbox func(context.Context) (context.Context, error)
}

// ScriptOpener opens the workspace at root. It returns nil, nil when there is none
// to attach.
type ScriptOpener func(ctx context.Context, root string) (*ScriptWorkspace, error)

func warnWorkspaceNotAttached(ctx context.Context, err error) {
	slog.WarnContext(ctx, "workspace not attached to this script; its workspace-reading members will raise MGS1022",
		attr.Error(err))
}

// lazyWorkspaceContext opens a script's workspace on the first read instead of at
// startup: about 700ms in this repo, against 10ms for a script that never reads it.
//
// The readers are types.WorkspaceFromContext and recorder.BaseFromContext, called from
// bindings throughout std and internal/interp. Answering their context keys here keeps
// every one of those call sites unchanged, where a lazy WorkspaceRepository would be
// non-nil before the open and so could not raise MGS1022 the way a nil one does, and
// would also have to forward the optional interfaces std type-asserts for. A read that
// cannot attach a workspace sees none, exactly as when the open ran at startup.
type lazyWorkspaceContext struct {
	context.Context //nolint:containedctx // class a: lazyWorkspaceContext is itself a context.Context, no stored-ctx cost to weigh
	root            string
	open            ScriptOpener

	once         sync.Once
	workspace    types.WorkspaceRepository
	activityBase string
	// judge is the one ApplySandbox installs, set once attached.
	judge    std.EditJudge
	judgeErr error
}

var (
	workspaceContextKey = contextKeyReadBy(func(ctx context.Context) { types.WorkspaceFromContext(ctx) })
	activityContextKey  = contextKeyReadBy(func(ctx context.Context) { recorder.BaseFromContext(ctx) })
	editJudgeContextKey = contextKeyReadBy(func(ctx context.Context) { std.EditJudgeFromContext(ctx) })
)

func (c *lazyWorkspaceContext) Value(key any) any {
	switch key {
	case workspaceContextKey:
		c.once.Do(c.attach)
		if c.workspace == nil {
			return nil
		}
		return c.workspace
	case activityContextKey:
		c.once.Do(c.attach)
		if c.activityBase == "" {
			return nil
		}
		return c.activityBase
	case editJudgeContextKey:
		return c.editJudge()
	}
	return c.Context.Value(key)
}

// editJudge defers the open to the first write into the workspace,
// so a write to a temp or env file opens nothing.
// A workspace that fails to open refuses the write.
func (c *lazyWorkspaceContext) editJudge() std.EditJudge {
	return std.EditJudge{
		Covers: func(ctx context.Context, path string) bool {
			root, err := workspace.FindRoot(c.root)
			if err != nil || !inDir(root, path) {
				return false
			}
			c.once.Do(c.attach)
			return c.judge.Covers == nil || c.judge.Covers(ctx, path)
		},
		Grade: func(ctx context.Context, path string, before, after []byte) error {
			c.once.Do(c.attach)
			if c.judge.Grade == nil {
				return fmt.Errorf("fs write %s: refused: the workspace that grades it did not open: %w", path,
					cmp.Or(c.judgeErr, errors.New("it attached no edit judge")))
			}
			return c.judge.Grade(ctx, path, before, after)
		},
	}
}

// inDir reports whether path, made absolute, lies under dir.
func inDir(dir, path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, abs)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// attach opens against the parent context, so the open cannot re-enter Value and
// deadlock on once.
func (c *lazyWorkspaceContext) attach() {
	ws, err := c.open(c.Context, c.root)
	if err != nil {
		c.judgeErr = err
		warnWorkspaceNotAttached(c.Context, err)
		return
	}
	if ws == nil {
		return
	}
	c.workspace = ws.Repo
	c.activityBase = ws.CacheDir
	// The judge the eager door gets, the sandbox being off here.
	sctx, err := ws.ApplySandbox(c.Context)
	if err != nil {
		c.judgeErr = err
		return
	}
	c.judge, _ = std.EditJudgeFromContext(sctx)
}

// keyRecorder is a context that records the last key looked up in it.
type keyRecorder struct {
	context.Context //nolint:containedctx // class a: keyRecorder is itself a context.Context, no stored-ctx cost to weigh
	key             any
}

func (r *keyRecorder) Value(key any) any {
	r.key = key
	return nil
}

// contextKeyReadBy returns the key read looks up, so a context can answer for a
// package whose unexported key it cannot name.
func contextKeyReadBy(read func(context.Context)) any {
	r := &keyRecorder{Context: context.Background()}
	read(r)
	return r.key
}
