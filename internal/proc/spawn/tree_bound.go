//go:build linux || darwin

package spawn

import (
	"cmp"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// defaultMaxTreeProcs bounds the live processes one command's tree may hold at
// once. macOS's default kern.maxprocperuid is 2666, and a tree near it hangs
// the desktop: a re-exec'ing test reached 2,311 on 2026-11-05.
const defaultMaxTreeProcs = 1024

// boundEvery is how often every followed tree is counted.
const boundEvery = 50 * time.Millisecond

// termGrace is how long a command's leftover descendants get between SIGTERM
// and SIGKILL once the command has exited.
const termGrace = 500 * time.Millisecond

// ErrTooManyProcesses is a command whose process tree passed its bound.
var ErrTooManyProcesses = errors.New("process tree over its bound")

// procInfo is one process as a table read saw it. start tells a reused pid
// from the process that held it before.
type procInfo struct {
	pid, ppid int
	start     int64
	comm      string
	zombie    bool
}

// procTable is one read of the host's processes.
type procTable interface {
	lookup(pid int) (procInfo, bool)
	children(pid int) []procInfo
	// marked is every live process started at or after since whose
	// environment names mark in TreeMarkEnv.
	marked(mark string, since int64) []procInfo
}

type treeKey struct {
	pid   int
	start int64
}

// boundedTree is one command's tree. known holds every live member last seen,
// so a member reparented by setsid or its parent's exit stays followed.
type boundedTree struct {
	root      int
	rootStart int64
	mark      string
	command   string
	known     map[treeKey]bool
	over      int
	top       string
}

// treeBounds follows every running command's tree on one ticker and one table
// read per tick, as processSampler does for memory. limit is read under mu.
type treeBounds struct {
	mu      sync.Mutex
	trees   map[*boundedTree]struct{}
	read    func() (procTable, error)
	running bool
	limit   int
	seq     int
	warned  sync.Once
	lost    sync.Once
}

var processBounds = &treeBounds{trees: map[*boundedTree]struct{}{}, read: readProcTable, limit: defaultMaxTreeProcs}

// setLimit changes the bound for trees counted from now on.
func (b *treeBounds) setLimit(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limit = n
}

// marked returns env with a fresh tree mark appended to TreeMarkEnv, and the
// mark. A nil env is this process's own, as os/exec reads it.
func (b *treeBounds) marked(env []string) ([]string, string) {
	b.mu.Lock()
	b.seq++
	mark := fmt.Sprintf("%d.%d", os.Getpid(), b.seq)
	b.mu.Unlock()
	if env == nil {
		env = os.Environ()
	}
	value := mark
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, TreeMarkEnv+"="); ok && v != "" {
			value = v + "," + mark
			continue
		}
		out = append(out, kv)
	}
	return append(out, TreeMarkEnv+"="+value), mark
}

// follow starts bounding the tree rooted at pid. It returns nil, and says so
// once, where the host cannot list its processes.
func (b *treeBounds) follow(pid int, command, mark string) *boundedTree {
	tbl, err := b.read()
	if err != nil {
		b.warned.Do(func() {
			slog.Warn(fmt.Sprintf("this host cannot list processes (%s), so no command's process tree is bounded or reaped past its group", err))
		})
		return nil
	}
	t := &boundedTree{root: pid, mark: mark, command: command, known: map[treeKey]bool{}}
	if p, ok := tbl.lookup(pid); ok {
		t.rootStart = p.start
	}
	t.scan(tbl)
	b.mu.Lock()
	defer b.mu.Unlock()
	b.trees[t] = struct{}{}
	if !b.running {
		b.running = true
		go b.loop()
	}
	return t
}

