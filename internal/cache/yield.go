package cache

import (
	"bufio"
	"cmp"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// A target that never replays is the most expensive kind of misconfiguration, because
// nothing about it looks wrong: every run passes, the cache reports no error, and the
// only symptom is that the build is slower than it should be. It is invisible per-run
// and obvious only in aggregate, which is why it is read back out of the invocation
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
	// the workspace: doctor matches these against per-target policy to tell a target
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
// replays at all is not a cold cache, it is a cache that cannot work: almost always
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
		// cmds are the DISTINCT commands the executions ran, from the journal's exec
		// records. More than one means the runs were not the same work, so "never
		// replayed" says nothing about the footprint: a different command SHOULD miss.
		// Measured: `go-test` showed 43 runs and 0 replays, which was twenty different
		// `-run` filters, most carrying `-count=1`. See the report filter below.
		cmds map[string]bool
		// cacheKeys are the DISTINCT cache keys those executions carried. The same argument
		// applies one level down: a key that differs every run means the INPUTS moved, so
		// the miss was correct and the cache is doing its job. Only a key that repeats
		// and still never replays is evidence of a footprint keyed on more than the
		// target reads. Measured: two generators showed 10 runs and 0 replays after ten
		// edits to the Go sources they read, which is not a defect at all.
		//
		// Empty for a journal written before result events carried the key; the report
		// filter falls back to the command test rather than guessing.
		cacheKeys map[string]bool
	}
	tallies := map[string]*tally{}
	for _, path := range journals {
		// A journal that could not be read whole is skipped entirely: a partial tally can
		// invent a stalled target by dropping its "cached" records.
		partial := map[string]*tally{}
		if err := scanJournal(path, only, func(rec journalRecord) {
			id := rec.project + "\x00" + rec.target
			t := partial[id]
			if t == nil {
				t = &tally{
					project: displayProject(rec.project), projectPath: rec.project, target: rec.target,
					cmds: map[string]bool{}, cacheKeys: map[string]bool{},
				}
				partial[id] = t
			}
			if rec.kind == "exec" {
				t.cmds[rec.text] = true
				return
			}
			switch rec.status {
			case "cached":
				t.cached++
			case "pass", "fail":
				// A failure still executed, so it still proves the cache did not replay.
				t.ran++
				t.totalMs += rec.durationMs
				if rec.cacheKey != "" {
					t.cacheKeys[rec.cacheKey] = true
				}
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
			for cmd := range t.cmds {
				agg.cmds[cmd] = true
			}
			for k := range t.cacheKeys {
				agg.cacheKeys[k] = true
			}
		}
	}

	var out []Stalled
	for _, t := range tallies {
		// More than one distinct command means the runs were not the same work, and a
		// different command is SUPPOSED to miss. Reporting it would accuse a target of a
		// footprint it has no evidence against, which is the failure this check exists to
		// avoid on the other side. It under-reports instead: a target whose args vary AND
		// whose footprint is wrong stays silent until someone runs it the same way twice.
		if len(t.cmds) > 1 {
			continue
		}
		// Keys that differ mean the inputs moved, so every miss was correct. This is the
		// same judgment as the command test, one level down, and it is the one that
		// separates a broken footprint from an ordinary edit-and-rebuild session.
		if len(t.cacheKeys) > 1 {
			continue
		}
		if t.cached == 0 && t.ran >= MinRunsForYield && t.totalMs/int64(t.ran) >= MinAvgMsForYield {
			out = append(out, Stalled{Project: t.project, ProjectPath: t.projectPath, Target: t.target, Runs: t.ran, TotalMs: t.totalMs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TotalMs > out[j].TotalMs })
	return out
}

// scanJournal feeds each result and exec record in one journal to fn. Journals are
// append-only and a run killed mid-write leaves a partial final line, so an unparsable
// line is skipped rather than treated as a corrupt file.
//
// exec records carry the command text, which is what lets a caller tell one target run
// twice from one target run two different ways. A replay writes no exec record, so the
// text cannot key the tally; it can only qualify it.
// journalRecord is one result or exec line, as the yield tallies read it. A struct rather
// than six positional parameters: the list grew twice while answering the same question,
// and a caller that transposes two strings of the same type gets no compiler error.
type journalRecord struct {
	project    string
	target     string
	kind       string
	status     string
	text       string
	cacheKey   string
	durationMs int64
}

func scanJournal(path string, only map[string]bool, fn func(journalRecord)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // captured output lines can be long
	for sc.Scan() {
		var rec struct {
			Kind       string `json:"kind"`
			Project    string `json:"project"`
			Target     string `json:"target"`
			Status     string `json:"status"`
			Text       string `json:"text"`
			CacheKey   string `json:"cache_key"`
			DurationMs int64  `json:"duration_ms"`
			// compat: see journal.Event.UnmarshalJSON. This scan decodes into a struct
			// of its own rather than journal.Event, so it repeats the fallback rather
			// than inheriting it.
			LegacyDurationMs int64 `json:"dur_ms"`
		}
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil || rec.Target == "" {
			continue
		}
		if rec.Kind != "result" && rec.Kind != "exec" {
			continue
		}
		if len(only) > 0 && !only[rec.Project+"\x00"+rec.Target] {
			continue
		}
		fn(journalRecord{
			project: rec.Project, target: rec.Target, kind: rec.Kind,
			status: rec.Status, text: rec.Text, cacheKey: rec.CacheKey,
			durationMs: cmp.Or(rec.DurationMs, rec.LegacyDurationMs),
		})
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
	if err := scanJournal(journalPath, nil, func(rec journalRecord) {
		if rec.kind != "result" {
			return
		}
		if (rec.status == "pass" || rec.status == "fail") && rec.durationMs >= minMs {
			out[rec.project+"\x00"+rec.target] = true
		}
	}); err != nil {
		// Same discard-on-error rule as StalledTargets: a journal that could not be
		// read whole may have dropped a slow execution, and a partial answer here
		// would silently narrow which targets get checked for yield.
		return map[string]bool{}
	}
	return out
}
