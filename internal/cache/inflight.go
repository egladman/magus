package cache

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// inflight records which targets are RUNNING, on disk, so the answer survives a death
// the process cannot report on.
//
// magus already reports a target that fails and a run that is cancelled. The case with
// no reporter is the run that is KILLED: SIGKILL cannot be trapped, so a magus the OOM
// killer takes leaves no last words and a CI job simply stops mid-log. That happened
// here - a shard died after "[pass] magus lint" with nothing after it, and the only way
// to learn what it had been doing was to guess.
//
// A live snapshot cannot answer that, because it dies with the process. So this writes
// through to a file on every edge, and the NEXT run reports what it finds. The reporter
// is a later invocation rather than the dying one, which is the only design that works
// when the dying one gets no cycles at all.
//
// ONE FILE PER PID, not one shared file. Several magus processes share a cache dir
// routinely (a recursive magus\run, the daemon serving two dispatches, an agent beside a
// human), and a shared file means each rewrite erases the others - which would delete
// exactly the record a peer is about to need. Per-pid also makes every write single-writer,
// so a reader never sees a torn set.
type inflight struct {
	dir  string // directory holding inflight-<pid>.json for every live run
	path string // this process's own file

	mu      sync.Mutex
	running map[string]inflightTarget
}

// inflightTarget is one running target. Pid and Started together identify the run: a pid
// alone is reused, and a restored CI cache can carry one from another machine entirely.
type inflightTarget struct {
	Project string    `json:"project"`
	Target  string    `json:"target"`
	Pid     int       `json:"pid"`
	Host    string    `json:"host"`
	Started time.Time `json:"started"`
}

const inflightPrefix = "inflight-"

// newInflight returns a tracker writing under dir, or nil when dir is empty. A nil
// tracker is usable: every method is nil-safe, so a cache with nowhere to write records
// nothing rather than making each call site check.
func newInflight(dir string) *inflight {
	if dir == "" {
		return nil
	}
	pid := os.Getpid()
	return &inflight{
		dir:     dir,
		path:    filepath.Join(dir, inflightPrefix+strconv.Itoa(pid)+".json"),
		running: map[string]inflightTarget{},
	}
}

// start records a target as running and returns the function that clears it.
func (i *inflight) start(project, target string) func() {
	if i == nil {
		return func() {}
	}
	key := project + "\x00" + target
	host, _ := os.Hostname()

	i.mu.Lock()
	i.running[key] = inflightTarget{
		Project: project, Target: target,
		Pid: os.Getpid(), Host: host, Started: time.Now(),
	}
	i.flushLocked()
	i.mu.Unlock()

	return func() {
		i.mu.Lock()
		delete(i.running, key)
		i.flushLocked()
		i.mu.Unlock()
	}
}

// flushLocked writes this process's set. The caller holds mu for the WHOLE operation:
// releasing it around the write let a stale snapshot land after a fresh one, so a clean
// exit could leave a file claiming a target was still running.
//
// Errors are dropped on purpose - a run must not fail because its post-mortem breadcrumb
// could not be written - but the drop is why the caller logs at debug when it matters.
func (i *inflight) flushLocked() {
	targets := make([]inflightTarget, 0, len(i.running))
	for _, u := range i.running {
		targets = append(targets, u)
	}
	sortTargets(targets)

	if len(targets) == 0 {
		// Nothing running: remove rather than write an empty file, so a clean exit
		// leaves no corpse for the next run to have to reason about.
		_ = os.Remove(i.path)
		return
	}
	b, err := json.Marshal(targets)
	if err != nil {
		return
	}
	// A unique temp name: a fixed one is shared by every writer in the directory, and
	// two renames over it interleave into a truncated file that parses as nothing.
	tmp, err := os.CreateTemp(i.dir, inflightPrefix+"*.tmp")
	if err != nil {
		return
	}
	name := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	if err := os.Rename(name, i.path); err != nil {
		_ = os.Remove(name)
	}
}

// Running reports what this process currently has in flight, for a test and for any
// future caller that wants to say what a cancellation is abandoning. Nothing in the run
// path reads it today.
func (i *inflight) Running() []inflightTarget {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	out := make([]inflightTarget, 0, len(i.running))
	for _, u := range i.running {
		out = append(out, u)
	}
	sortTargets(out)
	return out
}

func sortTargets(t []inflightTarget) {
	slices.SortFunc(t, func(a, b inflightTarget) int {
		if a.Project != b.Project {
			return strings.Compare(a.Project, b.Project)
		}
		return strings.Compare(a.Target, b.Target)
	})
}

// takeAbandoned returns targets a PREVIOUS magus left recorded as running, and deletes
// only those runs' files, so a live peer's record is untouched. It is destructive by
// design: the report fires once, for the run that finds it.
//
// A file is a corpse when its pid is not running on THIS host. The host check matters
// because a CI cache is tarred and restored onto a fresh runner, where the recorded pid
// is either free or belongs to something unrelated.
func (i *inflight) takeAbandoned() []inflightTarget {
	if i == nil {
		return nil
	}
	entries, err := os.ReadDir(i.dir)
	if err != nil {
		return nil
	}
	host, _ := os.Hostname()
	self := os.Getpid()

	var dead []inflightTarget
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, inflightPrefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		path := filepath.Join(i.dir, name)
		if path == i.path {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var prev []inflightTarget
		if json.Unmarshal(b, &prev) != nil || len(prev) == 0 {
			// Unreadable or empty: nothing to report, but it is still litter from a
			// run that is over, so do not leave it to be re-read every time.
			_ = os.Remove(path)
			continue
		}
		// Every target in one file shares a pid, so one liveness check settles the file.
		// Both conditions are host-scoped: a pid from another machine says nothing about
		// this one, and CI restores a cache dir onto a fresh runner where the recorded
		// number is free or belongs to something unrelated. Testing the pid first, as an
		// unconditional short-circuit, made exactly that case invisible.
		p := prev[0]
		if p.Host == host && (p.Pid == self || processAlive(p.Pid)) {
			continue // ourselves, or a live peer mid-run
		}
		dead = append(dead, prev...)
		_ = os.Remove(path)
	}
	sortTargets(dead)
	return dead
}

// abandonedMessage renders the post-mortem, or "" when nothing was left behind. It names
// how long the target had been running: a target killed after ten minutes and one killed
// on contact are different problems, and the timestamp is the only thing on hand that
// says which.
func abandonedMessage(targets []inflightTarget, now time.Time) string {
	if len(targets) == 0 {
		return ""
	}
	const show = 5
	parts := make([]string, 0, min(len(targets), show))
	for _, t := range targets[:min(len(targets), show)] {
		parts = append(parts, fmt.Sprintf("%s %s (%s in)",
			t.Project, t.Target, now.Sub(t.Started).Round(time.Second)))
	}
	list := strings.Join(parts, ", ")
	if len(targets) > show {
		list = fmt.Sprintf("%s and %d more", list, len(targets)-show)
	}
	return fmt.Sprintf(
		"a previous run was killed while running %d target(s): %s. Nothing reported it "+
			"because the process got no chance to; on CI that is usually the OOM killer",
		len(targets), list)
}
