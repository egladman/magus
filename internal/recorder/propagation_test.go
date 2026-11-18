package recorder

import (
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type PropagationSuite struct{ suite.Suite }

func TestPropagation(t *testing.T) { suite.Run(t, new(PropagationSuite)) }

const (
	testTrace = "4bf92f3577b34da6a3ce929d0e0e4736"
	testSpan  = "00f067aa0ba902b7"
)

func (s *PropagationSuite) TestParseTraceparentReadsATrace() {
	s.Equal(Spawn{TraceID: testTrace, ParentSpanID: testSpan, Flags: "01"}, parseTraceparent("00-"+testTrace+"-"+testSpan+"-01"))
	s.Equal(Spawn{TraceID: testTrace, ParentSpanID: testSpan, Flags: "00"}, parseTraceparent("00-"+testTrace+"-"+testSpan+"-00"), "an unsampled one is still a trace")
	s.Equal(Spawn{}, parseTraceparent(""), "unset")
}

// The W3C shape is the whole contract, so these are written against the
// spec's rules: a value magus half-accepts would record a half-true trace.
func (s *PropagationSuite) TestParseTraceparentRefusesWhatTheSpecRefuses() {
	s.Zero(parseTraceparent("01-"+testTrace+"-"+testSpan+"-01"), "a future version")
	s.Zero(parseTraceparent(fmt.Sprintf("00-%s-%s-01", strings.Repeat("0", 32), testSpan)), "an all-zero trace id")
	s.Zero(parseTraceparent(fmt.Sprintf("00-%s-%s-01", testTrace, strings.Repeat("0", 16))), "an all-zero span id")
	s.Zero(parseTraceparent(fmt.Sprintf("00-%s-%s-01", strings.ToUpper(testTrace), testSpan)), "uppercase hex")
	s.Zero(parseTraceparent(fmt.Sprintf("00-%s-%s-01", testTrace[:31], testSpan)), "a short trace id")
	s.Zero(parseTraceparent("00-"+testTrace+"-"+testSpan+"0-01"), "a long span id")
	s.Zero(parseTraceparent("00-"+testTrace+"-"+testSpan), "a missing field")
	s.Zero(parseTraceparent("00-"+testTrace+"-"+testSpan+"-01-x"), "an extra field")
	s.Zero(parseTraceparent(fmt.Sprintf("00-%s-%s-01", strings.Repeat("z", 32), testSpan)), "not hex at all")
}

// A malformed value is dropped either way; only the note says why, and it must name the shape
// rather than echo a string that failed the check that makes it safe to log.
func (s *PropagationSuite) TestParseTraceparent_NotesOnceAndNeverEchoesTheValue() {
	t := s.T()
	traceparentNoteOnce = sync.Once{}
	t.Cleanup(func() { traceparentNoteOnce = sync.Once{} })

	logged := captureWarnings(t, func() {
		require.Empty(t, parseTraceparent("garbage-value").TraceID)
		require.Empty(t, parseTraceparent("garbage-value").TraceID)
	})

	assert.Equal(t, 1, strings.Count(logged, EnvTraceparent), "the environment cannot change under a running worker, so the note cannot repeat")
	assert.Contains(t, logged, "32 hex trace id", "the note names the shape the value failed")
	assert.NotContains(t, logged, "garbage-value")
}

// baggage is parseBaggage over v as one pair, so a case reads in one line.
func baggage(v string) [2]string {
	lease, spawner := parseBaggage(v)
	return [2]string{lease, spawner}
}

func (s *PropagationSuite) TestParseBaggageReadsBothMembers() {
	s.Equal([2]string{"fleet/f3", "claude"}, baggage("magus.lease=fleet/f3,magus.spawner=claude"))
	s.Equal([2]string{"fleet/f3", ""}, baggage("userId=alice,magus.lease=fleet/f3,serverNode=DF28"), "other tenants are skipped")
	s.Equal([2]string{"fleet/f3", "claude"}, baggage("magus.lease=fleet/f3;prop=1,magus.spawner=claude;x"), "properties are ignored")
	s.Equal([2]string{"fleet/f3", "claude"}, baggage(" magus.lease = fleet/f3 , magus.spawner = claude "), "whitespace is formatting")
	s.Equal([2]string{"fleet/f3", ""}, baggage("broken,magus.lease=fleet/f3"), "a member with no = is skipped")
	s.Equal([2]string{"", ""}, baggage(""), "unset")
}

func (s *PropagationSuite) TestParseBaggagePercentDecodesTheValue() {
	s.Equal([2]string{"", "claude code/session 1"}, baggage("magus.spawner=claude%20code%2Fsession%201"))
	s.Equal([2]string{"", "wave+2"}, baggage("magus.spawner=wave+2"), "a plus is a plus, not a space")
}

// The id rule every lease channel shares: an id magus cannot vouch for is
// dropped, because the log exempts a lease from redaction.
func (s *PropagationSuite) TestParseBaggageDropsALeaseThatIsNotAnID() {
	baggageLeaseNoteOnce = sync.Once{}
	s.T().Cleanup(func() { baggageLeaseNoteOnce = sync.Once{} })
	s.Equal([2]string{"", ""}, baggage("magus.lease=two%20words"))
	s.Equal([2]string{"", ""}, baggage("magus.lease="), "an empty lease")
}

// Each malformed shape has its own note: an oversized list first must not
// silence the note for a bad lease id after it.
func (s *PropagationSuite) TestParseBaggage_NotesEachShapeOnce() {
	t := s.T()
	baggageLengthNoteOnce, baggageLeaseNoteOnce = sync.Once{}, sync.Once{}
	t.Cleanup(func() { baggageLengthNoteOnce, baggageLeaseNoteOnce = sync.Once{}, sync.Once{} })

	logged := captureWarnings(t, func() {
		parseBaggage(strings.Repeat("x", maxBaggageLen+1))
		parseBaggage("magus.lease=two%20words")
	})
	assert.Contains(t, logged, "bounded set of comma-separated")
	assert.Contains(t, logged, "letters, digits")
}

// The label rides every session record this process writes, so an unbounded one is a cost every
// later read of the repository pays.
func (s *PropagationSuite) TestParseBaggage_ClampsTheSpawnerLabel() {
	t := s.T()
	_, spawner := parseBaggage("magus.spawner=" + strings.Repeat("s", MaxSpawnerLen+50))
	assert.Len(t, spawner, MaxSpawnerLen)
}

// The two channels are read independently: a host that exports one and not the other still
// attributes what it can, and neither absence is an error.
func (s *PropagationSuite) TestSpawnFromEnv_ReadsTheTwoChannelsIndependently() {
	t := s.T()
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	t.Setenv(EnvTraceparent, "  "+traceparent+"  ")
	t.Setenv(EnvBaggage, BaggageLease+"=fleet/f3,"+BaggageSpawner+"=claude")

	spawn := SpawnFromEnv()
	assert.Equal(t, Spawn{
		TraceID:      "4bf92f3577b34da6a3ce929d0e0e4736",
		ParentSpanID: "00f067aa0ba902b7",
		Flags:        "01",
		Lease:        "fleet/f3",
		Spawner:      "claude",
	}, spawn, "surrounding whitespace is formatting, not part of the value")
	assert.Equal(t, "fleet/f3", LeaseFromEnv())

	t.Setenv(EnvBaggage, "")
	assert.Empty(t, LeaseFromEnv(), "no baggage is a session that claims no lease, not an error")
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", SpawnFromEnv().TraceID, "the trace is independent of the lease")

	t.Setenv(EnvTraceparent, "")
	assert.Equal(t, Spawn{}, SpawnFromEnv(), "a process a person started claims nothing at all")
}

// The one identity magus asserts rather than records. Distinct per call, because it is what a
// child session points at when it names its parent.
func (s *PropagationSuite) TestNewSpanID() {
	t := s.T()
	first, second := NewSpanID(), NewSpanID()
	assert.Len(t, first, 16)
	assert.True(t, lowerHex(first, 16), "a span id is 16 lowercase hex characters")
	assert.NotEqual(t, first, second)
}
