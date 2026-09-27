package queue

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// SchemaSnapshot names the document [WriteSnapshot] keeps.
const SchemaSnapshot = "mergequeue.snapshot/v1"

// Snapshot is the newest provider read for one base, kept so every reader after it
// answers without the network. `magus queue ls` writes it, having paid for the call,
// and `magus queue plan` adds its plan to it.
type Snapshot struct {
	Schema  string                   `json:"schema"`
	Fetched magustypes.InflightFetch `json:"fetched"`
	Changes types.Changes            `json:"changes"`
	Plan    *types.Plan              `json:"plan,omitempty"`
}

// snapshotDir is the repository's queue state: every worktree and clone of it reads the
// one read any of them made.
func snapshotDir(root string) (string, error) {
	base, err := config.UserStateDir()
	if err != nil {
		return "", fmt.Errorf("queue: resolve state dir: %w", err)
	}
	return vcs.StateDir(base, "queue", root)
}

func snapshotName(base string) string { return "changes-" + url.PathEscape(base) + ".json" }

// WriteSnapshot replaces the snapshot of s's base for the repository at root.
func WriteSnapshot(root string, s Snapshot) error {
	dir, err := snapshotDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	s.Schema = SchemaSnapshot
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return file.WriteFileAtomic(filepath.Join(dir, snapshotName(s.Fetched.Base)), append(b, '\n'), 0o644)
}

// ReadSnapshot returns base's snapshot for the repository at root, and false when no
// read of it was ever kept.
func ReadSnapshot(root, base string) (Snapshot, bool, error) {
	dir, err := snapshotDir(root)
	if err != nil {
		return Snapshot{}, false, err
	}
	return readSnapshot(filepath.Join(dir, snapshotName(base)))
}

// NewestSnapshot returns the most recently fetched snapshot of any base, and false when
// there is none.
func NewestSnapshot(root string) (Snapshot, bool, error) {
	dir, err := snapshotDir(root)
	if err != nil {
		return Snapshot{}, false, err
	}
	paths, err := filepath.Glob(filepath.Join(dir, "changes-*.json"))
	if err != nil {
		return Snapshot{}, false, err
	}
	var newest Snapshot
	found := false
	for _, p := range paths {
		s, ok, err := readSnapshot(p)
		if err != nil {
			return Snapshot{}, false, err
		}
		if ok && (!found || s.Fetched.At > newest.Fetched.At) {
			newest, found = s, true
		}
	}
	return newest, found, nil
}

func readSnapshot(path string) (Snapshot, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, err
	}
	defer f.Close()
	var s Snapshot
	if err := decode(f, SchemaSnapshot, &s, &s.Schema); err != nil {
		return Snapshot{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return s, true, nil
}

// RecordPlan adds p to its base's snapshot. With no snapshot there are no changes for
// the plan to place, so it records nothing.
func RecordPlan(root string, p types.Plan) error {
	s, ok, err := ReadSnapshot(root, p.Base)
	if err != nil || !ok {
		return err
	}
	s.Plan = &p
	return WriteSnapshot(root, s)
}

// JoinInflight joins list to the newest snapshot of the repository at root, as me sees
// it. It reads the snapshot and each job checkout's branch, and never the network.
func JoinInflight(ctx context.Context, root string, list magustypes.JobList, me job.Identity) (magustypes.JobList, error) {
	s, ok, err := NewestSnapshot(root)
	if err != nil {
		return list, err
	}
	if !ok {
		return job.Inflight(list, job.InflightInput{Me: me}), nil
	}
	return job.Inflight(list, job.InflightInput{
		Fetch: &s.Fetched, Changes: s.Changes, Plan: s.Plan, Branches: checkoutBranches(ctx, list.Jobs), Me: me,
	}), nil
}

// checkoutBranches reads the branch each distinct job checkout is on. A checkout that is
// gone, or on no branch, is left out.
func checkoutBranches(ctx context.Context, rows []magustypes.Job) map[string]string {
	out := map[string]string{}
	seen := map[string]bool{}
	for _, row := range rows {
		dir := row.CheckoutRoot
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		if _, err := os.Stat(dir); err != nil {
			continue
		}
		res, err := vcs.Resolve(ctx, dir, "", magustypes.VCSOptions{})
		if err != nil || res.VCS == nil {
			continue
		}
		if st, err := res.VCS.CheckoutState(ctx, dir); err == nil && st.Branch != "" {
			out[dir] = st.Branch
		}
	}
	return out
}
