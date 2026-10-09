package changeset

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/types"
)

// ReadingTTL is how long a [ReadingMark] counts. Twelve hours is a long working day: a mark
// older than that is a forgotten tab, and honoring it would report a merge as time lost that
// nobody spent while the job asked the forge about the review on every tick.
const ReadingTTL = 12 * time.Hour

// ReadingMark is the local fact that a person has said they are reading a review right now.
//
// It is the reader's own mark and nothing else: magus never tells the forge, and the merge queue
// does not look at it. It exists so that a merge landing under the reader can be reported, and the
// time lost to it measured, by whichever process notices the merge.
type ReadingMark struct {
	// Review is the [types.ReviewTarget] ID the mark was set against. A mark for one review says
	// nothing about another, so a branch that moves to a new pull request starts unmarked.
	Review string `json:"review"`
	// Repo is [types.ReviewTarget].Repo, kept so two forges that number their reviews alike do not
	// share a mark.
	Repo string `json:"repo,omitempty"`
	// Since is when the reading began, in unix milliseconds.
	Since int64 `json:"since"`
}

// Active reports whether m records a reading at all.
func (m ReadingMark) Active() bool { return m.Review != "" }

// Matches reports whether m was set against at.
func (m ReadingMark) Matches(at types.ReviewTarget) bool {
	return m.Active() && m.Review == at.ID && m.Repo == at.Repo
}

// Expired reports whether m is older than [ReadingTTL] as of now. A clock that stepped backwards
// never expires a mark.
func (m ReadingMark) Expired(now time.Time) bool { return m.Elapsed(now) > ReadingTTL }

// Elapsed is how long the reader had been reading as of now, never negative: a clock that stepped
// backwards between the mark and the merge reads as no time, not as a negative duration in a
// histogram.
//
// The forge reports no merge time, so now is when a process NOTICED the merge. The figure
// overstates the time lost by up to the check-review job's interval.
func (m ReadingMark) Elapsed(now time.Time) time.Duration {
	return max(0, now.Sub(time.UnixMilli(m.Since)))
}

// readingPath is where the mark lives, beside the seen-comment watermark, or "" when the store
// keeps nothing on disk.
func (s *Store) readingPath() string {
	if s.seenPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.seenPath), "reading.json")
}

// errNoReadingDir is [ErrNoStateDir] with the consequence spelled out. Every other persisted mark
// is best-effort, but a person who pressed "I am reading this now" and was not recorded would be
// told nothing by the missing report later, so this one says so.
func errNoReadingDir() error {
	return fmt.Errorf("record that a review is being read: %w", ErrNoStateDir)
}

// SetReading records that the reader is reading at, as of now, and returns the mark.
//
// Marking a review that is already marked keeps the original start: pressing the key twice, or a
// second tab sending it, must not shorten the interval a later merge is measured against. A mark
// for a different review is replaced, and so is one that has outlived [ReadingTTL]; a person
// reads one review at a time.
func (s *Store) SetReading(ctx context.Context, at types.ReviewTarget, now time.Time) (ReadingMark, error) {
	path := s.readingPath()
	if path == "" {
		return ReadingMark{}, errNoReadingDir()
	}
	var out ReadingMark
	err := file.Doc[ReadingMark]{Path: path}.Update(ctx, func(stored *ReadingMark) error {
		if stored.Matches(at) && !stored.Expired(now) {
			out = *stored
			return file.ErrSkipWrite
		}
		*stored = ReadingMark{Review: at.ID, Repo: at.Repo, Since: now.UnixMilli()}
		out = *stored
		return nil
	})
	return out, err
}

// ClearReading removes the mark. Clearing when none is set is not an error.
func (s *Store) ClearReading(ctx context.Context) error {
	_, err := s.clearReading(ctx, func(ReadingMark) bool { return true })
	return err
}

// ClearReadingIf removes the mark only if it is still exactly mark, and reports whether it did.
//
// A process that read the mark, spent time asking the forge, and then cleared unconditionally
// would delete a mark the reader set on another review in between. The comparison runs inside
// the file's lock so no write can land between the check and the removal.
func (s *Store) ClearReadingIf(ctx context.Context, mark ReadingMark) (bool, error) {
	return s.clearReading(ctx, func(stored ReadingMark) bool { return stored == mark })
}

func (s *Store) clearReading(ctx context.Context, want func(ReadingMark) bool) (bool, error) {
	path := s.readingPath()
	if path == "" {
		return false, errNoReadingDir()
	}
	cleared := false
	err := file.Doc[ReadingMark]{Path: path}.Update(ctx, func(stored *ReadingMark) error {
		if !stored.Active() || !want(*stored) {
			return file.ErrSkipWrite
		}
		*stored = ReadingMark{}
		cleared = true
		return nil
	})
	return cleared && err == nil, err
}

// LoadReading reads the mark WITHOUT a session, for the same reason [Store.LoadSeenComments]
// exists: the check-review job runs in its own process. An unreadable file reads as no mark (the
// zero mark, which is not [ReadingMark.Active]), so a corrupt one cannot stop the job from
// reporting a merge on the evidence it does have.
func (s *Store) LoadReading() ReadingMark {
	path := s.readingPath()
	if path == "" {
		return ReadingMark{}
	}
	m, err := file.Doc[ReadingMark]{Path: path}.Load()
	if err != nil {
		return ReadingMark{}
	}
	return m
}
