package stream

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

type EventSuite struct{ suite.Suite }

func TestEvent(t *testing.T) { suite.Run(t, new(EventSuite)) }

// decode reads one wire line through Event's own decoder.
func decode(line []byte) (Event, error) {
	var e Event
	err := codec.Unmarshal(line, &e)
	return e, err
}

// TestMarshalsFlat pins the wire shape an integrator parses: envelope
// fields and body fields at ONE level, never nested under a data key. A change
// here breaks every client, so it asserts the literal line.
func (s *EventSuite) TestMarshalsFlat() {
	e := Event{
		Ts:        1756312800000,
		Workspace: "/repo",
		Inv:       "inv1a2b3c",
		Body: Target{
			Project:    "cmd/magus",
			Target:     "build",
			Status:     "failed",
			CacheHit:   false,
			Ref:        "out_abc",
			DurationMs: 1200,
			Error:      "exit 1",
		},
	}
	got, err := codec.Marshal(e)
	s.Require().NoError(err)
	s.JSONEq(`{
		"api_version":"safetyscissors.journal-stream/v1alpha1","type":"target.result","ts":1756312800000,
		"workspace":"/repo","inv":"inv1a2b3c",
		"project":"cmd/magus","target":"build","status":"failed",
		"cache_hit":false,"ref":"out_abc","duration_ms":1200,"error":"exit 1"
	}`, string(got))
}

// TestEveryTypeRoundTrips ranges over EventTypes, so a type added without
// its decode arm fails here rather than at runtime.
func (s *EventSuite) TestEveryTypeRoundTrips() {
	bodies := map[EventType]Body{
		RunStarted:   Run{Phase: "started", Command: []string{"run", "build"}, Trigger: "run", MagusVersion: "0.5.0"},
		RunFinished:  Run{Phase: "finished", Status: "pass"},
		TargetResult: Target{Project: "api", Target: "test", Status: "ok", CacheHit: true, Ref: "outx", DurationMs: 12, Error: ""},
		TargetOutput: Output{Project: "api", Target: "test", Stream: "stderr", Text: "FAIL"},
	}
	for _, typ := range EventTypes() {
		body, ok := bodies[typ]
		s.Require().True(ok, "taxonomy type %q has no round-trip case; add one", typ)
		want := Event{Ts: 1756312800000, Workspace: "/repo", Inv: "inv1", Body: body}
		line, err := codec.Marshal(want)
		s.Require().NoError(err)
		got, err := decode(line)
		s.Require().NoError(err)
		s.Equal(want, got)
		s.Equal(typ, got.Body.EventType())
	}
}

// TestANilBodyDoesNotMarshal: a typeless line cannot be parsed by any
// subscriber, so producing one fails at the producer.
func (s *EventSuite) TestANilBodyDoesNotMarshal() {
	_, err := codec.Marshal(Event{Ts: 1})
	s.ErrorContains(err, "no body")
}

func (s *EventSuite) TestABodyThatIsNotAnObjectDoesNotMarshal() {
	_, err := codec.Marshal(Event{Ts: 1, Body: scalarBody{}})
	s.ErrorContains(err, "want a JSON object")
}

// scalarBody marshals to a JSON scalar, which the splice cannot flatten.
type scalarBody struct{}

func (scalarBody) EventType() EventType         { return RunStarted }
func (scalarBody) MarshalJSON() ([]byte, error) { return []byte(`"nope"`), nil }

// TestRunPhaseDefaultsToFinished: a body with no phase must still close a
// run a subscriber has open. Leaving a spinner spinning forever is the worse
// failure, so the fallback is deliberate rather than an accident of ordering.
func (s *EventSuite) TestRunPhaseDefaultsToFinished() {
	s.Equal(RunFinished, Run{}.EventType())
	s.Equal(RunFinished, Run{Phase: "bogus"}.EventType())
	s.Equal(RunStarted, Run{Phase: "started"}.EventType())
}

// TestFilterZeroValueExcludesOutput: the default is everything EXCEPT
// target.output, which outnumbers the rest combined.
func (s *EventSuite) TestFilterZeroValueExcludesOutput() {
	var f Filter
	for _, typ := range EventTypes() {
		s.Equal(typ != TargetOutput, f.Allows(typ), "type %q", typ)
	}

	only := Filter{Types: []EventType{TargetOutput}}
	s.True(only.Allows(TargetOutput))
	s.False(only.Allows(TargetResult))
}

// TestDecodeRefusesAnUnknownType: a Go caller gets a refusal rather than a
// zero body it would have to test for. Line-oriented clients skip instead, which
// is what lets the taxonomy grow without a schema bump.
func (s *EventSuite) TestDecodeRefusesAnUnknownType() {
	_, err := decode([]byte(`{"api_version":"safetyscissors.journal-stream/v1alpha1","type":"target.reslut","ts":1}`))
	s.ErrorContains(err, `unknown type "target.reslut"`)
}

// TestDecodeAcceptsANewerSchema: the envelope contract is that
// additive changes keep old fields meaning what they meant, so a client built
// against v1alpha1 must not refuse a later line.
func (s *EventSuite) TestDecodeAcceptsANewerSchema() {
	got, err := decode([]byte(`{"api_version":"safetyscissors.journal-stream/v99","type":"run.finished","ts":7,"phase":"finished","status":"pass","future_field":true}`))
	s.Require().NoError(err)
	s.Equal(Run{Phase: "finished", Status: "pass"}, got.Body)
	s.Equal(int64(7), got.Ts)
}

// TestParseEventTypeRejectsTypos: an ignored typo presents as a stream
// that is merely quiet, which is the failure that costs an hour to find.
func (s *EventSuite) TestParseEventTypeRejectsTypos() {
	got, err := ParseEventType("target.result")
	s.Require().NoError(err)
	s.Equal(TargetResult, got)

	_, err = ParseEventType("target.reslut")
	s.ErrorContains(err, "unknown event type")
}

// TestOmitsInapplicableNumbers: a field that does not apply is absent, not
// zero, since "duration_ms":0 says a replay took no time. Under jsonv2,
// `omitempty` keeps a zero number, so these fields carry `omitzero`.
func (s *EventSuite) TestOmitsInapplicableNumbers() {
	cached, err := codec.Marshal(Event{Ts: 1, Body: Target{
		Project: "api", Target: "build", Status: "ok", CacheHit: true, Ref: "out_1",
	}})
	s.Require().NoError(err)
	s.NotContains(string(cached), "duration_ms", "a replay has no duration to report")

	started, err := codec.Marshal(Event{Ts: 1, Body: Run{Phase: "started"}})
	s.Require().NoError(err)
	s.NotContains(string(started), "duration_ms", "a run that just started has no duration")

	// Present when it genuinely applies, so the omission above is not just "never emitted".
	ran, err := codec.Marshal(Event{Ts: 1, Body: Target{Status: "ok", DurationMs: 1200}})
	s.Require().NoError(err)
	s.Contains(string(ran), `"duration_ms":1200`)
}
