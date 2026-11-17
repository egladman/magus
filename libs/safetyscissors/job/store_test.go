package job

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type StoreSuite struct {
	suite.Suite
	dir string
}

func TestStoreSuite(t *testing.T) { suite.Run(t, new(StoreSuite)) }

func (s *StoreSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *StoreSuite) open(opts ...StoreOption) *Store {
	st, err := Open(s.dir, append(writable(), opts...)...)
	s.Require().NoError(err)
	return st
}

// writable is opts with the seams every write needs: an unbound actor and a notice sink.
func writable(opts ...StoreOption) []StoreOption {
	return append([]StoreOption{WithActor(func() Actor { return Actor{} }), WithNotices(func(Notice) {})}, opts...)
}

func (s *StoreSuite) TestAStoreWithNoActorRefusesAWrite() {
	st, err := Open(s.dir, WithNotices(func(Notice) {}))
	s.Require().NoError(err)

	_, err = st.Update(s.T().Context(), "a", func(*Job) {})

	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithActor", refused.Option)
	s.NoFileExists(filepath.Join(s.dir, jobsFileName))
}

func (s *StoreSuite) TestAStoreWithNoNoticesRefusesAWrite() {
	st, err := Open(s.dir, WithActor(func() Actor { return Actor{} }))
	s.Require().NoError(err)

	_, err = st.Update(s.T().Context(), "a", func(*Job) {})

	refused, ok := errors.AsType[*UnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal("WithNotices", refused.Option)
}

func (s *StoreSuite) TestAPinnedActorNeedsNoActorOption() {
	st, err := Open(s.dir, WithNotices(func(Notice) {}))
	s.Require().NoError(err)

	_, err = st.As(Actor{}).Update(s.T().Context(), "a", func(*Job) {})

	s.NoError(err)
}

func (s *StoreSuite) TestARewriteLeavingTwoRowsUnderOneIDIsRefused() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)

	err = st.Rewrite(s.T().Context(), func(f *Rows) error {
		f.Jobs = append(f.Jobs, f.Jobs[0].Clone())
		return nil
	})

	refused, ok := errors.AsType[*DuplicateRowError](err)
	s.Require().True(ok, err)
	s.Equal("a", refused.ID)
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Len(rows, 1)
}

func (s *StoreSuite) TestAWriteReadsTheStoresClock() {
	at := time.Unix(1_700_000_000, 0)
	st := s.open(WithClock(func() time.Time { return at }))

	row, err := st.Update(s.T().Context(), "a", func(*Job) {})

	s.Require().NoError(err)
	s.Equal(at.Unix(), row.Updated)
}

func (s *StoreSuite) TestACancelledReadStopsWaitingOnAnAppendInFlight() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName), []byte(`{"apiVersion":`), 0o600))
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()

	_, err := s.open().List(ctx)

	s.ErrorIs(err, context.Canceled)
}

func (s *StoreSuite) TestOpenRefusesNoDirectory() {
	_, err := Open(" ")

	s.ErrorContains(err, "none was named")
}

func (s *StoreSuite) TestAnUnopenedStoreFailsEveryOperation() {
	cause := errors.New("no state dir")
	st := Unopened(cause)

	_, err := st.List(s.T().Context())
	s.ErrorIs(err, cause)
	_, err = st.Update(s.T().Context(), "a", func(*Job) {})
	s.ErrorIs(err, cause, "an unplaceable store never reads as an empty ledger")
}

func (s *StoreSuite) TestAnAbsentFileIsAnEmptyLedger() {
	rows, err := s.open().List(s.T().Context())

	s.Require().NoError(err)
	s.Empty(rows)
}

func (s *StoreSuite) TestUpdateCreatesAndMergesARow() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Paths.Write = []string{"a.go"} })
	s.Require().NoError(err)

	row, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Parent = "root" })

	s.Require().NoError(err)
	s.Equal(StateDeclared, row.State, "a declaration naming no state is stored declared")
	s.Equal([]string{"a.go"}, row.Paths.Write, "a merge keeps what it does not change")
	s.Equal("root", row.Parent)
	s.Equal(2, row.Generation, "changing the declared spec moves the generation")
}

func (s *StoreSuite) TestARowWithNoIDIsRefused() {
	_, err := s.open().Update(s.T().Context(), " ", func(*Job) {})

	s.ErrorIs(err, ErrNoID)
}

