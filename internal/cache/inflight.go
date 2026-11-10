package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sys/pid"
)

// inflight records which targets are RUNNING, on disk, so the answer survives a death
// the process cannot report on.
//
// magus already reports a target that fails and a run that is cancelled. The case with
// no reporter is the run that is KILLED: SIGKILL cannot be trapped, so a magus the OOM
// killer takes leaves no last words and a CI job stops mid-log. That happened
// here: a shard died after "[pass] magus lint" with nothing after it, and the only way
// to learn what it had been doing was to guess.
//
// A live snapshot cannot answer that, because it dies with the process. So this writes
// through to a file on every edge, and the NEXT run reports what it finds. The reporter
// is a later invocation rather than the dying one, which is the only design that works
// when the dying one gets no cycles at all.
//
// The file is JSON Lines: a start line per target, a stop line when it ends.
// The last line per key wins, so a key with no stop line is still running.
//
// ONE FILE PER PID, not one shared file. Several magus processes share a cache dir
// routinely (a recursive magus\run, the server serving two dispatches, an agent beside a
// human), and a shared file means each rewrite erases the others, which would delete
// exactly the record a peer is about to need. Per-pid also makes every write single-writer,
// so a reader never sees a torn set.
type inflight struct {
	dir  string // directory holding inflight-<pid>.jsonl for every live run
	path string // this process's own file

	mu      sync.Mutex
	running map[uint64]inflightTarget
	next    uint64 // keys each start: one target can run twice at once (other charms or args)
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

// inflightEvent is one line of the log. A stop line has no Target.
type inflightEvent struct {
	Op     string          `json:"op"` // inflightStart or inflightStop
	Key    uint64          `json:"key"`
	Target *inflightTarget `json:"target,omitempty"`
}

const (
	inflightPrefix = "inflight-"
	inflightExt    = ".jsonl"
	inflightStart  = "start"
	inflightStop   = "stop"
)

// RunningTarget is one target executing in this process.
type RunningTarget struct {
	Project string // repo-relative project directory, "." for the root
	Target  string
	Started time.Time
}

// InFlight returns the targets this process is executing right now, across every Cache
// it opened, sorted by project then target; nil when none. A cache replay counts while
// it holds its seat. It is what a stop would discard, so a Ctrl+C handler asks it before
// deciding whether a press needs confirming. That handler starts before any workspace
// loads and cannot reach a Cache, hence process scope. Safe for concurrent use.
func InFlight() []RunningTarget {
	executing.mu.Lock()
	defer executing.mu.Unlock()
	if len(executing.targets) == 0 {
		return nil
	}
	out := make([]RunningTarget, 0, len(executing.targets))
	for _, t := range executing.targets {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b RunningTarget) int {
		if a.Project != b.Project {
			return strings.Compare(a.Project, b.Project)
		}
		return strings.Compare(a.Target, b.Target)
	})
	return out
}

// executing backs [InFlight].
var executing = processInflight{targets: map[uint64]RunningTarget{}}

type processInflight struct {
	mu      sync.Mutex
	next    uint64
	targets map[uint64]RunningTarget
}

// add records t and returns the function that clears that record alone.
func (p *processInflight) add(t RunningTarget) func() {
	p.mu.Lock()
	p.next++
	key := p.next
	p.targets[key] = t
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.targets, key)
		p.mu.Unlock()
	}
}

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
		path:    filepath.Join(dir, fmt.Sprintf("%s%s%s", inflightPrefix, strconv.Itoa(pid), inflightExt)),
		running: map[uint64]inflightTarget{},
	}
}

