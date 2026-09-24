package proc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// Output is the one StatusReply conversion `magus status` and the console share: every
// field crosses, a failed workspace keeps its state and error, and Affected stays unset.
func TestStatusReplyOutput(t *testing.T) {
	t.Parallel()
	assert.Nil(t, (*StatusReply)(nil).StatusOutput())

	loaded := time.UnixMilli(1_700_000_000_000)
	access := time.UnixMilli(1_700_000_100_000)
	started := time.UnixMilli(1_700_000_050_000)
	failure := &types.WorkspaceFailure{Message: "magusfile.buzz:3:1: expected expression"}
	reply := &StatusReply{
		ParentPID: 4242, Version: "d1", Capacity: 2, Running: 3, Queued: 1,
		Calls: []Call{
			{Args: []string{"run", "build"}, Workspace: "/ws", StartedAt: started, SubOp: "spawn", Inv: "inv1"},
		},
		Workspaces: []Workspace{
			{
				Root: "/ws", State: types.WorkspaceActive, LoadedAt: loaded, LastAccess: access,
				CacheHit: 5, CacheMiss: 2, CacheError: 1, CacheBytes: 1024, CacheSavedMs: 900, SecretProvider: "vault",
			},
			{Root: "/bad", State: types.WorkspaceFailed, Error: failure},
		},
	}

	assert.Equal(t, &types.StatusOutput{
		ParentPID: 4242,
		Version:   "d1",
		Capacity:  2,
		Running:   3,
		Available: 0,
		Queued:    1,
		RunningTargets: []types.StatusRunningTarget{
			{Args: []string{"run", "build"}, Workspace: "/ws", StartedAt: started, Step: "spawn", Inv: "inv1"},
		},
		Workspaces: []types.StatusWorkspace{
			{
				Root: "/ws", State: types.WorkspaceActive, LoadedAt: loaded, LastAccess: access,
				CacheHit: 5, CacheMiss: 2, CacheError: 1, CacheBytes: 1024, CacheSavedMs: 900, SecretProvider: "vault",
			},
			{Root: "/bad", State: types.WorkspaceFailed, Error: failure},
		},
	}, reply.StatusOutput())
}
