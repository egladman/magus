package types

import (
	json "github.com/egladman/magus/internal/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamEventMarshalsFlat pins the wire shape an integrator parses: envelope
// fields and body fields at ONE level, never nested under a data key. A change
// here breaks every client, so it asserts the literal line.
func TestStreamEventMarshalsFlat(t *testing.T) {
	e := StreamEvent{
		Ts:        1756312800000,
		Workspace: "/repo",
		Inv:       "inv1a2b3c",
		Body: StreamTarget{
			Project:    "cmd/magus",
			Target:     "build",
			Status:     "failed",
			CacheHit:   false,
			Ref:        "out_abc",
			DurationMs: 1200,
			Error:      "exit 1",
		},
	}
	got, err := json.Marshal(e)
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"schema":1,"type":"target.result","ts":1756312800000,
		"workspace":"/repo","inv":"inv1a2b3c",
		"project":"cmd/magus","target":"build","status":"failed",
		"cache_hit":false,"ref":"out_abc","duration_ms":1200,"error":"exit 1"
	}`, string(got))
}

// TestStreamEventRoundTrips: every type in the taxonomy survives encode/decode
// whole. Ranging over StreamEventTypes is what makes adding a type without
// wiring its decode arm a test failure rather than a runtime surprise.
func TestStreamEventRoundTrips(t *testing.T) {
	bodies := map[StreamEventType]StreamBody{
		StreamRunStarted:       StreamRun{Phase: "started", Command: []string{"run", "build"}, Trigger: "run", MagusVersion: "0.5.0"},
		StreamRunFinished:      StreamRun{Phase: "finished", Status: "pass", DurationMs: 4200},
		StreamTargetResult:     StreamTarget{Project: "api", Target: "test", Status: "ok", CacheHit: true, Ref: "out_x"},
		StreamTargetOutput:     StreamOutput{Project: "api", Target: "test", Stream: "stderr", Text: "FAIL"},
		StreamDiagnostic:       StreamDiagnosticBody{Code: "MGS1002", Message: "spell shadowed", Unit: "api:build", File: "spells/x/spell.buzz", Line: 3},
		StreamWorkspaceChanged: StreamChange{Paths: []string{"api/main.go"}},
		StreamAttentionRaised:  StreamAttention{ID: "att1", Request: Event{SchemaVersion: EventSchemaVersion, Outcome: OutcomeWaiting, Severity: SeverityNotice, Message: "blocked"}},
		StreamGuardVerdict:     StreamGuard{Verdict: "deny", Surface: "command", Subject: "go build ./...", Reason: "use magus run", Agent: "claude-code"},
	}

	for _, typ := range StreamEventTypes() {
		body, ok := bodies[typ]
		require.True(t, ok, "taxonomy type %q has no round-trip case; add one", typ)

		t.Run(string(typ), func(t *testing.T) {
			want := StreamEvent{Ts: 1756312800000, Workspace: "/repo", Inv: "inv1", Body: body}
			line, err := json.Marshal(want)
			require.NoError(t, err)

			got, err := DecodeStreamEvent(line)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, typ, got.Body.StreamType())
		})
	}
}

// TestStreamEventRejectsUnusableBody: a typeless or non-object line cannot be
// parsed by any subscriber, so producing one has to fail at the producer where
// it can still be diagnosed.
func TestStreamEventRejectsUnusableBody(t *testing.T) {
	t.Run("nil body", func(t *testing.T) {
		_, err := json.Marshal(StreamEvent{Ts: 1})
		assert.ErrorContains(t, err, "no body")
	})

	t.Run("body that is not an object", func(t *testing.T) {
		_, err := json.Marshal(StreamEvent{Ts: 1, Body: scalarBody{}})
		assert.ErrorContains(t, err, "want a JSON object")
	})
}

// scalarBody marshals to a JSON scalar, which the splice cannot flatten.
type scalarBody struct{}

func (scalarBody) StreamType() StreamEventType  { return StreamRunStarted }
func (scalarBody) MarshalJSON() ([]byte, error) { return []byte(`"nope"`), nil }

// TestStreamRunPhaseDefaultsToFinished: a body with no phase must still close a
// run a subscriber has open. Leaving a spinner spinning forever is the worse
// failure, so the fallback is deliberate rather than an accident of ordering.
func TestStreamRunPhaseDefaultsToFinished(t *testing.T) {
	assert.Equal(t, StreamRunFinished, StreamRun{}.StreamType())
	assert.Equal(t, StreamRunFinished, StreamRun{Phase: "bogus"}.StreamType())
	assert.Equal(t, StreamRunStarted, StreamRun{Phase: "started"}.StreamType())
}

// TestStreamFilterZeroValueExcludesOutput pins the one asymmetry in the filter:
// the default is everything EXCEPT target.output, because that type outnumbers
// the rest combined and a subscriber cannot recover from being sent it unasked.
func TestStreamFilterZeroValueExcludesOutput(t *testing.T) {
	var f StreamFilter
	for _, typ := range StreamEventTypes() {
		assert.Equal(t, typ != StreamTargetOutput, f.Allows(typ), "type %q", typ)
	}

	only := StreamFilter{Types: []StreamEventType{StreamTargetOutput}}
	assert.True(t, only.Allows(StreamTargetOutput))
	assert.False(t, only.Allows(StreamTargetResult))
}

// TestDecodeStreamEventUnknownType: a Go caller gets a refusal rather than a
// zero body it would have to test for. Line-oriented clients skip instead, which
// is what lets the taxonomy grow without a schema bump.
func TestDecodeStreamEventUnknownType(t *testing.T) {
	_, err := DecodeStreamEvent([]byte(`{"schema":1,"type":"target.reslut","ts":1}`))
	assert.ErrorContains(t, err, `unknown type "target.reslut"`)
}

// TestDecodeStreamEventAcceptsNewerSchema: the envelope contract is that
// additive changes keep old fields meaning what they meant, so a client built
// against schema 1 must not refuse a later line.
func TestDecodeStreamEventAcceptsNewerSchema(t *testing.T) {
	got, err := DecodeStreamEvent([]byte(`{"schema":99,"type":"run.finished","ts":7,"phase":"finished","status":"pass","future_field":true}`))
	require.NoError(t, err)
	assert.Equal(t, StreamRun{Phase: "finished", Status: "pass"}, got.Body)
	assert.Equal(t, int64(7), got.Ts)
}

// TestParseStreamEventTypeRejectsTypos: an ignored typo presents as a stream
// that is merely quiet, which is the failure that costs an hour to find.
func TestParseStreamEventTypeRejectsTypos(t *testing.T) {
	got, err := ParseStreamEventType("target.result")
	require.NoError(t, err)
	assert.Equal(t, StreamTargetResult, got)

	_, err = ParseStreamEventType("target.reslut")
	assert.ErrorContains(t, err, "unknown event type")
}

// TestStreamEventOmitsInapplicableNumbers pins a distinction the wire has to keep:
// a field that does not APPLY must be absent, not zero. A cached replay has no
// duration, and shipping "duration_ms":0 tells a subscriber the target took no
// time rather than that it never ran.
//
// It is a real regression guard rather than a restatement of the tag. Under the
// jsonv2 codec this repo builds with, `omitempty` omits only empty JSON values
// (null, "", [], {}) and NOT zero numbers, so the intuitive tag silently ships
// the zero. These fields carry `omitzero` for that reason.
func TestStreamEventOmitsInapplicableNumbers(t *testing.T) {
	cached, err := json.Marshal(StreamEvent{Ts: 1, Body: StreamTarget{
		Project: "api", Target: "build", Status: "ok", CacheHit: true, Ref: "out_1",
	}})
	require.NoError(t, err)
	assert.NotContains(t, string(cached), "duration_ms", "a replay has no duration to report")

	started, err := json.Marshal(StreamEvent{Ts: 1, Body: StreamRun{Phase: "started"}})
	require.NoError(t, err)
	assert.NotContains(t, string(started), "duration_ms", "a run that just started has no duration")

	// Present when it genuinely applies, so the omission above is not just "never emitted".
	ran, err := json.Marshal(StreamEvent{Ts: 1, Body: StreamTarget{Status: "ok", DurationMs: 1200}})
	require.NoError(t, err)
	assert.Contains(t, string(ran), `"duration_ms":1200`)
}