// start records a target as running and returns the function that clears it, and only
// it: a second concurrent run of the same project and target has its own record.
func (i *inflight) start(project, target string) func() {
	if i == nil {
		return func() {}
	}
	host, _ := os.Hostname()

	started := time.Now()

	i.mu.Lock()
	i.next++
	key := i.next
	t := inflightTarget{
		Project: project, Target: target,
		Pid: os.Getpid(), Host: host, Started: started,
	}
	i.running[key] = t
	i.appendLocked(inflightEvent{Op: inflightStart, Key: key, Target: &t})
	i.mu.Unlock()
	clearProcess := executing.add(RunningTarget{Project: project, Target: target, Started: started})

	return func() {
		clearProcess()
		i.mu.Lock()
		delete(i.running, key)
		if len(i.running) == 0 {
			// Remove rather than leave a log of finished work, so a clean exit
			// leaves no corpse for the next run to reason about.
			_ = os.Remove(i.path)
		} else {
			i.appendLocked(inflightEvent{Op: inflightStop, Key: key})
		}
		i.mu.Unlock()
	}
}

// appendLocked adds a line to this process's log. The caller holds mu, so
// lines land in edge order. Errors are dropped: a run must not fail because
// its breadcrumb could not be written.
func (i *inflight) appendLocked(e inflightEvent) {
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	// Unsynced: the kernel keeps a killed process's writes.
	_ = file.AppendLines(i.path, [][]byte{b}, 0o600, file.WithoutSync())
}

// replayInflight returns the targets with a start line and no stop line.
// A line that does not parse, such as a torn last one, is skipped.
func replayInflight(b []byte) []inflightTarget {
	live := map[uint64]inflightTarget{}
	for line := range strings.SplitSeq(string(b), "\n") {
		var e inflightEvent
		if line == "" || json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		switch {
		case e.Op == inflightStart && e.Target != nil:
			live[e.Key] = *e.Target
		case e.Op == inflightStop:
			delete(live, e.Key)
		}
	}
	out := make([]inflightTarget, 0, len(live))
	for _, t := range live {
		out = append(out, t)
	}
	sortTargets(out)
	return out
}

// Running reports what this tracker currently has in flight. [InFlight] is the
// process-wide view a cancellation reads.
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
		if isStale(i.dir, e, time.Now()) {
			_ = os.RemoveAll(filepath.Join(i.dir, name))
			continue
		}
		if !strings.HasPrefix(name, inflightPrefix) || !strings.HasSuffix(name, inflightExt) {
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
		prev := replayInflight(b)
		if len(prev) == 0 {
			// Nothing running in it: litter from a run that is over, so do not
			// leave it to be re-read every time.
			_ = os.Remove(path)
			continue
		}
		// Every target in one file shares a pid, so one liveness check settles the file.
		// Both conditions are host-scoped: a pid from another machine says nothing about
		// this one, and CI restores a cache dir onto a fresh runner where the recorded
		// number is free or belongs to something unrelated. Testing the pid first, as an
		// unconditional short-circuit, made exactly that case invisible.
		p := prev[0]
		if p.Host == host && (p.Pid == self || pid.Alive(p.Pid)) {
			continue // ourselves, or a live peer mid-run
		}
		dead = append(dead, prev...)
		_ = os.Remove(path)
	}
	sortTargets(dead)
	return dead
}

// staleAfter is how old a leftover temp file or staging directory must be before it is
// collected. A live writer holds one for milliseconds, and a staging directory for one
// replay, so an hour is past any of them.
const staleAfter = time.Hour

// isStale reports whether e is litter a killed run left in the cache root.
// That is an old binary's inflight temp file or a remote staging directory.
func isStale(dir string, e os.DirEntry, now time.Time) bool {
	name := e.Name()
	// compat(until: the oldest magus release still in use names its inflight temp
	// file through file.ReplaceFile): older binaries leave inflight-*.tmp behind.
	temp := strings.HasPrefix(name, inflightPrefix) && strings.HasSuffix(name, ".tmp")
	staging := e.IsDir() && strings.HasPrefix(name, stagingPrefix)
	if !temp && !staging {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, name))
	return err == nil && now.Sub(info.ModTime()) > staleAfter
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
