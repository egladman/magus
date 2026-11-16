package session

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

func agentRecord(invocation string, seq uint64, path string) Record {
	return Record{
		V: APIVersion, Invocation: invocation, Seq: seq, Kind: KindAgentEvent, Ts: int64(seq),
		Payload: fmt.Appendf(nil, `{"host":"h","event":"file.read","ref":"%d","at":%d,"text":%q}`, seq, seq, path),
	}
}

type FoldCacheSuite struct {
	suite.Suite
	dir string
}

func TestFoldCache(t *testing.T) { suite.Run(t, new(FoldCacheSuite)) }

func (s *FoldCacheSuite) SetupTest() { s.dir = s.T().TempDir() }

// oracle is ReadAll, narrowed the way FoldKind narrows.
func (s *FoldCacheSuite) oracle(kind string) Fold {
	fold, err := ReadAll(s.dir)
	s.Require().NoError(err)
	var records []Record
	for _, rec := range fold.Records {
		if rec.Kind == kind {
			records = append(records, rec)
		}
	}
	fold.Records = records
	return fold
}

func (s *FoldCacheSuite) TestFoldAgentEventsMatchesReadAll() {
	writeRaw(&s.Suite, s.dir, "a", []Record{
		{V: APIVersion, Invocation: "a", Seq: 1, Kind: KindInvocationStart, Ts: 1},
		agentRecord("a", 2, "x.go"),
	})
	writeRaw(&s.Suite, s.dir, "b", []Record{agentRecord("b", 3, "y.go")})
	legacy := fmt.Sprintf(`{"v":1,"session":"legacy","seq":1,"kind":"session_start","ts":%d,"payload":{}}`+"\n", time.Now().UnixMilli())
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, "legacy"+FileExt), []byte(legacy), 0o644))

	for range 2 { // the miss that writes the cache, then the hit that reads it
		got, err := FoldAgentEvents(s.dir)
		s.Require().NoError(err)
		s.Equal(s.oracle(KindAgentEvent), got)
	}
}

// The point of the cache: an unchanged file is not read again. Rewriting a file's bytes
// while restoring its size and modification time is invisible to the key, so a read that
// still returns the old records proves the file was not opened.
func (s *FoldCacheSuite) TestReadsOnlyFilesThatChanged() {
	writeRaw(&s.Suite, s.dir, "a", []Record{agentRecord("a", 1, "x.go")})
	writeRaw(&s.Suite, s.dir, "b", []Record{agentRecord("b", 2, "y.go")})
	_, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)

	pathA := filepath.Join(s.dir, "a"+FileExt)
	fi, err := os.Stat(pathA)
	s.Require().NoError(err)
	before, err := os.ReadFile(pathA)
	s.Require().NoError(err)
	f, err := os.OpenFile(pathA, os.O_WRONLY, 0)
	s.Require().NoError(err)
	_, err = f.WriteAt([]byte(string(before[:len(before)-2])+"X\n"), 0)
	s.Require().NoError(err)
	s.Require().NoError(f.Close())
	s.Require().NoError(os.Chtimes(pathA, fi.ModTime(), fi.ModTime()))

	w, err := Open(s.dir, "b", InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(KindAgentEvent, AgentEvent{Host: "h", Kind: EventFileWrite, Ref: "w", Text: "z.go"}))

	got, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	s.Require().Len(got.Records, 3, "a's cached record, b's original, and b's append")
	s.Equal("a", got.Records[0].Invocation, "a was served from the cache, not reread")
	s.Zero(got.Skipped, "the damaged line in a was never read")
}

