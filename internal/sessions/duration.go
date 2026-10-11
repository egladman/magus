package sessions

import (
	"cmp"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// lastRunScan bounds LastRealDuration to the newest invocation files: it runs on a hook's
// path, and a store holds thousands.
const lastRunScan = 300

// invocationPrefix starts every id journal.NewInvocationID mints, the only ids a target
// result is filed under. Its base36 millisecond stamp sorts by name in time order.
const invocationPrefix = "inv"

// LastRealDuration is how long target last ran on project for real, read from the newest
// invocation files under dir. A replay, a run refused before it started and a result with
// no duration are not a run. The charm and a spell filter are ignored, so `lint:rw` answers
// for `lint`. ok is false when none of the newest files records one.
func LastRealDuration(dir, project, target string) (time.Duration, bool) {
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
	for i := len(names) - 1; i >= 0 && i >= len(names)-lastRunScan; i-- {
		records, _, _, _ := readFile(filepath.Join(dir, names[i]))
		for j := len(records) - 1; j >= 0; j-- {
			if records[j].Kind != KindTargetResult {
				continue
			}
			var r TargetResult
			if json.Unmarshal(records[j].Payload, &r) != nil {
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
