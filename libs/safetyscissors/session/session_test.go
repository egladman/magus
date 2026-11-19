package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

// writeRaw lays down an invocation file directly, so a test can pin timestamps and
// sequence numbers the Writer would otherwise stamp from the clock.
func writeRaw(s *suite.Suite, dir, invocation string, records []Record) {
	var b strings.Builder
	for _, rec := range records {
		line, err := codec.Marshal(rec)
		s.Require().NoError(err)
		b.Write(line)
		b.WriteByte('\n')
	}
	s.Require().NoError(os.WriteFile(filepath.Join(dir, invocation+FileExt), []byte(b.String()), 0o644))
}

// testConfig opens invocation under dir at DefaultRetention, failing t on a prune error.
func testConfig(t testing.TB, dir, invocation string, start InvocationStart) Config {
	return Config{
		Dir: dir, Invocation: invocation, Start: start, Retention: DefaultRetention,
		PruneErrors: func(_ context.Context, err error) { t.Errorf("prune: %v", err) },
	}
}

func openAt(t testing.TB, dir, invocation string, start InvocationStart, opts ...Option) (*Writer, error) {
	return Open(t.Context(), testConfig(t, dir, invocation, start), opts...)
}

func loadAt(t testing.TB, dir string, events []LoadEvent, start InvocationStart) (LoadResult, error) {
	return LoadEvents(t.Context(), testConfig(t, dir, "", start), events)
}

func loadable(session, host, ref string, at int64) LoadEvent {
	return LoadEvent{Session: session, Event: AgentEvent{
		Host: host, Kind: EventFileRead, Ref: ref, AtMs: at, Text: ref + ".go",
	}}
}

// note is a payload that bounds itself, the way a caller's free-text payload does.
type note struct {
	Text string `json:"text"`
}

func (n note) Bound() any {
	if len(n.Text) > 8 {
		n.Text = n.Text[:8]
	}
	return n
}

type WriterSuite struct{ suite.Suite }

func TestWriter(t *testing.T) { suite.Run(t, new(WriterSuite)) }

func (s *WriterSuite) TestAppendReadRoundTrip() {
	dir := s.T().TempDir()

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{Workspace: "/repo", Command: "run build"})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Project: "api", Outcome: OutcomePass, DurationMs: 12}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2, "the first append also writes the start record")
	s.Zero(fold.Skipped)
	s.Equal(1, fold.Invocations)

	s.Equal(Record{V: APIVersion, Invocation: "sess1", Seq: 1, Kind: KindInvocationStart, Ts: fold.Records[0].Ts, Payload: fold.Records[0].Payload}, fold.Records[0])
	s.Equal(uint64(2), fold.Records[1].Seq)

	var got TargetResult
	s.Require().NoError(codec.Unmarshal(fold.Records[1].Payload, &got))
	s.Equal(TargetResult{Target: "build", Project: "api", Outcome: OutcomePass, DurationMs: 12}, got)
}

// A line a dead writer left unfinished costs that line, never the record appended after.
func (s *WriterSuite) TestAnAppendAfterAnUnfinishedLineStartsItsOwn() {
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "sess1.jsonl"), []byte(`{"v":"safetyscissors.session/v1alpha1","invo`), 0o600))

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{Command: "run build"})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Equal(1, fold.Skipped, "the unfinished line alone")
	s.Len(fold.Records, 2)
}

// A load creates the store as Open does, owner-only, though its lock comes first.
func (s *WriterSuite) TestALoadCreatesTheStorePrivate() {
	dir := filepath.Join(s.T().TempDir(), "sessions")

	_, err := loadAt(s.T(), dir, nil, InvocationStart{})
	s.Require().NoError(err)

	info, err := os.Stat(dir)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o700), info.Mode().Perm())
}

// The modes Open and Append create: an invocation records the commands its user ran.
func (s *WriterSuite) TestStoreIsPrivateToItsOwner() {
	dir := filepath.Join(s.T().TempDir(), "sessions")

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{Workspace: "/repo", Command: "run build"})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Project: "api", Outcome: OutcomePass}))

	info, err := os.Stat(dir)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o700), info.Mode().Perm())
	info, err = os.Stat(w.path)
	s.Require().NoError(err)
	s.Equal(os.FileMode(0o600), info.Mode().Perm())
}

