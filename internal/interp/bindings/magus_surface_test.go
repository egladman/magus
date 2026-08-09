package bindings

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// surfaceLockPath is the committed snapshot of everything a magusfile can call on the
// magus namespace, one dotted member per line, sorted.
var surfaceLockPath = filepath.Join("testdata", "magus-api.lock")

// magusSurfaceNames flattens the magusfile-surface magus namespace to dotted member
// names, two levels deep: the top-level members plus the members of each namespace
// member (project, cache, ci, secret, workspace). Two levels is what the removal
// history needs - `magus.project.register` and `magus.target.literal` were both
// nested - and going deeper would snapshot returned data rather than the surface.
func magusSurfaceNames(t *testing.T) []string {
	t.Helper()
	ctx := context.Background()
	sess := buzz.NewSession(ctx, buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })

	ns := buildMagusNS(ctx, sess, interp.NewHostCallObserver(ctx), false, magusfileSurface)
	require.True(t, ns.IsMap(), "the magus namespace is a map")

	var out []string
	for _, k := range ns.MapKeys() {
		out = append(out, k)
		v, ok := ns.MapGet(k)
		if !ok || !v.IsMap() {
			continue
		}
		for _, sub := range v.MapKeys() {
			out = append(out, k+"."+sub)
		}
	}
	sort.Strings(out)
	return out
}

// TestMagusSurfaceLocked is the gate the MGS1025 table cannot provide for itself.
//
// removedMagusfileAPI (internal/interp/runtime.go) is hand-maintained, so it only ever
// describes removals someone remembered to write down. Deleting a binding is otherwise
// silent in both directions: Buzz reads a missing member as null, so the magusfiles that
// still call it keep loading and fail later with "null is not callable", and no test
// anywhere notices the surface got smaller.
//
// This makes the surface a committed artifact. A removed member fails here, naming the
// member and the table that has to describe it; an added one fails too, which is the
// cheap price of the snapshot and is settled by regenerating.
func TestMagusSurfaceLocked(t *testing.T) {
	got := magusSurfaceNames(t)

	if os.Getenv("UPDATE_MAGUS_API_LOCK") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(surfaceLockPath), 0o755))
		require.NoError(t, os.WriteFile(surfaceLockPath, []byte(strings.Join(got, "\n")+"\n"), 0o644))
		t.Logf("wrote %s (%d members)", surfaceLockPath, len(got))
		return
	}

	data, err := os.ReadFile(surfaceLockPath)
	require.NoError(t, err, "the surface lock must be committed; regenerate with UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/")
	want := strings.Fields(strings.TrimSpace(string(data)))

	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}
	for _, n := range want {
		assert.Truef(t, gotSet[n],
			"magus.%s was REMOVED from the magusfile surface.\n"+
				"A magusfile still calling it loads fine and fails at run time with "+
				"\"null is not callable\".\n"+
				"Add it to removedMagusfileAPI in internal/interp/runtime.go so it is "+
				"rejected at load with MGS1025, document it in "+
				"docs/reference/codes/magusfile/MGS1025.md, then regenerate this lock with "+
				"UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/", n)
	}

	wantSet := map[string]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range got {
		assert.Truef(t, wantSet[n],
			"magus.%s was ADDED to the magusfile surface; regenerate the lock with "+
				"UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/", n)
	}
}

// TestRemovedAPIIsActuallyRemoved keeps the table honest in the other direction: an
// entry naming a member that still exists would reject a magusfile for calling
// something that works.
func TestRemovedAPIIsActuallyRemoved(t *testing.T) {
	live := map[string]bool{}
	for _, n := range magusSurfaceNames(t) {
		live[n] = true
	}
	for _, name := range interp.RemovedAPINames() {
		assert.Falsef(t, live[name],
			"removedMagusfileAPI lists magus.%s, but the magus namespace still binds it; "+
				"MGS1025 would reject a magusfile for calling something that works", name)
	}
}
