package guard

import (
	"io"
	"time"
)

// checkpointEnvelopeWait bounds how long a host's payload may take to arrive.
//
// stdin is the checkpoint command's optional enrichment, not its input, so waiting on it
// forever trades two pointers for the record it was meant to write. A hook writes its
// event and closes; anything that has sent nothing by now is a shell that inherited an
// idle pipe, which no amount of further waiting improves.
const checkpointEnvelopeWait = 2 * time.Second

// checkpointEnvelopeMax bounds how much of it is read, matching the cap the adoption
// command puts on the same class of input.
const checkpointEnvelopeMax = 4 << 20

// ReadCheckpointEnvelope decodes a host's hook payload from in for the two pointers only
// a host knows: its session id and its transcript path. A payload that is absent,
// unparsable, or slow to arrive yields the zero envelope, which contributes nothing.
// interactive says in is a terminal, which is never read.
//
// Nothing in the payload becomes the note. A host's closing message is the model's
// prose, and copying it in would make the record indistinguishable from a sentence a
// person wrote, in a store a person reads. A wrapper that wants the model's words passes
// --note and owns that decision.
//
// The read is bounded rather than skipped when --note is set. Gating it on the flag also
// kept the pointers out of the record for any wrapper that passed a note, which is the
// shape the docs invite, and it left the actual hazard open: a caller with no --note and
// an idle inherited pipe still waited forever.
func ReadCheckpointEnvelope(in io.Reader, interactive bool) (session, transcript string) {
	if interactive {
		return "", ""
	}
	type read struct {
		body []byte
		err  error
	}
	done := make(chan read, 1)
	go func() {
		body, err := io.ReadAll(io.LimitReader(in, checkpointEnvelopeMax))
		done <- read{body: body, err: err}
	}()

	select {
	case r := <-done:
		if r.err != nil {
			return "", ""
		}
		return HostAttribution(string(r.body))
	case <-time.After(checkpointEnvelopeWait):
		return "", ""
	}
}