// The clock is the caller's: a record is stamped with it, not with the wall clock.
func (s *WriterSuite) TestAppendStampsWithTheCallersClock() {
	dir := s.T().TempDir()
	at := time.UnixMilli(1_700_000_000_000)

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{}, WithClock(func() time.Time { return at }))
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2)
	s.Equal(at.UnixMilli(), fold.Records[0].Ts)
	s.Equal(at.UnixMilli(), fold.Records[1].Ts)
}

// A result carries what the target claimed and cost, and a replay's zero claim writes
// only its sizing.
func (s *WriterSuite) TestTargetResultEncodesTheClaimAndCost() {
	ran := TargetResult{
		Target: "go-test", Outcome: OutcomePass, StartedMs: 1_700_000_000_000,
		Slots: 4, MemoryMB: 3800, Sizing: "measured", WaitedMs: 1500,
		MaxRSSBytes: 2 << 30, CPUMs: 90_000,
	}
	b, err := codec.Marshal(ran)
	s.Require().NoError(err)
	s.JSONEq(`{"target":"go-test","outcome":"pass","started_ms":1700000000000,"slots":4,"memory_mb":3800,"sizing":"measured","waited_ms":1500,"max_rss_bytes":2147483648,"cpu_ms":90000}`, string(b))
	var got TargetResult
	s.Require().NoError(codec.Unmarshal(b, &got))
	s.Equal(ran, got)

	b, err = codec.Marshal(TargetResult{Target: "go-test", Outcome: OutcomePass, Replayed: true, Sizing: "none"})
	s.Require().NoError(err)
	s.JSONEq(`{"target":"go-test","outcome":"pass","replayed":true,"sizing":"none"}`, string(b))
}

// A duration written as dur_ms before DurationMs existed still reads.
func (s *WriterSuite) TestTargetResultReadsTheOlderDurationSpelling() {
	var got TargetResult
	s.Require().NoError(codec.Unmarshal([]byte(`{"target":"build","outcome":"pass","dur_ms":42}`), &got))
	s.Equal(int64(42), got.DurationMs)
}

// Open must not create the file: a command that records no fact should leave no
// invocation behind, or a listing fills up with runs that did nothing.
func (s *WriterSuite) TestOpenWritesNothingUntilTheFirstFact() {
	dir := s.T().TempDir()

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{})
	s.Require().NoError(err)

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Empty(fold.Records)
	s.Zero(fold.Invocations)

	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))
	fold, err = ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Len(fold.Records, 2)
}

// The bound is applied at write time, which is what makes it hold for a producer that
// never heard of it.
func (s *WriterSuite) TestAppendStoresABounderAsItBoundsItself() {
	dir := s.T().TempDir()

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), "note", note{Text: "a long line of text"}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2)
	s.JSONEq(`{"text":"a long l"}`, string(fold.Records[1].Payload))
}

// An id is reached twice whenever it is reused (a resumed agent session, a retried
// command), and the second writer has to continue the numbering rather than restart it,
// or (Invocation, Seq) stops identifying one record.
func (s *WriterSuite) TestOpenResumesTheSequenceOfAnExistingInvocation() {
	dir := s.T().TempDir()

	first, err := openAt(s.T(), dir, "sess1", InvocationStart{Command: "run build"})
	s.Require().NoError(err)
	s.Require().NoError(first.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	s.Require().NoError(first.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomePass}))

	second, err := openAt(s.T(), dir, "sess1", InvocationStart{Command: "run lint"})
	s.Require().NoError(err)
	s.Require().NoError(second.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "lint", Outcome: OutcomePass}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 5, "each writer declares its own command line")

	var seqs []uint64
	for _, rec := range fold.Records {
		seqs = append(seqs, rec.Seq)
	}
	s.Equal([]uint64{1, 2, 3, 4, 5}, seqs, "the second writer continues the numbering instead of reusing seq 1")
	s.Equal(KindInvocationStart, fold.Records[0].Kind)
	s.Equal(KindInvocationStart, fold.Records[3].Kind)
}