func (s *StoreSuite) TestTheFileIsStampedWithTheModuleGroup() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)

	raw, err := os.ReadFile(filepath.Join(s.dir, jobsFileName))
	s.Require().NoError(err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	s.Require().Len(lines, 2)
	s.Contains(lines[0], `"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList"`)
	s.Contains(lines[1], `"apiVersion":"safetyscissors.job/v1alpha1","kind":"Job"`)
}

func (s *StoreSuite) TestALineOfAnotherGroupIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName),
		[]byte(`{"apiVersion":"batch.magus/v1alpha1","kind":"JobList"}`+"\n"), 0o600))

	_, err := s.open().List(s.T().Context())

	var version *RecordVersionError
	s.Require().ErrorAs(err, &version)
	s.Equal(1, version.Line)
}

func (s *StoreSuite) TestAFileAtANewerMigrationIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName),
		[]byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList","migration":3}`+"\n"), 0o600))

	_, err := s.open(WithMigrations(1, nil)).List(s.T().Context())

	var newer *NewerWriterError
	s.Require().ErrorAs(err, &newer)
	s.Equal(3, newer.Found)
	s.Equal(1, newer.Known)
}

func (s *StoreSuite) TestAnOlderFileIsMigratedUnderTheLock() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, jobsFileName),
		[]byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList"}`+"\n"), 0o600))
	var found []int
	migrate := func(_ context.Context, path string, from int, _ []codec.Line) error {
		found = append(found, from)
		return os.WriteFile(path, []byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"JobList","migration":1}`+"\n"), 0o600)
	}

	_, err := s.open(WithMigrations(1, migrate)).List(s.T().Context())

	s.Require().NoError(err)
	s.Equal([]int{0}, found)
}

func (s *StoreSuite) TestTheDocumentAnOlderWriterKeptIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, documentFileName), []byte(`{}`), 0o600))

	_, err := s.open().List(s.T().Context())

	var legacy *LegacyDocumentError
	s.Require().ErrorAs(err, &legacy)
	s.Equal(filepath.Join(s.dir, documentFileName), legacy.Document)
}

func (s *StoreSuite) TestARowRequiringAnUnknownFeatureIsReadButNotWritten() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)
	s.Require().NoError(st.Rewrite(s.T().Context(), func(f *Rows) error {
		f.Jobs[0].Requires = []string{"never-implemented"}
		return nil
	}))

	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	s.Require().Len(rows, 1)
	_, err = st.Update(s.T().Context(), "a", func(r *Job) { r.Parent = "p" })

	var newer *NewerRecordsError
	s.Require().ErrorAs(err, &newer)
	s.Equal([]string{"never-implemented"}, newer.Lacks)
}

func (s *StoreSuite) TestErrorWrapSeesTheStoresOwnRefusals() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, documentFileName), []byte(`{}`), 0o600))
	coded := errors.New("coded")

	_, err := s.open(WithErrorWrap(func(err error) error { return errors.Join(coded, err) })).List(s.T().Context())

	s.ErrorIs(err, coded)
}

func (s *StoreSuite) TestASkippedRewriteWritesNothing() {
	st := s.open()

	s.Require().NoError(st.Rewrite(s.T().Context(), func(*Rows) error { return ErrSkipWrite }))

	s.NoFileExists(filepath.Join(s.dir, jobsFileName))
}

func (s *StoreSuite) TestRetentionRunsInsideEveryRewrite() {
	var seen int
	st := s.open(WithRetention(func(_ context.Context, f *Rows, _ time.Time) error {
		seen = len(f.Jobs)
		f.Jobs = nil
		return nil
	}))
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)

	rows, err := st.List(s.T().Context())

	s.Require().NoError(err)
	s.Equal(1, seen)
	s.Empty(rows, "the pass dropped the row before the write")
}

func (s *StoreSuite) TestClearArchivesWhatItDrops() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)

	dropped, err := st.Clear(s.T().Context())

	s.Require().NoError(err)
	s.Equal(1, dropped)
	archives, err := filepath.Glob(filepath.Join(s.dir, "jobs-*.jsonl"))
	s.Require().NoError(err)
	s.Len(archives, 1)
}

func (s *StoreSuite) TestAnEntryIsRecordedOnceAndConsumed() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Paths.Write = []string{"internal/x"} })
	s.Require().NoError(err)

	row, err := st.Enter(s.T().Context(), "a", "internal/x/y.go")
	s.Require().NoError(err)
	s.Require().Len(row.Entries, 1)
	_, err = st.Enter(s.T().Context(), "a", "internal/x/y.go")
	s.ErrorContains(err, "already has an entry")

	s.Require().NoError(st.ConsumeEntry(s.T().Context(), "a", "internal/x/y.go"))
	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	_, open := OpenEntry(rows[0], "internal/x/y.go")
	s.False(open, "a consumed entry is no longer open")
}

func (s *StoreSuite) TestAnUnattributedWriteToNoRowIsDropped() {
	s.NoError(s.open().RecordUnattributedWrite(s.T().Context(), "nobody", "a.go"))
}

// The guard notes every write it grades, so an unbounded list would make a long editing
// session against a declared plan grow the store without limit.
func (s *StoreSuite) TestUnattributedWritesKeepTheNewest() {
	st := s.open()
	const over = 5
	var owned []string
	for i := range maxUnattributedWrites + over {
		owned = append(owned, fmt.Sprintf("f%d.go", i))
	}
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Paths.Write = owned })
	s.Require().NoError(err)
	for _, p := range owned {
		s.Require().NoError(st.RecordUnattributedWrite(s.T().Context(), "a", p))
	}

	rows, err := st.List(s.T().Context())
	s.Require().NoError(err)
	kept := rows[0].Unattributed
	s.Require().Len(kept, maxUnattributedWrites)
	s.Equal(fmt.Sprintf("f%d.go", over), kept[0].Path, "the oldest are dropped")
	s.Equal(fmt.Sprintf("f%d.go", maxUnattributedWrites+over-1), kept[len(kept)-1].Path)
}

func (s *StoreSuite) TestAReleaseOfARootlessStoreIsAbsent() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Paths.Write = []string{"a.go"} })
	s.Require().NoError(err)

	row, err := st.Update(s.T().Context(), "a", func(r *Job) { r.Paths.Write = nil })

	s.Require().NoError(err)
	s.Require().Len(row.Releases, 1)
	s.Equal(DigestAbsent, row.Releases[0].Digest)
}

func (s *StoreSuite) TestARewriteDroppingAnUnfinishedAttemptLineIsANotice() {
	var notices []Notice
	st := s.open(WithNotices(func(n Notice) { notices = append(notices, n) }))
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)
	path := filepath.Join(s.dir, attemptsFileName)
	s.Require().NoError(os.WriteFile(path, []byte(`{"apiVersion":`), 0o600))

	s.Require().NoError(st.AppendAttempts(s.T().Context(), Attempt{Job: "a", N: 1}))

	s.Contains(notices, Notice{Path: path, Text: "line 1 was one no writer finished; the rewrite dropped it"})
	tries, err := st.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Len(tries, 1, "the append landed after the rewrite")
}

func (s *StoreSuite) TestAttemptsLiveBesideTheRows() {
	st := s.open()
	_, err := st.Update(s.T().Context(), "a", func(*Job) {})
	s.Require().NoError(err)

	s.Require().NoError(st.AppendAttempts(s.T().Context(), Attempt{Job: "a", N: 1, Status: AttemptStatus{Started: 3}}))
	_, err = st.Delete(s.T().Context(), "a", true)
	s.Require().NoError(err)

	tries, err := st.Attempts(s.T().Context())
	s.Require().NoError(err)
	s.Empty(tries, "a dropped row's tries end with it")
	archives, err := filepath.Glob(filepath.Join(s.dir, "jobs-*.attempts.jsonl"))
	s.Require().NoError(err)
	s.Len(archives, 1, "the archive keeps the dropped row's tries")
}

func (s *StoreSuite) TestAnAttemptOfAnotherVersionIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, attemptsFileName),
		[]byte(`{"apiVersion":"safetyscissors.job/v1alpha99","kind":"Attempt","spec":{"job":"a","n":1},"status":{}}`+"\n"), 0o600))

	_, err := s.open().Attempts(s.T().Context())

	var newer *NewerRecordsError
	s.Require().ErrorAs(err, &newer)
	s.Equal("attempt 1 of a", newer.Record)
}
