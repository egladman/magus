package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

const retention = 30 * 24 * time.Hour

// Kinds the test tie reads: a tied record joins its group, and a held one also keeps
// its group at any age, the way an unanswered request does.
const (
	kindTied = "tied"
	kindHeld = "held"
)

type grouped struct {
	Group string `json:"group"`
}

// testTie ties every tied or held record to its group and holds the groups a held
// record names.
func testTie() Tie {
	group := func(rec Record) string {
		if rec.Kind != kindTied && rec.Kind != kindHeld {
			return ""
		}
		var g grouped
		if codec.Unmarshal(rec.Payload, &g) != nil {
			return ""
		}
		return g.Group
	}
	return Tie{
		Key: group,
		Held: func(fold Fold) []string {
			var out []string
			for _, rec := range fold.Records {
				if rec.Kind == kindHeld {
					out = append(out, group(rec))
				}
			}
			return out
		},
	}
}

// msAgo is a record timestamp age old, in the unix milliseconds a Record carries.
func msAgo(age time.Duration) int64 { return time.Now().Add(-age).UnixMilli() }

type PruneSuite struct {
	suite.Suite
	dir string
}

func TestPrune(t *testing.T) { suite.Run(t, new(PruneSuite)) }

func (s *PruneSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *PruneSuite) record(invocation string, seq uint64, ts int64, kind string, payload any) Record {
	raw, err := codec.Marshal(payload)
	s.Require().NoError(err)
	return Record{V: APIVersion, Invocation: invocation, Seq: seq, Kind: kind, Ts: ts, Payload: raw}
}

func (s *PruneSuite) result(invocation string, seq uint64, age time.Duration) Record {
	return s.record(invocation, seq, msAgo(age), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass})
}

// writeAged lays down an invocation file and backdates its modification time to match.
// Both halves matter and they are not the same knob: pruning DECIDES on the newest
// record, and only reads a file at all once its modification time says it might be old
// enough to be worth reading.
func (s *PruneSuite) writeAged(invocation string, age time.Duration, records []Record) {
	writeRaw(&s.Suite, s.dir, invocation, records)
	when := time.Now().Add(-age)
	s.Require().NoError(os.Chtimes(filepath.Join(s.dir, invocation+FileExt), when, when))
}

// writeLegacy lays down a schema-1 file whose lines are dated at, with its modification
// time backdated to match.
func (s *PruneSuite) writeLegacy(id string, at time.Time) string {
	path := filepath.Join(s.dir, id+FileExt)
	line := fmt.Sprintf(`{"v":1,"session":"%s","seq":1,"kind":"session_start","ts":%d,"payload":{}}`+"\n", id, at.UnixMilli())
	s.Require().NoError(os.WriteFile(path, []byte(line+line), 0o644))
	s.Require().NoError(os.Chtimes(path, at, at))
	return path
}

// stored names the invocation files still on disk, sorted.
func (s *PruneSuite) stored() []string {
	entries, err := os.ReadDir(s.dir)
	s.Require().NoError(err)
	out := []string{}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), FileExt); ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func (s *PruneSuite) read() Fold {
	fold, err := ReadAll(s.T().Context(), s.dir)
	s.Require().NoError(err)
	return fold
}

func (s *PruneSuite) TestDeletesAnInvocationWhoseNewestFactAgedOut() {
	s.writeAged("stale", 40*24*time.Hour, []Record{s.result("stale", 1, 40*24*time.Hour)})
	s.writeAged("fresh", time.Hour, []Record{s.result("fresh", 1, time.Hour)})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention))

	s.Equal([]string{"fresh"}, s.stored())
	s.Len(s.read().Records, 1)
}

// An invocation that has run for months keeps its whole history, back to its first fact.
// Retention is a question about the file, and the answer is delete or leave alone.
func (s *PruneSuite) TestKeepsEveryFactInAFileItSpares() {
	s.writeAged("long-lived", 90*24*time.Hour, []Record{
		s.result("long-lived", 1, 90*24*time.Hour),
		s.result("long-lived", 2, 80*24*time.Hour),
		s.result("long-lived", 3, time.Hour),
	})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention))

	s.Equal([]string{"long-lived"}, s.stored(), "the newest record is inside the window, so the file is not old")
	s.Len(s.read().Records, 3, "a spared file is spared whole")
}

// The modification time is a pre-filter, never the verdict.
func (s *PruneSuite) TestDecidesOnRecordsNotOnTheFilesystemClock() {
	s.writeAged("misdated", 200*24*time.Hour, []Record{s.result("misdated", 1, time.Hour)})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention))

	s.Equal([]string{"misdated"}, s.stored())
}

