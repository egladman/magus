package guard

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestReadCheckpointEnvelopeTakesOnlyThePointers(t *testing.T) {
	body := `{"hook_event_name":"Stop","session_id":"01a07dab","transcript_path":"/tmp/rollout.jsonl",` +
		`"last_assistant_message":"the model's closing words"}`

	session, transcript := ReadCheckpointEnvelope(strings.NewReader(body), false)

	assert.Equal(t, "01a07dab", session)
	assert.Equal(t, "/tmp/rollout.jsonl", transcript)
}

// Anything that is not a host envelope contributes nothing, and is never an error: this
// runs on a hook path where refusing to record would cost the session, not the payload.
func TestReadCheckpointEnvelopeIgnoresWhatItCannotUse(t *testing.T) {
	bodies := map[string]string{
		"empty":         "",
		"plain text":    "where was I",
		"broken json":   `{"session_id":`,
		"not an object": `["session_id"]`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			session, transcript := ReadCheckpointEnvelope(strings.NewReader(body), false)
			assert.Empty(t, session)
			assert.Empty(t, transcript)
		})
	}
}

// The hazard this bound exists for: a caller holding an idle pipe. Waiting on it forever
// trades two optional pointers for the record the command was invoked to write.
func TestReadCheckpointEnvelopeGivesUpOnAReaderThatNeverEnds(t *testing.T) {
	type pointers struct{ session, transcript string }
	done := make(chan pointers, 1)
	go func() {
		session, transcript := ReadCheckpointEnvelope(neverEOF{}, false)
		done <- pointers{session, transcript}
	}()

	select {
	case got := <-done:
		assert.Empty(t, got.session)
		assert.Empty(t, got.transcript)
	case <-time.After(checkpointEnvelopeWait + 5*time.Second):
		t.Fatal("ReadCheckpointEnvelope blocked on a reader that never closes")
	}
}

// neverEOF blocks forever without returning data or an error, the way an inherited pipe
// with no writer does.
type neverEOF struct{}

func (neverEOF) Read([]byte) (int, error) { select {} }
