package viewer

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/egladman/magus/internal/journal"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventToProtoMapsEnums pins the string<->enum mapping so a rename on either side
// is caught (the domain struct and the wire contract must not silently diverge).
func TestEventToProtoMapsEnums(t *testing.T) {
	r := journal.Event{
		Ts: 1700, Project: "web", Target: "test",
		Kind: journal.KindResult, Stream: journal.StreamStderr, Level: "error",
		Status: journal.StatusFail, Ref: "out1a2b3c", DurMs: 42, Text: "boom",
	}
	p := eventToProto(r)
	assert.Equal(t, int64(1700), p.GetTime().AsTime().UnixMilli(), "ts -> google.protobuf.Timestamp")
	assert.Equal(t, "web", p.GetProject())
	assert.Equal(t, "test", p.GetTarget())
	assert.Equal(t, viewerv1.Kind_KIND_RESULT, p.GetKind())
	assert.Equal(t, viewerv1.Stream_STREAM_STDERR, p.GetStream())
	assert.Equal(t, viewerv1.Status_STATUS_FAIL, p.GetStatus())
	assert.Equal(t, "out1a2b3c", p.GetRef())
	assert.Equal(t, 42*time.Millisecond, p.GetDuration().AsDuration(), "dur_ms -> google.protobuf.Duration")
	assert.Equal(t, "boom", p.GetText())

	// A zero timestamp maps to an unset (nil) field, not epoch.
	assert.Nil(t, eventToProto(journal.Event{Kind: journal.KindOutput}).GetTime())

	out := eventToProto(journal.Event{Kind: journal.KindOutput, Stream: journal.StreamStdout})
	assert.Equal(t, viewerv1.Kind_KIND_OUTPUT, out.GetKind())
	assert.Equal(t, viewerv1.Stream_STREAM_STDOUT, out.GetStream())
	assert.Equal(t, viewerv1.Kind_KIND_UNSPECIFIED, eventToProto(journal.Event{Kind: "bogus"}).GetKind())
}

// TestEventToProtoCarriesStartedCommand confirms the KindStarted event's command and
// version map onto the wire event, so a live stream (no Journal header) still learns the
// run's identity from its first frame; a non-started event leaves them unset.
func TestEventToProtoCarriesStartedCommand(t *testing.T) {
	started := eventToProto(journal.Event{
		Kind:         journal.KindStarted,
		MagusVersion: "v9",
		Command:      &journal.Command{Arguments: []string{"affected", "ci"}, Trigger: journal.TriggerCI},
	})
	assert.Equal(t, viewerv1.Kind_KIND_STARTED, started.GetKind())
	assert.Equal(t, "v9", started.GetMagusVersion())
	assert.Equal(t, []string{"affected", "ci"}, started.GetCommand().GetArguments())
	assert.Equal(t, viewerv1.Trigger_TRIGGER_CI, started.GetCommand().GetTrigger())

	plain := eventToProto(journal.Event{Kind: journal.KindOutput, Text: "hi"})
	assert.Nil(t, plain.GetCommand(), "non-started events carry no command")
	assert.Empty(t, plain.GetMagusVersion())
}

// TestEncodeJournalFragmentRoundTrip confirms the static wire envelope decodes back to
// the same Journal: base64url -> gunzip -> proto, exactly what the JS client does.
func TestEncodeJournalFragmentRoundTrip(t *testing.T) {
	events := []journal.Event{
		{Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: "building..."},
		{Kind: journal.KindResult, Project: "web", Target: "build", Status: journal.StatusPass, Ref: "outabc", DurMs: 10},
	}
	inv := journal.Invocation{ID: "inv7", MagusVersion: "v1.2.3", Command: journal.Command{Arguments: []string{"affected", "ci"}, Trigger: journal.TriggerCI}}
	frag, err := EncodeJournalFragment(inv, events)
	require.NoError(t, err)

	gzipped, err := base64.RawURLEncoding.DecodeString(frag)
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(gzipped))
	require.NoError(t, err)
	raw, err := io.ReadAll(zr)
	require.NoError(t, err)

	var got viewerv1.Journal
	require.NoError(t, proto.Unmarshal(raw, &got))
	assert.Equal(t, "inv7", got.GetInvocation().GetId())
	assert.Equal(t, "v1.2.3", got.GetInvocation().GetMagusVersion())
	assert.Equal(t, viewerv1.Trigger_TRIGGER_CI, got.GetInvocation().GetCommand().GetTrigger())
	require.Len(t, got.GetEvents(), 2)
	assert.Equal(t, "building...", got.GetEvents()[0].GetText())
	assert.Equal(t, viewerv1.Status_STATUS_PASS, got.GetEvents()[1].GetStatus())
	assert.Equal(t, "outabc", got.GetEvents()[1].GetRef())
}