func (s *WriterSuite) TestOpenRejectsAnIDThatCouldEscapeTheStore() {
	for _, id := range []string{"", "../escape", "a/b", "."} {
		_, err := openAt(s.T(), s.T().TempDir(), id, InvocationStart{})
		var invalid *InvalidIDError
		s.Require().ErrorAs(err, &invalid, "invocation id %q", id)
		s.Equal(id, invalid.ID)
	}
}

func (s *WriterSuite) TestValidFileIDRefusesAPathSeparator() {
	s.True(ValidFileID("8f1c-2d4e_9"))
	s.False(ValidFileID("../escape"))
	s.False(ValidFileID(""))
}

// A pane name is an id like any other: a caller with no host session still writes.
func (s *WriterSuite) TestAnInvocationNeedsNoHostSession() {
	dir := s.T().TempDir()
	w, err := openAt(s.T(), dir, "pane-left", InvocationStart{Command: "edit"})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "save", Outcome: OutcomePass}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Equal("pane-left", summaries[0].Invocation)
	s.Empty(summaries[0].Session)
}

type ReadSuite struct{ suite.Suite }

func TestRead(t *testing.T) { suite.Run(t, new(ReadSuite)) }

// One oversized line must cost one record and nothing else. A bufio.Scanner abandons
// the rest of the file on ErrTooLong, which hides every LATER fact.
func (s *ReadSuite) TestReadAllSkipsAnOversizedLineAndKeepsReading() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")

	body := strings.Join([]string{
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"target_result","ts":1,"payload":{"target":"build","outcome":"pass"}}`,
		fmt.Sprintf(`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":2,"kind":"target_result","ts":2,"payload":{"target":"%s"}}`, strings.Repeat("x", maxLineBytes*2)),
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":3,"kind":"target_result","ts":3,"payload":{"target":"lint","outcome":"pass"}}`,
	}, "\n")
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Equal(1, fold.Skipped)
	s.Require().Len(fold.Records, 2)
	s.Equal(uint64(1), fold.Records[0].Seq)
	s.Equal(uint64(3), fold.Records[1].Seq, "the record after the oversized line is the one a scanner loses")
}

// A file the fold cannot open is missing history, not one damaged line.
func (s *ReadSuite) TestAnUnreadableFileIsAnError() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	s.Require().NoError(os.WriteFile(path, []byte(`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"target_result","ts":1}`+"\n"), 0o000))

	_, err := ReadAll(s.T().Context(), dir)

	var store *StoreError
	s.Require().ErrorAs(err, &store)
	s.Equal(path, store.Path)
	_, err = FoldKind(s.T().Context(), dir, "target_result")
	s.Require().ErrorAs(err, &store)
}

// Two writers with distinct ids in one store are both visible to one fold. This is the
// two-worktree done-check in miniature: the writers differ, the store does not, so each
// sees the other's facts.
func (s *ReadSuite) TestFoldSeesEveryWriterInTheStore() {
	dir := s.T().TempDir()

	left, err := openAt(s.T(), dir, "wt-left", InvocationStart{Workspace: "/repo/a"})
	s.Require().NoError(err)
	right, err := openAt(s.T(), dir, "wt-right", InvocationStart{Workspace: "/repo/b"})
	s.Require().NoError(err)

	s.Require().NoError(left.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	s.Require().NoError(right.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "test", Outcome: OutcomeFail}))
	s.Require().NoError(left.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "lint", Outcome: OutcomePass}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Equal(2, fold.Invocations)
	s.Len(fold.Records, 5)

	seen := map[string]int{}
	for _, rec := range fold.Records {
		seen[rec.Invocation]++
	}
	s.Equal(map[string]int{"wt-left": 3, "wt-right": 2}, seen)

	summaries := Summarize(fold)
	s.Require().Len(summaries, 2)
	byID := map[string]Summary{}
	for _, sum := range summaries {
		byID[sum.Invocation] = sum
	}
	s.Equal("/repo/a", byID["wt-left"].Workspace)
	s.Equal("/repo/b", byID["wt-right"].Workspace)
	s.Len(byID["wt-left"].Targets, 2)
	s.Len(byID["wt-right"].Targets, 1)
}

