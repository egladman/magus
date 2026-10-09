package changeset

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// errNoStateDir is what a Store with no state directory answers to a reading mark. Every other
// persisted mark is best-effort, but a person who pressed "I am reading this now" and was not
// recorded would be told nothing by the missing report later, so this one says so.
var errNoStateDir = errors.New("no state directory to record that a review is being read")

// Reading is the local fact that a person has said they are reading a review right now.
//
// It is the reader's own mark and nothing else: magus never tells the forge, and the merge queue
// does not look at it. It exists so that a merge landing under the reader can be reported, and the
// time lost to it measured, by whichever process notices the merge.
type Reading struct {
	// Review is the [types.ReviewTarget] ID the mark was set against. A mark for one review says
	// nothing about another, so a branch that moves to a new pull request starts unmarked.
	Review string `json:"review"`
	// Repo is [types.ReviewTarget].Repo, kept so two forges that number their reviews alike do not
	// share a mark.
	Repo string `json:"repo,omitempty"`
	// Since is when the reading began, in unix milliseconds.
	Since int64 `json:"since"`
}

// Active reports whether r records a reading at all.
func (r Reading) Active() bool { return r.Review != "" }

// Matches reports whether r was set against at.
func (r Reading) Matches(at types.ReviewTarget) bool {
	return r.Active() && r.Review == at.ID && r.Repo == at.Repo
}

// Elapsed is how long the reader had been reading as of now, never negative: a clock that stepped
// backwards between the mark and the merge reads as no time, not as a negative duration in a
// histogram.
func (r Reading) Elapsed(now time.Time) time.Duration {
	return max(0, now.Sub(time.UnixMilli(r.Since)))
}

// readingPath is where the mark lives, beside the seen-thread watermark, or "" when the store
// keeps nothing on disk.
func (s *Store) readingPath() string {
	if s.seenPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.seenPath), "reading.json")
}

// SetReading records that the reader is reading at, as of now, and returns the mark.
//
// Marking a review that is already marked keeps the original start: pressing the key twice, or a
// second tab sending it, must not shorten the interval a later merge is measured against. A mark
// for a different review is replaced; a person reads one review at a time.
func (s *Store) SetReading(ctx context.Context, at types.ReviewTarget, now time.Time) (Reading, error) {
	path := s.readingPath()
	if path == "" {
		return Reading{}, errNoStateDir
	}
	var out Reading
	err := file.Doc[Reading]{Path: path}.Update(ctx, func(stored *Reading) error {
		if stored.Matches(at) {
			out = *stored
			return file.ErrSkipWrite
		}
		*stored = Reading{Review: at.ID, Repo: at.Repo, Since: now.UnixMilli()}
		out = *stored
		return nil
	})
	return out, err
}

// ClearReading removes the mark. Clearing when none is set is not an error.
func (s *Store) ClearReading(ctx context.Context) error {
	path := s.readingPath()
	if path == "" {
		return errNoStateDir
	}
	return file.Doc[Reading]{Path: path}.Update(ctx, func(stored *Reading) error {
		if !stored.Active() {
			return file.ErrSkipWrite
		}
		*stored = Reading{}
		return nil
	})
}

// LoadReading reads the mark WITHOUT a session, for the same reason [Store.LoadSeenThreads]
// exists: the check-review job runs in its own process. An unreadable file reads as no mark, so a
// corrupt one cannot stop the job from reporting a merge on the evidence it does have.
func (s *Store) LoadReading() (Reading, bool) {
	path := s.readingPath()
	if path == "" {
		return Reading{}, false
	}
	r, err := file.Doc[Reading]{Path: path}.Load()
	if err != nil || !r.Active() {
		return Reading{}, false
	}
	return r, true
}
