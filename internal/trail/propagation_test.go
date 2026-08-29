package trail

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The W3C shape is the whole contract, so the table is written against the spec's rules rather
// than against magus's use of them: a value magus half-accepts is one it would record a
// half-true trace from.
func TestParseTraceparent(t *testing.T) {
	const (
		trace = "4bf92f3577b34da6a3ce929d0e0e4736"
		span  = "00f067aa0ba902b7"
	)

	for name, tc := range map[string]struct {
		value string
		want  Spawn
	}{
		"a sampled traceparent": {
			"00-" + trace + "-" + span + "-01",
			Spawn{TraceID: trace, ParentSpanID: span, Flags: "01"},
		},
		"an unsampled one is still a trace": {
			"00-" + trace + "-" + span + "-00",
			Spawn{TraceID: trace, ParentSpanID: span, Flags: "00"},
		},
		"unset":                {"", Spawn{}},
		"a future version":     {"01-" + trace + "-" + span + "-01", Spawn{}},
		"an all-zero trace id": {"00-" + strings.Repeat("0", 32) + "-" + span + "-01", Spawn{}},
		"an all-zero span id":  {"00-" + trace + "-" + strings.Repeat("0", 16) + "-01", Spawn{}},
		"uppercase hex":        {"00-" + strings.ToUpper(trace) + "-" + span + "-01", Spawn{}},
		"a short trace id":     {"00-" + trace[:31] + "-" + span + "-01", Spawn{}},
		"a long span id":       {"00-" + trace + "-" + span + "0-01", Spawn{}},
		"a missing field":      {"00-" + trace + "-" + span, Spawn{}},
		"an extra field":       {"00-" + trace + "-" + span + "-01-x", Spawn{}},
		"not hex at all":       {"00-" + strings.Repeat("z", 32) + "-" + span + "-01", Spawn{}},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, parseTraceparent(tc.value))
		})
	}
}

// A malformed value is dropped either way; only the note says why, and it must name the shape
// rather than echo a string that failed the check that makes it safe to log.
func TestParseTraceparent_NotesOnceAndNeverEchoesTheValue(t *testing.T) {
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

func TestParseBaggage(t *testing.T) {
	for name, tc := range map[string]struct {
		value          string
		wantDelegation string
		wantSpawner    string
	}{
		"both members":              {"magus.delegation=fleet/f3,magus.spawner=claude", "fleet/f3", "claude"},
		"other tenants are skipped": {"userId=alice,magus.delegation=fleet/f3,serverNode=DF28", "fleet/f3", ""},
		"properties are ignored":    {"magus.delegation=fleet/f3;prop=1,magus.spawner=claude;x", "fleet/f3", "claude"},
		"whitespace is formatting":  {" magus.delegation = fleet/f3 , magus.spawner = claude ", "fleet/f3", "claude"},
		"the value is percent-decoded": {
			"magus.spawner=claude%20code%2Fsession%201", "", "claude code/session 1",
		},
		"a plus is a plus, not a space": {"magus.spawner=wave+2", "", "wave+2"},
		"a member with no = is skipped": {"broken,magus.delegation=fleet/f3", "fleet/f3", ""},
		"unset":                         {"", "", ""},
		// The id rule every delegation channel shares: an id magus cannot vouch for is dropped,
		// because the trail exempts a delegation from redaction.
		"a delegation that is not an id": {"magus.delegation=two%20words", "", ""},
		"an empty delegation":            {"magus.delegation=", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			baggageNoteOnce = sync.Once{}
			t.Cleanup(func() { baggageNoteOnce = sync.Once{} })

			delegation, spawner := parseBaggage(tc.value)
			assert.Equal(t, tc.wantDelegation, delegation)
			assert.Equal(t, tc.wantSpawner, spawner)
		})
	}
}

// The label rides every session record this process writes, so an unbounded one is a cost every
// later read of the repository pays.
func TestParseBaggage_ClampsTheSpawnerLabel(t *testing.T) {
	_, spawner := parseBaggage("magus.spawner=" + strings.Repeat("s", MaxSpawnerLen+50))
	assert.Len(t, spawner, MaxSpawnerLen)
}

// The two channels are read independently: a host that exports one and not the other still
// attributes what it can, and neither absence is an error.
func TestSpawnFromEnv_ReadsTheTwoChannelsIndependently(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	t.Setenv(EnvTraceparent, "  "+traceparent+"  ")
	t.Setenv(EnvBaggage, BaggageDelegation+"=fleet/f3,"+BaggageSpawner+"=claude")

	spawn := SpawnFromEnv()
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", spawn.TraceID, "surrounding whitespace is formatting, not part of the value")
	assert.Equal(t, "00f067aa0ba902b7", spawn.ParentSpanID)
	assert.Equal(t, "01", spawn.Flags)
	assert.Equal(t, "fleet/f3", spawn.Delegation)
	assert.Equal(t, "claude", spawn.Spawner)
	assert.Equal(t, "fleet/f3", DelegationFromEnv())

	t.Setenv(EnvBaggage, "")
	assert.Empty(t, DelegationFromEnv(), "no baggage is a session that claims no delegation, not an error")
	assert.Equal(t, "4bf92f3577b34da6a3ce929d0e0e4736", SpawnFromEnv().TraceID, "the trace is independent of the delegation")

	t.Setenv(EnvTraceparent, "")
	assert.Equal(t, Spawn{}, SpawnFromEnv(), "a process a person started claims nothing at all")
}

// The one identity magus asserts rather than records. Distinct per call, because it is what a
// child session points at when it names its parent.
func TestNewSpanID(t *testing.T) {
	first, second := NewSpanID(), NewSpanID()
	assert.Len(t, first, 16)
	assert.True(t, lowerHex(first, 16), "a span id is 16 lowercase hex characters")
	assert.NotEqual(t, first, second)
}
