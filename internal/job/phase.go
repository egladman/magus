package job

import (
	"fmt"
	"time"
)

// Warm-cache bounds of the phases a fork and a store read time. A phase past its bound is
// reported, never refused: slowness is not wrongness, and a refusal would strand the fork.
const (
	// landingSweepBound covers one probe round: landingBudget probes of a few VCS
	// subprocesses each.
	landingSweepBound = 5 * time.Second
	// forkTreeBound covers reading the shared checkout's files for a fork's write paths.
	forkTreeBound = 5 * time.Second
	// forkFootprintBound covers reading each live holder's diff to harden its soft paths.
	forkFootprintBound = 10 * time.Second
)

// slowLine is the one line a phase past its bound prints, "" when took stayed within it.
func slowLine(phase string, took, bound time.Duration, cause string) string {
	if took <= bound {
		return ""
	}
	return fmt.Sprintf("slow: %s took %s against a %s bound; likely %s", phase, took.Round(time.Millisecond), bound, cause)
}

// phase starts timing name on the store's clock and returns the stop that, when the phase
// ran past bound, writes its [slowLine] to the store's notices. cause is read only then,
// so the common fast phase pays for no description.
func (s *Store) phase(name string, bound time.Duration, cause func() string) (stop func()) {
	began := s.now()
	return func() {
		if line := slowLine(name, s.now().Sub(began), bound, cause()); line != "" {
			fmt.Fprintf(s.noticeWriter(), "job: %s\n", line)
		}
	}
}