// Ordering is by (Ts, Invocation, Seq). The tie-break matters more than the timestamp:
// two writers appending in one millisecond must still fold to one stable order.
func (s *ReadSuite) TestFoldOrdersByTimeThenInvocationThenSeq() {
	dir := s.T().TempDir()

	writeRaw(&s.Suite, dir, "beta", []Record{
		{V: APIVersion, Invocation: "beta", Seq: 1, Kind: KindTargetResult, Ts: 100},
		{V: APIVersion, Invocation: "beta", Seq: 2, Kind: KindTargetResult, Ts: 100},
	})
	writeRaw(&s.Suite, dir, "alpha", []Record{
		{V: APIVersion, Invocation: "alpha", Seq: 1, Kind: KindTargetResult, Ts: 100},
		{V: APIVersion, Invocation: "alpha", Seq: 2, Kind: KindTargetResult, Ts: 300},
	})

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)

	var order []string
	for _, rec := range fold.Records {
		order = append(order, fmt.Sprintf("%s/%c", rec.Invocation, '0'+rec.Seq))
	}
	s.Equal([]string{"alpha/1", "beta/1", "beta/2", "alpha/2"}, order)
}

// A file is written by a process that can be killed mid-line, so a truncated tail is
// expected. The read must surrender the bad line and nothing else.
func (s *ReadSuite) TestReadAllToleratesACorruptTail() {
	dir := s.T().TempDir()

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))

	path := filepath.Join(dir, "sess1.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	s.Require().NoError(err)
	_, err = f.WriteString(`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":3,"kind":"target_res`)
	s.Require().NoError(err)
	s.Require().NoError(f.Close())

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err, "a half-written line must never fail the read")
	s.Len(fold.Records, 2)
	s.Equal(1, fold.Skipped)

	// The invocation survives the damage rather than vanishing with it.
	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Len(summaries[0].Targets, 1)
}

func (s *ReadSuite) TestReadAllCountsEveryUnusableLine() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"target_result","ts":1}`,
		`not json at all`,
		``, // a blank line is not damage; it is skipped without counting
		`{"v":"safetyscissors.session/v1alpha1","seq":2,"kind":"target_result","ts":2}`, // no invocation: unattributable
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":3,"ts":3}`,   // no kind: uninterpretable
	}, "\n")
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Len(fold.Records, 1)
	s.Equal(3, fold.Skipped)
}

// An unknown kind and an unknown payload field are what a NEWER writer writes. An older
// reader must show less, never fail, and must not drop the invocation.
func (s *ReadSuite) TestUnknownKindsAndFieldsAreIgnoredNotRejected() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"invocation_start","ts":10,"payload":{"workspace":"/repo","invented_field":"ignored"}}`,
		`{"v":"safetyscissors.session/v1alpha9","invocation":"sess1","seq":2,"kind":"attention_requested","ts":20,"payload":{"whatever":true}}`,
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":3,"kind":"target_result","ts":30,"payload":{"target":"build","outcome":"pass","future":1}}`,
	}, "\n")
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Len(fold.Records, 3)
	s.Zero(fold.Skipped, "an unknown kind is not damage")

	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Equal(Summary{
		Invocation: "sess1",
		Workspace:  "/repo",
		StartedMs:  10,
		LastMs:     30, // an unrecognized fact still advances the clock
		Facts:      3,  // and still counts as activity
		Targets:    []TargetResult{{Target: "build", Outcome: OutcomePass}},
	}, summaries[0])
}

