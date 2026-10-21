package spawn

import (
	"sync"
	"time"

	"github.com/egladman/magus/internal/sys/mem"
	"github.com/egladman/magus/types"
)

// treeSampleEvery is how often a running command's process tree is measured.
//
// A compromise, and worth naming as one. The kernel's own figure (wait4's
// ru_maxrss) is exact but folds a subtree as a MAXIMUM, so it reports the largest
// single process rather than what the tree held together; sampling sums the tree
// but only sees the instants it looks at. Neither is complete, which is why the
// two are combined rather than one replacing the other.
//
// 200ms because the cost is per sample and, on darwin, the whole process table is
// read to find a tree: roughly 1.5ms on a busy machine, under 1% of one core. A
// command shorter than one tick pays nothing, because the first sample lands after
// the first tick.
const treeSampleEvery = 200 * time.Millisecond

// processSampler is the one sampler every exec in this process shares, so ten
// concurrent execs wake on one ticker rather than ten.
var processSampler = newTreeSampler(mem.TreeBytes, newTicker)

func newTicker() (<-chan time.Time, func()) {
	t := time.NewTicker(treeSampleEvery)
	return t.C, t.Stop
}

// treeSampler keeps every sample of each followed process tree, because the
// kernel will not total a tree: `go test` and `make -j` run children at once. One
// goroutine runs while any root is followed and owns the samples; stop asks it for
// them, so a tick it already received is recorded first.
type treeSampler struct {
	read  func(pid int) int64
	ticks func() (<-chan time.Time, func())
	leave chan *sampledRoot

	mu      sync.Mutex
	roots   map[*sampledRoot]struct{}
	running bool
}

// sampledRoot is one followed tree. A pointer rather than the pid keys it, because
// a pid can be reused while an earlier holder is still being stopped.
type sampledRoot struct {
	pid     int
	start   time.Time
	samples []types.MemorySample
	done    chan []types.MemorySample
}

func newTreeSampler(read func(pid int) int64, ticks func() (<-chan time.Time, func())) *treeSampler {
	return &treeSampler{
		read:  read,
		ticks: ticks,
		leave: make(chan *sampledRoot),
		roots: map[*sampledRoot]struct{}{},
	}
}

// follow begins sampling the tree rooted at pid, timing samples from start. A pid
// by value rather than the *exec.Cmd, because reading c.Process from another
// goroutine races with the Start that writes it.
func (s *treeSampler) follow(pid int, start time.Time) *sampledRoot {
	r := &sampledRoot{pid: pid, start: start, done: make(chan []types.MemorySample, 1)}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.roots[r] = struct{}{}
	if !s.running {
		s.running = true
		go s.loop()
	}
	return r
}

// stop ends sampling r and returns its samples: none when the command was shorter
// than a tick, the host cannot read a tree, or r is nil because it never started.
func (s *treeSampler) stop(r *sampledRoot) []types.MemorySample {
	if r == nil {
		return nil
	}
	s.leave <- r
	return <-r.done
}

func (s *treeSampler) loop() {
	tick, stopTicker := s.ticks()
	defer stopTicker()
	for {
		select {
		case now := <-tick:
			s.mu.Lock()
			roots := make([]*sampledRoot, 0, len(s.roots))
			for r := range s.roots {
				roots = append(roots, r)
			}
			s.mu.Unlock()
			for _, r := range roots {
				if n := s.read(r.pid); n > 0 {
					r.samples = append(r.samples, types.MemorySample{At: max(now.Sub(r.start), 0), Bytes: n})
				}
			}
		case r := <-s.leave:
			r.done <- r.samples
			s.mu.Lock()
			delete(s.roots, r)
			idle := len(s.roots) == 0
			if idle {
				s.running = false
			}
			s.mu.Unlock()
			if idle {
				return
			}
		}
	}
}

func samplePeak(samples []types.MemorySample) int64 {
	var peak int64
	for _, s := range samples {
		peak = max(peak, s.Bytes)
	}
	return peak
}
