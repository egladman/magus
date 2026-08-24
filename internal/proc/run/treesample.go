package run

import (
	"sync"
	"time"

	"github.com/egladman/magus/internal/sys/mem"
)

// treeSampleEvery is how often a running command's process tree is measured.
//
// A compromise, and worth naming as one. The kernel's own figure (wait4's
// ru_maxrss) is exact but folds a subtree as a MAXIMUM, so it reports the largest
// single process rather than what the tree held together; sampling sums the tree
// but only sees the instants it looks at. Neither is complete, which is why the
// two are combined rather than one replacing the other.
//
// 200ms because the cost is per sample and the whole process table is read to find
// the tree: measured at roughly 1.5ms for every process on a busy machine, so this
// is under 1% of one core, and a command shorter than one tick pays nothing at all
// because the first sample lands after the first tick.
const treeSampleEvery = 200 * time.Millisecond

// treeSampler follows a running command's process tree and remembers the largest
// total it saw.
//
// It exists because a build step is a TREE and the kernel will not total one for
// us: `go test` runs package binaries concurrently, `make -j` runs recipes
// concurrently, and for both the interesting number is what was resident at once.
type treeSampler struct {
	done chan struct{}
	wg   sync.WaitGroup

	mu   sync.Mutex
	peak int64
}

// newTreeSampler creates a sampler that is not yet following anything. stop is
// safe on one that never was, which is the path a command that failed to start
// takes.
func newTreeSampler() *treeSampler {
	return &treeSampler{done: make(chan struct{})}
}

// follow begins sampling the tree rooted at pid.
//
// A pid by value rather than the *exec.Cmd, because reading c.Process from another
// goroutine races with the Start that writes it. That is not theoretical: the
// first version of this file did exactly that and the race detector caught it on
// every -race run.
func (s *treeSampler) follow(pid int) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(treeSampleEvery)
		defer tick.Stop()
		for {
			select {
			case <-s.done:
				return
			case <-tick.C:
				if n := mem.TreeBytes(pid); n > s.peakSoFar() {
					s.record(n)
				}
			}
		}
	}()
}

// stop ends sampling and returns the largest tree total observed, or 0 when the
// command was too short to sample or the host cannot report one.
func (s *treeSampler) stop() int64 {
	close(s.done)
	s.wg.Wait()
	return s.peakSoFar()
}

func (s *treeSampler) record(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > s.peak {
		s.peak = n
	}
}

func (s *treeSampler) peakSoFar() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peak
}