// A loaded event written before the guard's defer rename holds "pass", and the store is
// refused with the file to clear rather than read with that event missing.
func (s *ReadSuite) TestARetiredDecisionRefusesTheStore() {
	for _, retired := range []string{"pass", "allow"} {
		dir := s.T().TempDir()
		path := filepath.Join(dir, "sess1.jsonl")
		body := `{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"agent_event","ts":10,` +
			`"payload":{"host":"h1","event":"shell.command","ref":"r1","at":10,"verdict":"` + retired + `"}}`
		s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

		_, err := ReadAll(s.T().Context(), dir)
		var refused *RetiredDecisionError
		s.Require().ErrorAs(err, &refused, retired)
		s.Equal(RetiredDecisionError{Path: path, Decision: retired}, *refused)
		s.Contains(err.Error(), path, "the error names the file")

		_, err = FoldAgentEvents(s.T().Context(), dir)
		s.Require().ErrorAs(err, &refused, "the cached fold refuses it too")

		_, err = ReadInvocation(s.T().Context(), dir, "sess1")
		s.Require().ErrorAs(err, &refused, "reading the one file refuses it too")
	}
}

// A record from before the module took the store carries magus.sessions, and the store
// is refused with the file named rather than read with that record missing.
func (s *ReadSuite) TestARetiredGroupRefusesTheStore() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := `{"v":"magus.sessions/v1alpha1","invocation":"sess1","seq":1,"kind":"target_result","ts":10,"payload":{"target":"build","outcome":"pass"}}`
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	_, err := ReadAll(s.T().Context(), dir)
	var refused *RetiredGroupError
	s.Require().ErrorAs(err, &refused)
	s.Equal(RetiredGroupError{Path: path, Version: "magus.sessions/v1alpha1"}, *refused)
	s.Contains(err.Error(), path, "the error names the file")

	_, err = FoldAgentEvents(s.T().Context(), dir)
	s.Require().ErrorAs(err, &refused, "the cached fold refuses it too")

	_, err = ReadInvocation(s.T().Context(), dir, "sess1")
	s.Require().ErrorAs(err, &refused, "reading the one file refuses it too")
}

func (s *ReadSuite) TestSummarizeOrdersMostRecentFirst() {
	dir := s.T().TempDir()
	writeRaw(&s.Suite, dir, "old", []Record{{V: APIVersion, Invocation: "old", Seq: 1, Kind: KindTargetResult, Ts: 100}})
	writeRaw(&s.Suite, dir, "new", []Record{{V: APIVersion, Invocation: "new", Seq: 1, Kind: KindTargetResult, Ts: 900}})
	writeRaw(&s.Suite, dir, "mid", []Record{{V: APIVersion, Invocation: "mid", Seq: 1, Kind: KindTargetResult, Ts: 500}})

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)

	var ids []string
	for _, sum := range Summarize(fold) {
		ids = append(ids, sum.Invocation)
	}
	s.Equal([]string{"new", "mid", "old"}, ids)
}

// The lease rides BOTH payloads, and the target result is the one that matters: a reader
// joining one fact to a lease does not hold the start record that opened the file.
func (s *ReadSuite) TestLeaseSurvivesTheStoreOnBothPayloads() {
	dir := s.T().TempDir()

	w, err := openAt(s.T(), dir, "sess1", InvocationStart{Workspace: "/repo", Command: "run build", Lease: "fleet/f3"})
	s.Require().NoError(err)
	s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass, Lease: "fleet/f3"}))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Require().Len(fold.Records, 2)

	var start InvocationStart
	s.Require().NoError(codec.Unmarshal(fold.Records[0].Payload, &start))
	s.Equal("fleet/f3", start.Lease)

	var result TargetResult
	s.Require().NoError(codec.Unmarshal(fold.Records[1].Payload, &result))
	s.Equal("fleet/f3", result.Lease)

	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Equal("fleet/f3", summaries[0].Lease, "the view reads the lease off the invocation, not off each fact")
}

