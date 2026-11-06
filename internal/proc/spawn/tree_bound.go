//go:build linux || darwin

package spawn

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// maxTreeProcs bounds the live processes one command's tree may hold at once.
// macOS's default kern.maxprocperuid is 2666, and a tree near it hangs the
// desktop: a re-exec'ing test reached 2,311 on 2026-11-05. A var so a test can
// lower it.
var maxTreeProcs = 1024

// boundEvery is how often every followed tree is counted. A var so a test can
// shorten it.
var boundEvery = 50 * time.Millisecond

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
}

type treeKey struct {
	pid   int
	start int64
}

// boundedTree is one command's tree. known holds every live member last seen,
// so a member reparented by setsid or its parent's exit stays followed.
type boundedTree struct {
	root    int
	command string
	known   map[treeKey]bool
	over    int
	top     string
}

// treeBounds follows every running command's tree on one ticker and one table
// read per tick, as processSampler does for memory.
type treeBounds struct {
	mu      sync.Mutex
	trees   map[*boundedTree]struct{}
	read    func() (procTable, error)
	running bool
	warned  sync.Once
}

var processBounds = &treeBounds{trees: map[*boundedTree]struct{}{}, read: func() (procTable, error) { return readProcTable() }}

// follow starts bounding the tree rooted at pid. It returns nil, and says so
// once, where the host cannot list its processes.
func (b *treeBounds) follow(ctx context.Context, pid int, command string) *boundedTree {
	tbl, err := b.read()
	if err != nil {
		b.warned.Do(func() {
			slog.WarnContext(ctx, "this host cannot list processes, so no command's process tree is bounded or reaped past its group", attr.Error(err))
		})
		return nil
	}
	t := &boundedTree{root: pid, command: command, known: map[treeKey]bool{}}
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
			continue
		}
		for _, t := range trees {
			b.mu.Lock()
			alive := t.scan(tbl)
			if t.over == 0 && len(alive) > maxTreeProcs {
				t.over, t.top = len(alive), mostCommon(alive)
			}
			// Past the bound every tick kills again, since a member can fork
			// between the read and its kill.
			if t.over > 0 {
				killAll(alive)
			}
			b.mu.Unlock()
		}
	}
}

// killTree kills every live member of t until a read finds none left.
func (b *treeBounds) killTree(t *boundedTree) {
	if t == nil {
		return
	}
	for range 8 {
		tbl, err := b.read()
		if err != nil {
			return
		}
		b.mu.Lock()
		alive := t.scan(tbl)
		b.mu.Unlock()
		if len(alive) == 0 {
			return
		}
		killAll(alive)
	}
}

// stop ends bounding t, kills what of it still lives, and returns the error a
// tree that passed maxTreeProcs earns.
func (b *treeBounds) stop(t *boundedTree) error {
	if t == nil {
		return nil
	}
	b.killTree(t)
	b.mu.Lock()
	delete(b.trees, t)
	over, top := t.over, t.top
	b.mu.Unlock()
	if over == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s held %d processes at once, most of them %s, so magus killed every one, since a command may hold %d",
		ErrTooManyProcesses, t.command, over, top, maxTreeProcs)
}

// ErrTooManyProcesses is a command whose process tree passed maxTreeProcs.
var ErrTooManyProcesses = errors.New("process tree over its bound")

// scan walks t from every member still alive under its recorded start, adds
// what they started, and forgets the dead. It returns the live members.
func (t *boundedTree) scan(tbl procTable) []procInfo {
	var stack []procInfo
	if len(t.known) == 0 {
		if p, ok := tbl.lookup(t.root); ok {
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

func killAll(procs []procInfo) {
	for _, p := range procs {
		_ = syscall.Kill(p.pid, syscall.SIGKILL)
	}
}

// mostCommon is the command name most of procs run.
func mostCommon(procs []procInfo) string {
	counts := map[string]int{}
	for _, p := range procs {
		counts[p.comm]++
	}
	names := slices.Collect(maps.Keys(counts))
	slices.SortFunc(names, func(a, b string) int { return cmp.Or(counts[b]-counts[a], cmp.Compare(a, b)) })
	if len(names) == 0 {
		return "an unnamed command"
	}
	return names[0]
}
