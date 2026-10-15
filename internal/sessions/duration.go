package sessions

import (
	"cmp"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// maxInvocationFiles bounds LastRunDuration to the newest invocation files: it runs on
// a hook's path with a deadline, each file is read whole, and a store holds thousands.
const maxInvocationFiles = 100

// invocationPrefix starts every id journal.NewInvocationID mints, the only ids a target
// result is filed under. Its base36 millisecond stamp sorts by name in time order.
const invocationPrefix = "inv"

// LastRunDuration is how long target last ran on project, read from the newest
// invocation files under dir. A replay, a run refused before it started and a result with
// no duration are not a run. The charm and a spell filter are ignored, so `lint:rw` answers
// for `lint`. ok is false when none of the newest files records one.
func LastRunDuration(dir, project, target string) (time.Duration, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, false
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), invocationPrefix) && strings.HasSuffix(e.Name(), fileExt) {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	project, target = path.Clean(cmp.Or(project, ".")), baseTarget(target)
	newest := names[max(0, len(names)-maxInvocationFiles):]
	for _, name := range slices.Backward(newest) {
		records, _, _, _ := readFile(filepath.Join(dir, name))
		for _, rec := range slices.Backward(records) {
			if rec.Kind != KindTargetResult {
				continue
			}
			var r TargetResult
			if json.Unmarshal(rec.Payload, &r) != nil {
				continue
			}
			if r.Replayed || r.DiagnosticCode != "" || r.DurationMs <= 0 ||
				baseTarget(r.Target) != target || path.Clean(cmp.Or(r.Project, ".")) != project {
				continue
			}
			return time.Duration(r.DurationMs) * time.Millisecond, true
		}
	}
	return 0, false
}

// baseTarget is a target's normalized name, its spell filter and charm dropped.
func baseTarget(t string) string {
	if _, after, ok := strings.Cut(t, "::"); ok {
		t = after
	}
	name, _, _ := strings.Cut(t, ":")
	return types.Normalize(name)
}