// The trace context survives the store and reaches the view, so a child's parent span
// can be joined to the invocation that minted it.
func (s *ReadSuite) TestTraceContextSurvivesTheStoreAndSummarizes() {
	dir := s.T().TempDir()

	parent := InvocationStart{Workspace: "/repo", Command: "run build", SpanID: "00f067aa0ba902b7"}
	child := InvocationStart{
		Workspace:    "/repo",
		Command:      "run test",
		Lease:        "fleet/f3",
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		SpanID:       "a1b2c3d4e5f60718",
		ParentSpanID: "00f067aa0ba902b7",
		Spawner:      "claude code",
	}
	for id, start := range map[string]InvocationStart{"sess1": parent, "sess2": child} {
		w, err := openAt(s.T(), dir, id, start)
		s.Require().NoError(err)
		s.Require().NoError(w.Append(s.T().Context(), KindTargetResult, TargetResult{Target: "build", Outcome: OutcomePass}))
	}

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	byID := make(map[string]Summary)
	for _, sum := range Summarize(fold) {
		byID[sum.Invocation] = sum
	}
	s.Require().Len(byID, 2)

	s.Equal(child.TraceID, byID["sess2"].TraceID)
	s.Equal(child.Spawner, byID["sess2"].Spawner)
	s.Equal(byID["sess1"].SpanID, byID["sess2"].ParentSpanID, "the child names the span the parent minted")
	s.Empty(byID["sess1"].ParentSpanID, "an invocation nothing spawned claims no parent")
}

// The field is additive: a file written before it existed still reads, and the
// invocations in it summarize as unattributed rather than as damage.
func (s *ReadSuite) TestAnInvocationWrittenWithoutALeaseStillReads() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, "sess1.jsonl")
	body := strings.Join([]string{
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":1,"kind":"invocation_start","ts":10,"payload":{"workspace":"/repo"}}`,
		`{"v":"safetyscissors.session/v1alpha1","invocation":"sess1","seq":2,"kind":"target_result","ts":20,"payload":{"target":"build","outcome":"pass"}}`,
	}, "\n")
	s.Require().NoError(os.WriteFile(path, []byte(body+"\n"), 0o644))

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Zero(fold.Skipped)

	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Empty(summaries[0].Lease)
	s.Require().Len(summaries[0].Targets, 1)
	s.Empty(summaries[0].Targets[0].Lease)
}

func (s *ReadSuite) TestReadAllMissingStoreIsEmptyNotAnError() {
	fold, err := ReadAll(s.T().Context(), filepath.Join(s.T().TempDir(), "never-created"))
	s.Require().NoError(err, "nothing has run yet is not a failure")
	s.Empty(fold.Records)
	s.Zero(fold.Invocations)
}

func (s *ReadSuite) TestReadInvocationReadsOneFile() {
	dir := s.T().TempDir()
	writeRaw(&s.Suite, dir, "a", []Record{{V: APIVersion, Invocation: "a", Seq: 1, Kind: KindTargetResult, Ts: 1}})
	writeRaw(&s.Suite, dir, "b", []Record{{V: APIVersion, Invocation: "b", Seq: 1, Kind: KindTargetResult, Ts: 2}})

	got, err := ReadInvocation(s.T().Context(), dir, "a")
	s.Require().NoError(err)
	s.Equal([]Record{{V: APIVersion, Invocation: "a", Seq: 1, Kind: KindTargetResult, Ts: 1}}, got)
	got, err = ReadInvocation(s.T().Context(), dir, "absent")
	s.Require().NoError(err)
	s.Empty(got)
}

// A file that cannot be read is an error, not an invocation with no records.
func (s *ReadSuite) TestReadInvocationReturnsAReadFailure() {
	dir := s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(dir, "a"+FileExt), 0o755))
	_, err := ReadInvocation(s.T().Context(), dir, "a")
	var store *StoreError
	s.Require().ErrorAs(err, &store)
	s.Equal(filepath.Join(dir, "a"+FileExt), store.Path)
}

// An id that could name a file outside the store is refused before anything opens.
func (s *ReadSuite) TestReadInvocationRefusesAnEscapingID() {
	dir := s.T().TempDir()
	for _, id := range []string{"", "../a", "a/b", "."} {
		_, err := ReadInvocation(s.T().Context(), dir, id)
		var invalid *InvalidIDError
		s.Require().ErrorAs(err, &invalid, "invocation id %q", id)
	}
}