// TestEnumMappingsExhaustive walks every domain enum value through its proto mapper so a new
// constant added on either side (or a renamed one) is caught. The default/unknown arm maps to
// the UNSPECIFIED zero value.
func TestEnumMappingsExhaustive(t *testing.T) {
	kinds := map[string]viewerv1.Kind{
		journal.KindStarted:  viewerv1.Kind_KIND_STARTED,
		journal.KindFinished: viewerv1.Kind_KIND_FINISHED,
		journal.KindExec:     viewerv1.Kind_KIND_EXEC,
		journal.KindOutput:   viewerv1.Kind_KIND_OUTPUT,
		journal.KindResult:   viewerv1.Kind_KIND_RESULT,
		journal.KindScope:    viewerv1.Kind_KIND_SCOPE,
		journal.KindWarn:     viewerv1.Kind_KIND_WARN,
		"bogus":              viewerv1.Kind_KIND_UNSPECIFIED,
	}
	for in, want := range kinds {
		assert.Equal(t, want, kindToProto(in), "kindToProto(%q)", in)
	}

	streams := map[string]viewerv1.Stream{
		journal.StreamStdout: viewerv1.Stream_STREAM_STDOUT,
		journal.StreamStderr: viewerv1.Stream_STREAM_STDERR,
		"bogus":              viewerv1.Stream_STREAM_UNSPECIFIED,
	}
	for in, want := range streams {
		assert.Equal(t, want, streamToProto(in), "streamToProto(%q)", in)
	}

	statuses := map[string]viewerv1.Status{
		journal.StatusPass:   viewerv1.Status_STATUS_PASS,
		journal.StatusFail:   viewerv1.Status_STATUS_FAIL,
		journal.StatusCached: viewerv1.Status_STATUS_CACHED,
		"bogus":              viewerv1.Status_STATUS_UNSPECIFIED,
	}
	for in, want := range statuses {
		assert.Equal(t, want, statusToProto(in), "statusToProto(%q)", in)
	}

	triggers := map[string]viewerv1.Trigger{
		journal.TriggerRun:      viewerv1.Trigger_TRIGGER_RUN,
		journal.TriggerAffected: viewerv1.Trigger_TRIGGER_AFFECTED,
		journal.TriggerCI:       viewerv1.Trigger_TRIGGER_CI,
		journal.TriggerX:        viewerv1.Trigger_TRIGGER_X,
		journal.TriggerWatch:    viewerv1.Trigger_TRIGGER_WATCH,
		journal.TriggerDirect:   viewerv1.Trigger_TRIGGER_DIRECT,
		"bogus":                 viewerv1.Trigger_TRIGGER_UNSPECIFIED,
	}
	for in, want := range triggers {
		assert.Equal(t, want, triggerToProto(in), "triggerToProto(%q)", in)
	}
}

// TestInvocationToProtoMapsHeader pins the invocation-header mapping (id/command/timing/version)
// used by the static Journal envelope.
func TestInvocationToProtoMapsHeader(t *testing.T) {
	p := invocationToProto(journal.Invocation{
		ID: "inv42", StartedMs: 1000, FinishedMs: 2000, MagusVersion: "v3",
		Command: journal.Command{Arguments: []string{"run", "build"}, Cwd: "/w", Trigger: journal.TriggerWatch},
	})
	assert.Equal(t, "inv42", p.GetId())
	assert.Equal(t, "v3", p.GetMagusVersion())
	assert.Equal(t, int64(1000), p.GetStartTime().AsTime().UnixMilli())
	assert.Equal(t, int64(2000), p.GetEndTime().AsTime().UnixMilli())
	assert.Equal(t, []string{"run", "build"}, p.GetCommand().GetArguments())
	assert.Equal(t, "/w", p.GetCommand().GetCwd())
	assert.Equal(t, viewerv1.Trigger_TRIGGER_WATCH, p.GetCommand().GetTrigger())
}

// TestEncodeEventRoundTrip confirms one SSE event decodes back: base64 -> proto.
func TestEncodeEventRoundTrip(t *testing.T) {
	ev, err := EncodeEvent(journal.Event{Kind: journal.KindOutput, Stream: journal.StreamStderr, Text: "warn: x"})
	require.NoError(t, err)
	raw, err := base64.StdEncoding.DecodeString(ev)
	require.NoError(t, err)
	var got viewerv1.Event
	require.NoError(t, proto.Unmarshal(raw, &got))
	assert.Equal(t, "warn: x", got.GetText())
	assert.Equal(t, viewerv1.Stream_STREAM_STDERR, got.GetStream())
}
