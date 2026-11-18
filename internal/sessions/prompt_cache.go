package sessions

import (
	"time"

	"github.com/egladman/magus/internal/recorder"
)

// PromptCacheForCheckout resolves the prompt-cache clock from the newest guard
// observation in the trail under cacheDir, which is where a hook wired in THIS checkout
// wrote.
//
// The checkout rather than the repository, unlike everything else the session listing
// reads: a session's tool calls land in the cache dir of the tree they ran against,
// and the question this answers is about the agent sitting in front of you.
//
// An empty clock for a checkout whose trail holds nothing: no hook wired, or a tree
// nobody has run an agent against. Nothing is printed then, because the alternative is
// a clock counting up from an activity magus never saw.
func PromptCacheForCheckout(cacheDir string, now time.Time) PromptCacheClock {
	act, ok := recorder.LastAgentActivity(cacheDir)
	if !ok {
		return PromptCacheClock{}
	}
	return PromptCacheAt(act.Session, act.At, now)
}