type PreFoldRecordSuite struct {
	suite.Suite
	fold Fold
}

func TestPreFoldRecord(t *testing.T) { suite.Run(t, new(PreFoldRecordSuite)) }

func (s *PreFoldRecordSuite) SetupTest() {
	dir := s.T().TempDir()
	body := strings.Join([]string{
		`{"v":2,"invocation":"sess1","seq":1,"kind":"invocation_start","ts":10,"payload":{"workspace":"/repo"}}`,
		`{"v":1,"session":"old","seq":1,"kind":"invocation_start","ts":5}`,
	}, "\n")
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "sess1.jsonl"), []byte(body+"\n"), 0o644))
	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.fold = fold
}

func (s *PreFoldRecordSuite) TestReadsAVersionTwoRecordAsV1alpha1() {
	s.Require().Len(s.fold.Records, 1)
	s.Equal(Record{V: APIVersion, Invocation: "sess1", Seq: 1, Kind: KindInvocationStart, Ts: 10,
		Payload: []byte(`{"workspace":"/repo"}`)}, s.fold.Records[0])
	s.Zero(s.fold.Skipped)
}

func (s *PreFoldRecordSuite) TestStillCountsAVersionOneRecordAsLegacy() {
	s.Equal(1, s.fold.Legacy)
}

type LoadSuite struct{ suite.Suite }

func TestLoad(t *testing.T) { suite.Run(t, new(LoadSuite)) }

func (s *LoadSuite) TestLoadEventsDedupsOnHostSessionRef() {
	dir := s.T().TempDir()
	events := []LoadEvent{
		loadable("s1", "h1", "r1", 10),
		loadable("s1", "h1", "r2", 20),
		loadable("s2", "h1", "r1", 30),
	}

	first, err := loadAt(s.T(), dir, events, InvocationStart{})
	s.Require().NoError(err)
	s.Equal(LoadResult{Loaded: 3, ByKind: map[string]int{EventFileRead: 3}}, first,
		"r1 under two sessions is two events")

	second, err := loadAt(s.T(), dir, events, InvocationStart{})
	s.Require().NoError(err)
	s.Equal(LoadResult{Deduped: 3, ByKind: map[string]int{}}, second,
		"a dedup run reports no kinds: the breakdown counts what was written")

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Len(AgentEvents(fold, "s1"), 2)
	s.Len(AgentEvents(fold, "s2"), 1)
}

// A re-load that carries hint ids the stored event lacks appends it again, and the fold
// yields the call once with both sets of ids.
func (s *LoadSuite) TestALoadThatAddsHintIDsIsFoldedIntoOneEvent() {
	dir := s.T().TempDir()
	early := LoadEvent{Session: "s1", Event: AgentEvent{Host: "h1", Kind: EventShellCommand, Ref: "r1", AtMs: 10}}
	joined := early
	joined.Event.NextServed = []string{"hint-1"}

	_, err := loadAt(s.T(), dir, []LoadEvent{early}, InvocationStart{})
	s.Require().NoError(err)
	got, err := loadAt(s.T(), dir, []LoadEvent{joined}, InvocationStart{})
	s.Require().NoError(err)
	s.Equal(1, got.Loaded, "the ids are new, so the event is appended again")

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	events := AgentEvents(fold, "s1")
	s.Require().Len(events, 1)
	s.Equal([]string{"hint-1"}, events[0].NextServed)
}

// The host label is the join a loaded session exists to record, so it has to ride the
// start record and not only the events.
func (s *LoadSuite) TestLoadEventsRecordsTheHostOnTheInvocationStart() {
	dir := s.T().TempDir()
	_, err := loadAt(s.T(), dir, []LoadEvent{loadable("s1", "h1", "r1", 10)}, InvocationStart{Workspace: "/tmp/ws"})
	s.Require().NoError(err)

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	got := summaries[0]
	origin := got.Origin
	origin.Host, origin.Session = "h1", "s1"
	s.Equal(Summary{
		Invocation: got.Invocation,
		Origin:     origin, // a load names the host session the events came from
		SpanID:     got.SpanID,
		Workspace:  "/tmp/ws",
		StartedMs:  got.StartedMs,
		LastMs:     got.LastMs,
		Facts:      got.Facts,
		Events:     1,
		Model:      got.Model,
		pairAtMs:   got.pairAtMs,
	}, got)
}