// The window is measured from the caller's clock.
func (s *PruneSuite) TestAgesByTheCallersClock() {
	s.writeAged("recent", time.Hour, []Record{s.result("recent", 1, time.Hour)})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention, WithClock(func() time.Time { return time.Now().Add(60 * 24 * time.Hour) })))

	s.Empty(s.stored(), "sixty days on, an hour-old fact is past a thirty-day window")
}

// Rule 1: a held group pins every file naming it, however old.
func (s *PruneSuite) TestExemptsAFileHoldingAHeldGroup() {
	age := 200 * 24 * time.Hour
	s.writeAged("blocked", age, []Record{s.record("blocked", 1, msAgo(age), kindHeld, grouped{Group: "g1"})})
	s.writeAged("quiet", age, []Record{s.result("quiet", 1, age)})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention, WithTie(testTie())))

	s.Equal([]string{"blocked"}, s.stored(), "the exemption covers the held file only; its neighbor ages out normally")
}

// Rule 2, the resurrection half: one file of a group is young, so the old one stays.
func (s *PruneSuite) TestKeepsAnOldHalfWhileItsGroupHasAYoungOne() {
	s.writeAged("asker", time.Hour, []Record{
		s.record("asker", 1, msAgo(200*24*time.Hour), kindTied, grouped{Group: "g1"}),
		s.result("asker", 2, time.Hour),
	})
	s.writeAged("answer", 199*24*time.Hour, []Record{s.record("answer", 1, msAgo(199*24*time.Hour), kindTied, grouped{Group: "g1"})})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention, WithTie(testTie())))

	s.Equal([]string{"answer", "asker"}, s.stored())
}

// Without a tie, nothing pairs the two files, and the old half goes alone.
func (s *PruneSuite) TestWithoutATieTheOldHalfGoes() {
	s.writeAged("asker", time.Hour, []Record{s.record("asker", 1, msAgo(time.Hour), kindTied, grouped{Group: "g1"})})
	s.writeAged("answer", 199*24*time.Hour, []Record{s.record("answer", 1, msAgo(199*24*time.Hour), kindTied, grouped{Group: "g1"})})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention))

	s.Equal([]string{"asker"}, s.stored())
}

// Rule 2, the other half: once no file naming a group is young enough to spare, the
// whole group goes at once.
func (s *PruneSuite) TestDeletesAGroupsFilesTogetherOnceAllAgedOut() {
	age := 200 * 24 * time.Hour
	s.writeAged("asker", age, []Record{s.record("asker", 1, msAgo(age), kindTied, grouped{Group: "g1"})})
	s.writeAged("answer", age-time.Hour, []Record{s.record("answer", 1, msAgo(age-time.Hour), kindTied, grouped{Group: "g1"})})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention, WithTie(testTie())))

	s.Empty(s.stored())
}

// Exemptions travel: sparing a file for one group can spare a SECOND group that shares
// it, which in turn spares the file holding that group's other half. One pass in an
// unlucky map order gets this wrong, which is why pruning iterates to a fixed point.
func (s *PruneSuite) TestExemptionPropagatesThroughASharedFile() {
	age := 200 * 24 * time.Hour
	s.writeAged("asker", age, []Record{
		s.record("asker", 1, msAgo(age), kindHeld, grouped{Group: "g-open"}),
		s.record("asker", 2, msAgo(age), kindTied, grouped{Group: "g-done"}),
	})
	s.writeAged("answer", age, []Record{s.record("answer", 1, msAgo(age), kindTied, grouped{Group: "g-done"})})

	s.Require().NoError(Prune(s.T().Context(), s.dir, retention, WithTie(testTie())))

	s.Equal([]string{"answer", "asker"}, s.stored())
}

// Every producer opens, so opening is where the store pays for its own housekeeping.
func (s *PruneSuite) TestOpenPrunesTheStore() {
	s.writeAged("ancient", 400*24*time.Hour, []Record{s.result("ancient", 1, 400*24*time.Hour)})

	_, err := Open(s.T().Context(), s.dir, "current", InvocationStart{Workspace: "/repo"})
	s.Require().NoError(err)

	s.Empty(s.stored())
}

// Open prunes with the tie it was given.
func (s *PruneSuite) TestOpenPrunesWithItsTie() {
	age := 400 * 24 * time.Hour
	s.writeAged("blocked", age, []Record{s.record("blocked", 1, msAgo(age), kindHeld, grouped{Group: "g1"})})

	_, err := Open(s.T().Context(), s.dir, "current", InvocationStart{}, WithTie(testTie()))
	s.Require().NoError(err)

	s.Equal([]string{"blocked"}, s.stored())
}