func (s *FoldCacheSuite) TestRefoldsFromATornCache() {
	writeRaw(&s.Suite, s.dir, "a", []Record{agentRecord("a", 1, "x.go")})
	_, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)

	cache := foldCachePath(s.dir, KindAgentEvent)
	whole, err := os.ReadFile(cache)
	s.Require().NoError(err)
	s.Require().NoError(os.WriteFile(cache, whole[:len(whole)/2], 0o644))

	got, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	s.Equal(s.oracle(KindAgentEvent), got)

	repaired, err := os.ReadFile(cache)
	s.Require().NoError(err)
	s.Equal(whole, repaired, "the refold writes the cache back whole")
}

func (s *FoldCacheSuite) TestForgetsAPrunedFile() {
	writeRaw(&s.Suite, s.dir, "a", []Record{agentRecord("a", 1, "x.go")})
	writeRaw(&s.Suite, s.dir, "b", []Record{agentRecord("b", 2, "y.go")})
	_, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)

	s.Require().NoError(os.Remove(filepath.Join(s.dir, "a"+FileExt)))

	got, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	s.Equal(s.oracle(KindAgentEvent), got)
	s.NotContains(readFoldCache(foldCachePath(s.dir, KindAgentEvent), KindAgentEvent).Files, "a"+FileExt)
}

// Each kind keeps its own cache, so a reader of one kind never decodes another, and a
// record appended later is seen by the next read.
func (s *FoldCacheSuite) TestEachKindKeepsItsOwnCache() {
	writeRaw(&s.Suite, s.dir, "a", []Record{agentRecord("a", 1, "x.go")})
	w, err := Open(s.dir, "b", InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(KindTargetResult, TargetResult{Target: "ci", Outcome: OutcomePass}))

	_, err = FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	results, err := FoldKind(s.dir, KindTargetResult)
	s.Require().NoError(err)
	s.Require().Len(results.Records, 1)
	s.Equal(KindTargetResult, results.Records[0].Kind)
	s.Empty(readFoldCache(foldCachePath(s.dir, KindTargetResult), KindTargetResult).Files["a"+FileExt].Records,
		"the result cache holds no agent event")

	s.Require().NoError(w.Append(KindTargetResult, TargetResult{Target: "ci", Outcome: OutcomeFail}))
	results, err = FoldKind(s.dir, KindTargetResult)
	s.Require().NoError(err)
	s.Len(results.Records, 2)
}

// The kind names the cache file, so one that could escape the store is refused, and the
// cache is as private as the invocation files it folds.
func (s *FoldCacheSuite) TestFoldKindRefusesAnEscapingKindAndWritesItsCachePrivate() {
	for _, kind := range []string{"", "../x", "a/b"} {
		_, err := FoldKind(s.dir, kind)
		var invalid *InvalidIDError
		s.Require().ErrorAs(err, &invalid, "kind %q", kind)
		s.Equal("kind", invalid.Field)
	}

	writeRaw(&s.Suite, s.dir, "a", []Record{agentRecord("a", 1, "x.go")})
	_, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	fi, err := os.Stat(foldCachePath(s.dir, KindAgentEvent))
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), fi.Mode().Perm())
}

// Readers and writers race on one store. Each read must equal some fold of the files,
// and once the writers stop, a read must equal the final fold exactly: a cache written
// mid-append cannot have pinned a short one.
func (s *FoldCacheSuite) TestUnderConcurrentReadersAndWriters() {
	const writers, appends = 4, 25
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			w, err := Open(s.dir, fmt.Sprintf("w%d", i), InvocationStart{})
			if !s.NoError(err) {
				return
			}
			for j := range appends {
				s.NoError(w.Append(KindAgentEvent, AgentEvent{Host: "h", Kind: EventFileRead, Ref: fmt.Sprint(j), Text: "f.go"}))
			}
		})
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				got, err := FoldAgentEvents(s.dir)
				if s.NoError(err) {
					s.LessOrEqual(len(got.Records), writers*appends)
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	readers.Wait()

	got, err := FoldAgentEvents(s.dir)
	s.Require().NoError(err)
	s.Len(got.Records, writers*appends)
	s.Equal(s.oracle(KindAgentEvent), got)
}
