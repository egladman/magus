package interp

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The accumulator must ride the same scope as the deadline, or the error that reports the
// split reads zero for every target: the ceiling context is what the body runs under, and
// a tracker installed anywhere else is a tracker nothing writes to.
func TestDeclaredCeilingTracksDependencyWait(t *testing.T) {
	ws := &ceilingWorkspace{projects: []*types.Project{{
		Dir:            "/w/api",
		TargetPolicies: map[string]types.Target{"build": {Timeout: "15m"}},
	}}}
	ctx := types.WithWorkspace(context.Background(), ws)

	bodyCtx, cancel, ceiling := withDeclaredCeiling(ctx, "/w/api", "build")
	defer cancel()

	require.Equal(t, 15*time.Minute, ceiling)
	types.AddDependencyWait(bodyCtx, 90*time.Second)
	assert.Equal(t, 90*time.Second, types.DependencyWait(bodyCtx),
		"the ceiling scope carries no accumulator, so the split can never be measured")
}

// A target declaring no timeout gets no deadline, but it still gets its own accumulator:
// an uncapped body is exactly the one that would otherwise write its ctx.needs waits into
// a ceilinged ancestor that already counts the whole child as one span.
func TestDeclaredCeilingIsAPassThroughWithoutATimeout(t *testing.T) {
	ws := &ceilingWorkspace{projects: []*types.Project{{
		Dir:            "/w/api",
		TargetPolicies: map[string]types.Target{"build": {}},
	}}}
	parent := types.TrackDependencyWait(types.WithWorkspace(context.Background(), ws))
	types.AddDependencyWait(parent, time.Minute)

	bodyCtx, cancel, ceiling := withDeclaredCeiling(parent, "/w/api", "build")
	defer cancel()

	assert.Zero(t, ceiling)
	_, hasDeadline := bodyCtx.Deadline()
	assert.False(t, hasDeadline)

	types.AddDependencyWait(bodyCtx, 30*time.Second)
	assert.Equal(t, 30*time.Second, types.DependencyWait(bodyCtx))
	assert.Equal(t, time.Minute, types.DependencyWait(parent),
		"an uncapped body's dependency time reached the ceilinged ancestor that already counts it")
}

// The trace line is the whole point of measuring a ceiling that did NOT expire, so it has
// to be reachable. Asserted against a handler at trace level because a log nobody can turn
// on is not observability.
func TestCeilingTraceReportsTheSplit(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: levelTrace})))

	ctx := types.TrackDependencyWait(context.Background())
	types.AddDependencyWait(ctx, 4*time.Second)
	logCeiling(ctx, "ci", 45*time.Minute, 10*time.Second)

	out := buf.String()
	require.Contains(t, out, "target.ceiling")
	assert.Contains(t, out, "target=ci")
	assert.Contains(t, out, "composed=4s")
	assert.Contains(t, out, "own=6s")
}

// Below trace the line costs nothing: the attrs are not built and nothing is written.
func TestCeilingTraceIsSilentAboveTrace(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo})))

	logCeiling(types.TrackDependencyWait(context.Background()), "ci", time.Minute, time.Second)

	assert.Empty(t, strings.TrimSpace(buf.String()))
}

type ceilingWorkspace struct {
	types.WorkspaceRepository
	projects []*types.Project
}

func (w *ceilingWorkspace) All() []*types.Project { return w.projects }