// Pruning reads every invocation file's metadata, so Open does it at most once per
// interval; a store pruned a moment ago is left alone.
func (s *PruneSuite) TestOpenPrunesAtMostOncePerInterval() {
	_, err := Open(s.T().Context(), s.dir, "first", InvocationStart{})
	s.Require().NoError(err)

	s.writeAged("ancient", 400*24*time.Hour, []Record{s.result("ancient", 1, 400*24*time.Hour)})
	_, err = Open(s.T().Context(), s.dir, "second", InvocationStart{})
	s.Require().NoError(err)
	s.Equal([]string{"ancient"}, s.stored())

	stale := time.Now().Add(-2 * pruneInterval)
	s.Require().NoError(os.Chtimes(filepath.Join(s.dir, pruneStamp), stale, stale))
	_, err = Open(s.T().Context(), s.dir, "third", InvocationStart{})
	s.Require().NoError(err)
	s.Empty(s.stored())
}

// A writer must never delete the file it is about to append to, whatever the clock says
// about the file already sitting under its id.
func (s *PruneSuite) TestOpenNeverPrunesItsOwnFile() {
	s.writeAged("resumed", 400*24*time.Hour, []Record{s.result("resumed", 1, 400*24*time.Hour)})

	w, err := Open(s.T().Context(), s.dir, "resumed", InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))

	s.Equal([]string{"resumed"}, s.stored())
	s.Len(s.read().Records, 3, "the old fact, the start, and the new fact")
}

func (s *PruneSuite) TestANonPositiveWindowKeepsEverything() {
	s.writeAged("ancient", 400*24*time.Hour, []Record{s.result("ancient", 1, 400*24*time.Hour)})

	s.Require().NoError(Prune(s.T().Context(), filepath.Join(s.dir, "absent"), retention), "a store that does not exist has nothing to prune")
	s.Require().NoError(Prune(s.T().Context(), s.dir, 0))
	s.Require().NoError(Prune(s.T().Context(), s.dir, -time.Hour))

	s.Equal([]string{"ancient"}, s.stored())
}

// Pruning in one process runs against a fold another process is in the middle of
// reading, so a listed file can be gone by the time the reader opens it. That is
// housekeeping finishing first, not corruption.
func (s *PruneSuite) TestReadAllTreatsAFileThatVanishedMidFoldAsAbsent() {
	writeRaw(&s.Suite, s.dir, "kept", []Record{s.record("kept", 1, 10, KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass})})

	// A dangling symlink reproduces the race deterministically: the directory lists the
	// name, and the open that follows fails with ENOENT.
	if err := os.Symlink(filepath.Join(s.dir, "already-pruned"), filepath.Join(s.dir, "gone"+FileExt)); err != nil {
		s.T().Skipf("symlinks unavailable on this platform: %v", err)
	}

	fold := s.read()
	s.Len(fold.Records, 1)
	s.Equal(1, fold.Invocations, "a file that is no longer there is not an invocation anybody can read")
	s.Zero(fold.Skipped, "pruning is not damage")
}

func (s *PruneSuite) TestReadFileReportsAMissingFileAsVanishedNotSkipped() {
	records, skipped, legacy, retired, vanished := readFile(filepath.Join(s.dir, "never-existed"+FileExt))
	s.Empty(records)
	s.Zero(skipped)
	s.Zero(legacy)
	s.Empty(retired)
	s.True(vanished)
}

// Schema-1 lines are never read as records, but they carry a timestamp. Keeping their
// files forever is what left eleven thousand of them in one store after the rename.
func (s *PruneSuite) TestASchemaOneFileIsCountedAndAgesOutByItsTimestamps() {
	old := s.writeLegacy("0123456789abcdef", time.Now().Add(-90*24*time.Hour))
	young := s.writeLegacy("fedcba9876543210", time.Now().Add(-24*time.Hour))
	undated := filepath.Join(s.dir, "undated"+FileExt)
	s.Require().NoError(os.WriteFile(undated, []byte("not a record\n"), 0o644))
	ancient := time.Now().Add(-90 * 24 * time.Hour)
	s.Require().NoError(os.Chtimes(undated, ancient, ancient))

	fold := s.read()
	s.Empty(fold.Records)
	s.Equal(Fold{Records: fold.Records, Invocations: 3, Skipped: 1, Legacy: 4}, fold,
		"a line written before the rename is not damage; the undated one is")

	s.Require().NoError(Prune(s.T().Context(), s.dir, DefaultRetention))
	s.NoFileExists(old, "a schema-1 file past the window ages out like any other")
	s.FileExists(young)
	s.FileExists(undated, "a file with no dated line has no known age, so it stays")
}

