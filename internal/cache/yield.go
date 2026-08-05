package cache

import (
	"bufio"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// A target that never replays is the most expensive kind of misconfiguration, because
// nothing about it looks wrong: every run passes, the cache reports no error, and the
// only symptom is that the build is slower than it should be. It is invisible per-run
// and obvious only in aggregate - which is why it is read back out of the invocation
// journals rather than detected live.
//
// The thresholds keep this quiet until acting on it is worthwhile. A handful of runs
// proves nothing (a target legitimately misses while you edit its inputs), and a target
// that misses in milliseconds costs nothing to rerun.
const (
	// MinRunsForYield is how many executions a target needs before a zero hit rate is
	// evidence rather than noise.
	MinRunsForYield = 8
	// MinAvgMsForYield is the average execution time below which a never-cached target
	// is not worth reporting.
	MinAvgMsForYield = 2_000
	// maxJournalsScanned bounds the read. Journals accumulate one per invocation, and
	// the recent ones describe how the workspace behaves today.
	maxJournalsScanned = 200
)

// Stalled is one (project, target) pair that executed repeatedly and never replayed
// from cache. TotalMs is the wall clock spent doing so.
type Stalled struct {
	// Project is the human display form ("(root)" for the workspace root).
	Project string
	// ProjectPath is the raw workspace-relative key the journal recorded ("." for the
	// root). Carried alongside the display form so a caller can look the project up in
	// the workspace - doctor matches these against per-target policy to tell a target
	// that CANNOT replay by design from one that merely does not.
	ProjectPath string
	Target      string
	Runs        int
	TotalMs     int64
}

// AvgMs is the mean execution time across Runs.
func (s Stalled) AvgMs() int64 {
	if s.Runs == 0 {
		return 0
	}
	return s.TotalMs / int64(s.Runs)
}

// StalledTargets reports targets that execute repeatedly and never replay from cache,
// worst wall-clock first.
//
// It reads the invocation journals magus already writes (<cacheDir>/runs/*.jsonl), so it
// costs no new bookkeeping: every target result is recorded there with a status of
// "cached" (replayed) or "pass"/"fail" (executed). A pair with many executions and no
// replays at all is not a cold cache, it is a cache that cannot work - almost always
// because the target's footprint includes inputs it does not read, so ordinary edits
// keep busting a key that had no reason to change.
//
// When only is non-empty, only those "project\x00target" keys are considered. The run
// path passes the targets that just executed, so a normal run never pays to assess the
// whole workspace.
func StalledTargets(cacheDir string, only map[string]bool) []Stalled {
	journals, err := filepath.Glob(filepath.Join(cacheDir, RunsDir, "*.jsonl"))
	if err != nil || len(journals) == 0 {
		return nil
	}
	sort.Strings(journals)
	if len(journals) > maxJournalsScanned {
		journals = journals[len(journals)-maxJournalsScanned:]
	}

	type tally struct {
		project     string
		projectPath string
		target      string
		ran         int
		cached      int
		totalMs     int64
	}
	tallies := map[string]*tally{}
	for _, path := range journals {
		// A journal that could not be read whole is skipped entirely: a partial tally can
		// invent a stalled target by dropping its "cached" records.
		partial := map[string]*tally{}
		if err := scanJournal(path, only, func(project, target, status string, durMs int64) {
			key := project + "\x00" + target
			t := partial[key]
			if t == nil {
				t = &tally{project: displayProject(project), projectPath: project, target: target}
				partial[key] = t
			}
			switch status {
			case "cached":
				t.cached++
			case "pass", "fail":
				// A failure still executed, so it still proves the cache did not replay.
				t.ran++
				t.totalMs += durMs
			}
		}); err != nil {
			continue
		}
		for k, t := range partial {
			agg := tallies[k]
			if agg == nil {
				tallies[k] = t
				continue
			}
			agg.ran += t.ran
			agg.cached += t.cached
			agg.totalMs += t.totalMs
		}
	}

	var out []Stalled
	for _, t := range tallies {
		if t.cached == 0 && t.ran >= MinRunsForYield && t.totalMs/int64(t.ran) >= MinAvgMsForYield {
			out = append(out, Stalled{Project: t.project, ProjectPath: t.projectPath, Target: t.target, Runs: t.ran, TotalMs: t.totalMs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalMs > out[j].TotalMs })
	return out
}

// scanJournal feeds each result record in one journal to fn. Journals are append-only
// and a run killed mid-write leaves a partial final line, so an unparsable line is
// skipped rather than treated as a corrupt file.
func scanJournal(path string, only map[string]bool, fn func(project, target, status string, durMs int64)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // captured output lines can be long
	for sc.Scan() {
		var rec struct {
			Kind    string `json:"kind"`
			Project string `json:"project"`
			Target  string `json:"target"`
			Status  string `json:"status"`
			DurMs   int64  `json:"dur_ms"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil || rec.Kind != "result" || rec.Target == "" {
			continue
		}
		if len(only) > 0 && !only[rec.Project+"\x00"+rec.Target] {
			continue
		}
		fn(rec.Project, rec.Target, rec.Status, rec.DurMs)
	}
	// A line over the buffer cap aborts the scan mid-file and otherwise looks like EOF.
	// Half a journal is worse than none here: dropping a "cached" record while keeping
	// the "ran" ones is exactly the shape that accuses a working target of never
	// replaying. Report it so the caller can discard the whole file's tally.
	return sc.Err()
}

// displayProject names the workspace root, which journals record as ".", the way the
// rest of the CLI prints it.
func displayProject(p string) string {
	if p == "" || p == "." {
		return "(root)"
	}
	return strings.TrimSuffix(p, "/")
}

// SlowExecutions returns the "project\x00target" keys that actually EXECUTED in one
// invocation journal and took at least minMs. Cached replays are excluded: a replay
// proves the cache works for that target, which is the opposite of the finding.
//
// This is the gate that makes a per-run cache-yield check affordable. Scanning the
// workspace's whole journal history on every run would be absurd; scanning it after a
// target just spent a minute executing is free by comparison, and a fast run reads only
// its own journal and stops.
func SlowExecutions(journalPath string, minMs int64) map[string]bool {
	out := map[string]bool{}
	if err := scanJournal(journalPath, nil, func(project, target, status string, durMs int64) {
		if (status == "pass" || status == "fail") && durMs >= minMs {
			out[project+"\x00"+target] = true
		}
	}); err != nil {
		// Same discard-on-error rule as StalledTargets: a journal that could not be
		// read whole may have dropped a slow execution, and a partial answer here
		// would silently narrow which targets get checked for yield.
		return map[string]bool{}
	}
	return out
}