func (b *treeBounds) loop() {
	tick := time.NewTicker(boundEvery)
	defer tick.Stop()
	for range tick.C {
		b.mu.Lock()
		if len(b.trees) == 0 {
			b.running = false
			b.mu.Unlock()
			return
		}
		trees := slices.Collect(maps.Keys(b.trees))
		b.mu.Unlock()
		tbl, err := b.read()
		if err != nil {
			b.lost.Do(func() {
				slog.Warn(fmt.Sprintf("magus stopped reading the process table (%s), so running commands' trees are no longer bounded", err))
			})
			continue
		}
		for _, t := range trees {
			b.mu.Lock()
			alive := t.scan(tbl)
			if t.over == 0 && len(alive) > b.limit {
				t.over, t.top = len(alive), mostCommon(alive)
			}
			// Past the bound every tick kills again, since a member can fork
			// between the read and its kill.
			if t.over > 0 {
				signalAll(alive, syscall.SIGKILL)
			}
			b.mu.Unlock()
		}
	}
}

// killTree ends every process still carrying t's mark once its command
// exits: SIGTERM, then SIGKILL after termGrace. The mark alone decides, so no
// tick timing does, and a process that drops it to outlive the run is spared.
func (b *treeBounds) killTree(t *boundedTree) {
	if t == nil || t.mark == "" {
		return
	}
	left := b.leftover(t)
	if len(left) == 0 {
		return
	}
	signalAll(left, syscall.SIGTERM)
	for deadline := time.Now().Add(termGrace); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if left = b.leftover(t); len(left) == 0 {
			return
		}
	}
	for range 8 {
		if left = b.leftover(t); len(left) == 0 {
			return
		}
		signalAll(left, syscall.SIGKILL)
	}
}

// leftover is every live process carrying t's mark, nil when the table cannot
// be read.
func (b *treeBounds) leftover(t *boundedTree) []procInfo {
	tbl, err := b.read()
	if err != nil {
		return nil
	}
	return tbl.marked(t.mark, t.rootStart)
}

// stop ends bounding t, kills what of it still lives, and returns the error a
// tree that passed its bound earns.
func (b *treeBounds) stop(t *boundedTree) error {
	if t == nil {
		return nil
	}
	b.killTree(t)
	b.mu.Lock()
	delete(b.trees, t)
	over, top, limit := t.over, t.top, b.limit
	b.mu.Unlock()
	if over == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s held %d processes at once, most of them %s, so magus killed every one; a command may hold %d",
		ErrTooManyProcesses, t.command, over, top, limit)
}

// scan walks t from every member still alive under its recorded start, adds
// what they started, and forgets the dead. It returns the live members. The
// root is taken only under its start at follow, so a reused pid is not.
func (t *boundedTree) scan(tbl procTable) []procInfo {
	var stack []procInfo
	if len(t.known) == 0 {
		if p, ok := tbl.lookup(t.root); ok && (t.rootStart == 0 || p.start == t.rootStart) {
			stack = append(stack, p)
		}
	}
	for k := range t.known {
		if p, ok := tbl.lookup(k.pid); ok && p.start == k.start {
			stack = append(stack, p)
		}
	}
	seen := map[treeKey]bool{}
	var alive []procInfo
	for len(stack) > 0 {
		p := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		k := treeKey{pid: p.pid, start: p.start}
		if seen[k] {
			continue
		}
		seen[k] = true
		if !p.zombie {
			alive = append(alive, p)
		}
		stack = append(stack, tbl.children(p.pid)...)
	}
	t.known = seen
	return alive
}

func signalAll(procs []procInfo, sig syscall.Signal) {
	for _, p := range procs {
		_ = syscall.Kill(p.pid, sig)
	}
}

// hasMark reports whether a TreeMarkEnv value names mark.
func hasMark(value, mark string) bool {
	return slices.Contains(strings.Split(value, ","), mark)
}

// mostCommon is the command name most of procs run. procs is never empty: it
// is a tree past its bound.
func mostCommon(procs []procInfo) string {
	counts := map[string]int{}
	for _, p := range procs {
		counts[p.comm]++
	}
	names := slices.Collect(maps.Keys(counts))
	slices.SortFunc(names, func(a, b string) int { return cmp.Or(counts[b]-counts[a], cmp.Compare(a, b)) })
	return names[0]
}