func (s *PruneSuite) TestCapsTheStoreAtItsNewestFiles() {
	for i := range 5 {
		id := fmt.Sprintf("inv%d", i)
		s.writeAged(id, time.Duration(i)*time.Hour, []Record{
			{V: APIVersion, Invocation: id, Seq: 1, Kind: KindInvocationStart, Ts: msAgo(time.Duration(i) * time.Hour)},
		})
	}
	s.writeLegacy("legacy", time.Now().Add(-30*time.Minute))

	s.Require().NoError(prune(s.T().Context(), s.dir, retention, 3, "inv4"+FileExt, newOptions(nil)))

	s.Equal([]string{"inv0", "inv1", "inv4", "legacy"}, s.stored(),
		"the newest three by newest fact survive, schema-1 included, and the writer's own file is never past the cap")
}

func (s *PruneSuite) TestCapSparesTheNewestLoadedSessions() {
	s.writeAged("loaded", 3*time.Hour, []Record{
		{V: APIVersion, Invocation: "loaded", Seq: 1, Kind: KindAgentEvent, Ts: msAgo(3 * time.Hour), Payload: []byte(`{"host":"h","event":"file.read","ref":"r","at":1}`)},
	})
	for i := range 3 {
		id := fmt.Sprintf("inv%d", i)
		s.writeAged(id, time.Duration(i)*time.Hour, []Record{
			{V: APIVersion, Invocation: id, Seq: 1, Kind: KindInvocationStart, Ts: msAgo(time.Duration(i) * time.Hour)},
		})
	}

	s.Require().NoError(prune(s.T().Context(), s.dir, retention, 2, "", newOptions(nil)))

	s.Equal([]string{"inv0", "inv1", "loaded"}, s.stored(), "a loaded host session is the rare kind the cap must not push out")
}

func (s *PruneSuite) TestCapStillHonorsTheTie() {
	s.writeAged("asker", 5*time.Hour, []Record{s.record("asker", 1, msAgo(5*time.Hour), kindHeld, grouped{Group: "g1"})})
	for i := range 3 {
		id := fmt.Sprintf("inv%d", i)
		s.writeAged(id, time.Duration(i)*time.Hour, []Record{
			{V: APIVersion, Invocation: id, Seq: 1, Kind: KindInvocationStart, Ts: msAgo(time.Duration(i) * time.Hour)},
		})
	}

	s.Require().NoError(prune(s.T().Context(), s.dir, retention, 2, "", newOptions([]Option{WithTie(testTie())})))

	s.Equal([]string{"asker", "inv0", "inv1"}, s.stored(), "a held file is exempt from the cap as from retention")
}

// A plain Stat-then-write pair let every concurrent Open in the same window see the same
// stale stamp and each independently decide to prune. Racing many callers at a stale
// stamp, only one may ever run prune.
func (s *PruneSuite) TestClaimStalePruneStampPrunesOnceUnderConcurrency() {
	stamp := filepath.Join(s.dir, pruneStamp)
	s.Require().NoError(os.WriteFile(stamp, nil, 0o644))
	stale := time.Now().Add(-2 * pruneInterval)
	s.Require().NoError(os.Chtimes(stamp, stale, stale))
	// An invocation old enough to prune: its delete proves some caller pruned.
	s.writeAged("ancient", 400*24*time.Hour, []Record{s.result("ancient", 1, 400*24*time.Hour)})

	const callers = 50
	var ready sync.WaitGroup
	ready.Add(callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Go(func() {
			ready.Done()
			ready.Wait() // line every goroutine up before any of them races the claim
			claimStalePruneStamp(s.T().Context(), s.dir, stamp, "", newOptions(nil))
		})
	}
	wg.Wait()

	s.Empty(s.stored(), "the one ancient invocation must be gone: some caller pruned")
	fi, err := os.Stat(stamp)
	s.Require().NoError(err)
	s.WithinDuration(time.Now(), fi.ModTime(), time.Minute, "the winner must have refreshed the stamp")
}

// A caller that loses the lock race does nothing rather than blocking for its turn: with
// the lock already held, a stale-looking stamp is left exactly as it was.
func (s *PruneSuite) TestClaimStalePruneStampSkipsWhenAlreadyContended() {
	stamp := filepath.Join(s.dir, pruneStamp)
	s.Require().NoError(os.WriteFile(stamp, nil, 0o644))
	stale := time.Now().Add(-2 * pruneInterval)
	s.Require().NoError(os.Chtimes(stamp, stale, stale))

	held, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- codec.WithLock(context.Background(), stamp+".lock", time.Second, func() error {
			close(held)
			<-release
			return nil
		})
	}()
	<-held

	claimStalePruneStamp(s.T().Context(), s.dir, stamp, "", newOptions(nil))
	close(release)
	s.Require().NoError(<-done)

	fi, err := os.Stat(stamp)
	s.Require().NoError(err)
	s.True(fi.ModTime().Equal(stale), "a contended caller must not touch the stamp")
}