// A session that spans a model or host switch reports what it ENDED on, since that is
// the pair today's rules would be checked against, not what it began with.
func (s *LoadSuite) TestSummarizeReadsTheNewestModelAndHostVersionPair() {
	dir := s.T().TempDir()
	_, err := loadAt(s.T(), dir, []LoadEvent{
		{Session: "s1", Event: AgentEvent{Host: "h1", Kind: EventFileRead, Ref: "r1", AtMs: 10, Agent: Agent{Model: "claude-opus-5", HostVersion: "2.1.260"}}},
		{Session: "s1", Event: AgentEvent{Host: "h1", Kind: EventFileRead, Ref: "r2", AtMs: 20, Agent: Agent{Model: "claude-opus-5-5", HostVersion: "2.1.280"}}},
	}, InvocationStart{})
	s.Require().NoError(err)

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Equal(Agent{Model: "claude-opus-5-5", HostVersion: "2.1.280"}, summaries[0].Model, "the newest event's pair wins, not the first")
}

// The pair and the inlined Origin's subagent id are two keys: a shared one drops both
// under either JSON build.
func (s *LoadSuite) TestSummaryKeepsTheSubagentAndThePairApart() {
	sum := Summary{Invocation: "i", Model: Agent{Model: "m"}}
	sum.Agent = "sub-1"
	raw, err := codec.Marshal(sum)
	s.Require().NoError(err)
	var back map[string]any
	s.Require().NoError(codec.Unmarshal(raw, &back))
	s.Equal("sub-1", back["agent"])
	s.Equal(map[string]any{"model": "m"}, back["model"])
}

// A host that never names a model or its own version summarizes with both empty: a
// host that says nothing is unobservable, not "no model".
func (s *LoadSuite) TestSummarizeLeavesTheModelPairEmptyWhenNoEventNamesOne() {
	dir := s.T().TempDir()
	_, err := loadAt(s.T(), dir, []LoadEvent{loadable("s1", "h1", "r1", 10)}, InvocationStart{})
	s.Require().NoError(err)

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	summaries := Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Zero(summaries[0].Model)
}

// NewestEventMs reads the HOST's timestamp: a store loaded today from a month-old
// transcript is a month-old record.
func (s *LoadSuite) TestNewestEventMsReadsTheHostTimestamp() {
	dir := s.T().TempDir()
	_, err := loadAt(s.T(), dir, []LoadEvent{
		loadable("s1", "h1", "r1", 10),
		loadable("s1", "h1", "r2", 900),
	}, InvocationStart{})
	s.Require().NoError(err)

	fold, err := ReadAll(s.T().Context(), dir)
	s.Require().NoError(err)
	s.Equal(int64(900), NewestEventMs(fold))
}

func (s *LoadSuite) TestNewestEventMsIsZeroWithoutALoad() {
	fold, err := ReadAll(s.T().Context(), s.T().TempDir())
	s.Require().NoError(err)
	s.Zero(NewestEventMs(fold))
}

func (s *LoadSuite) TestValidEventKind() {
	s.True(ValidEventKind(EventShellCommand))
	s.True(ValidEventKind(EventMagusCall))
	s.False(ValidEventKind("tool.use"))
	s.False(ValidEventKind(""))
}

// A load refuses a session id that could not name a file, and says which.
func (s *LoadSuite) TestLoadEventsRefusesAnInvalidSessionID() {
	_, err := loadAt(s.T(), s.T().TempDir(), []LoadEvent{loadable("../x", "h1", "r1", 10)}, InvocationStart{})
	var invalid *InvalidIDError
	s.Require().ErrorAs(err, &invalid)
	s.Equal("../x", invalid.ID)
}
