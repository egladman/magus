package journal

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

// writtenLines are lines the magus writer produced before the journal moved
// into this module, one per kind it writes.
var writtenLines = map[Kind]string{
	KindStarted:  `{"ts":1794086077260,"inv":"invmw6wj0wb1","kind":"started","duration_ms":0,"started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0,"command":{"arguments":["run","go-build","."],"cwd":"/repo","trigger":"run"},"magus_version":"v0.4.3"}`,
	KindExec:     `{"ts":1794086079604,"inv":"invmw6wj0wb1","project":".","target":"go-build:rw","kind":"exec","duration_ms":0,"text":"go build -trimpath","started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0}`,
	KindOutput:   `{"ts":1794084142531,"inv":"invmw6vclim1","project":".","target":"go-build:rw","kind":"output","stream":"stderr","duration_ms":0,"text":"built","started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0}`,
	KindRun:      `{"ts":1794086081115,"inv":"invmw6wj0wb1","project":".","target":"generate:rw","kind":"result","level":"error","status":"fail","ref":"out5c1f62f4a259","duration_ms":1220,"cache_key":"5c1f62f4","text":"exit 1","diagnostic_code":"MGS1001","started_ms":1794086079895,"slots":2,"memory_mb":512,"sizing":"declared","waited_ms":15,"max_rss_bytes":70418432,"cpu_ms":745}`,
	KindScope:    `{"ts":1793587449256,"inv":"invmvynnp2c1","kind":"scope","duration_ms":0,"started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0,"undeclared":[{"project":".","files":["LICENSE","go.work"],"inputs":["go.work"]}]}`,
	KindSecret:   `{"ts":1794086079700,"inv":"invmw6wj0wb1","project":"api","target":"deploy","kind":"secret","duration_ms":0,"text":"env://TOKEN via env","started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0}`,
	KindFinished: `{"ts":1794076369147,"inv":"invmw6qpvwy1","kind":"finished","status":"pass","duration_ms":0,"started_ms":0,"slots":0,"memory_mb":0,"waited_ms":0,"max_rss_bytes":0,"cpu_ms":0}`,
}

type RecordSuite struct{ suite.Suite }

func TestRecord(t *testing.T) { suite.Run(t, new(RecordSuite)) }

func (s *RecordSuite) decode(line string) Record {
	var r Record
	s.Require().NoError(codec.Unmarshal([]byte(line), &r))
	return r
}

func (s *RecordSuite) TestEveryWrittenKindRoundTripsByteForByte() {
	for kind, line := range writtenLines {
		r := s.decode(line)
		s.Equal(kind, r.Kind())
		got, err := codec.Marshal(r)
		s.Require().NoError(err, "kind %s", kind)
		s.Equal(line, string(got), "kind %s", kind)
	}
}

func (s *RecordSuite) TestNewRecordWritesWhatTheWriterWrote() {
	at := time.UnixMilli(1794086081115)
	r := NewRecord(at, "invmw6wj0wb1", Run{
		Project:        ".",
		Target:         "generate:rw",
		Level:          "error",
		Status:         StatusFail,
		Ref:            "out5c1f62f4a259",
		Duration:       1220 * time.Millisecond,
		CacheKey:       "5c1f62f4",
		Error:          "exit 1",
		DiagnosticCode: "MGS1001",
		Started:        time.UnixMilli(1794086079895),
		Slots:          2,
		MemoryMB:       512,
		Sizing:         SizingDeclared,
		Waited:         15 * time.Millisecond,
		MaxRSSBytes:    70418432,
		CPU:            745 * time.Millisecond,
	})
	got, err := codec.Marshal(r)
	s.Require().NoError(err)
	s.Equal(writtenLines[KindRun], string(got))

	started := NewRecord(time.UnixMilli(1794086077260), "invmw6wj0wb1", Started{
		Command: Command{Arguments: []string{"run", "go-build", "."}, Cwd: "/repo", Trigger: TriggerRun},
		Version: "v0.4.3",
	})
	got, err = codec.Marshal(started)
	s.Require().NoError(err)
	s.Equal(writtenLines[KindStarted], string(got))
}

func (s *RecordSuite) TestEachAccessorReadsOnlyItsKind() {
	run, ok := s.decode(writtenLines[KindRun]).Run()
	s.Require().True(ok)
	s.Equal(1220*time.Millisecond, run.Duration)
	s.Equal(time.UnixMilli(1794086079895), run.Started)
	s.Equal("exit 1", run.Error)

	r := s.decode(writtenLines[KindOutput])
	_, isRun := r.Run()
	s.False(isRun)
	out, ok := r.Output()
	s.Require().True(ok)
	s.Equal(Output{Project: ".", Target: "go-build:rw", Stream: Stderr, Text: "built"}, out)

	exec, ok := s.decode(writtenLines[KindExec]).Exec()
	s.Require().True(ok)
	s.Equal("go build -trimpath", exec.Command)

	scope, ok := s.decode(writtenLines[KindScope]).Scope()
	s.Require().True(ok)
	s.Equal([]UndeclaredSeed{{Project: ".", Files: []string{"LICENSE", "go.work"}, Inputs: []string{"go.work"}}}, scope.Undeclared)
}

func (s *RecordSuite) TestAnUnknownKindIsKeptVerbatim() {
	line := `{"ts":5,"inv":"inv1","kind":"warn","level":"warn","text":"x","extra":true}`
	r := s.decode(line)
	s.Equal(Kind("warn"), r.Kind())
	_, started := r.Started()
	_, run := r.Run()
	_, output := r.Output()
	s.False(started || run || output, "no accessor reports a kind this build does not know")
	got, err := codec.Marshal(r)
	s.Require().NoError(err)
	s.Equal(line, string(got))
	s.Contains(r.LogValue().String(), "raw=")
}

func (s *RecordSuite) TestTheZeroRecordDoesNotMarshal() {
	_, err := codec.Marshal(Record{})
	s.Error(err)
}

func (s *RecordSuite) TestDurMsIsNotRead() {
	run, ok := s.decode(`{"ts":1,"kind":"result","status":"pass","dur_ms":900}`).Run()
	s.Require().True(ok)
	s.Zero(run.Duration)
}

func (s *RecordSuite) TestAScopeLinesProjectAndTargetAreIgnored() {
	r := s.decode(`{"ts":1,"project":"api","target":"build","kind":"scope"}`)
	scope, ok := r.Scope()
	s.Require().True(ok)
	s.Empty(scope.Undeclared)
}
