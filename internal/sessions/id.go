package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strconv"
	"sync"

	"github.com/egladman/magus/internal/journal"
)

// NewID mints a session id for a producer that has no invocation id to reuse.
//
// A producer that HAS one must not call this. The invocation id is what joins a session
// fact back to the execution journal that produced it, so [FactHandler] uses the id off
// the event, and only the attention producers - each its own short-lived process, with
// no run to join to - mint here.
//
// It is [journal.NewInvocationID] plus a per-process suffix, and the suffix is the whole
// reason this function exists. That id is a millisecond stamp and a counter that resets
// with the process, so two magus processes starting in the same millisecond both mint
// the same first id. Attention producers hit exactly that case: `magus notify` fires
// from an agent hook, several hosts can fire at once, and each writes with its counter
// at 1. A collision is not a lost write - [Writer.resume] continues the numbering
// already in the file - but the two invocations then share one session, and the store
// says one command did what two did.
//
// The result satisfies the session-id rule [Open] enforces (alphanumeric with - and _),
// because it names the session file.
func NewID() string {
	return journal.NewInvocationID() + "-" + processSuffix()
}

// processSuffix is derived once per process. Re-deriving it per call would defeat the
// point: two ids minted by one process must still read as one process's work.
//
// Four random bytes, or the pid when the system entropy source is unavailable. The pid
// fallback is weaker on purpose rather than fatal: it still separates two processes on
// one machine, which is the collision this exists for, and only degrades for two hosts
// writing one store over a network mount - which the package doc already puts outside
// its guarantees.
var processSuffix = sync.OnceValue(func() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "p" + strconv.Itoa(os.Getpid())
	}
	return hex.EncodeToString(b[:])
})
