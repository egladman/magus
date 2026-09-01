package interactive

import (
	"fmt"
	"io"
	"sync"
)

// hintsEnabled controls actionable hints; it is unrelated to TTY detection.
var hintsEnabled = true

// SetHintsEnabled sets whether actionable hints are emitted.
func SetHintsEnabled(on bool) {
	hintsEnabled = on
}

// HintsEnabled reports whether actionable hints are active.
func HintsEnabled() bool {
	return hintsEnabled
}

// emitted is the set of hint texts already shown, so a fact repeated across
// many targets/charms/calls in one run (or one long-lived daemon) teaches
// once instead of nagging on every occurrence.
var (
	emittedMu sync.Mutex
	emitted   = make(map[string]struct{})
)

// maxEmittedDedupe bounds the dedupe set. The daemon hosting Emit calls never
// restarts to clear it, so an unbounded map would be a slow leak; once full it
// resets and hints start teaching again rather than growing forever.
const maxEmittedDedupe = 4096

// Emit writes "hint: <msg>\n" to w when hints are enabled, once per distinct
// msg - see emitted.
func Emit(w io.Writer, msg string) {
	if !HintsEnabled() {
		return
	}
	emittedMu.Lock()
	if _, ok := emitted[msg]; ok {
		emittedMu.Unlock()
		return
	}
	if len(emitted) >= maxEmittedDedupe {
		emitted = make(map[string]struct{})
	}
	emitted[msg] = struct{}{}
	emittedMu.Unlock()
	fmt.Fprintf(w, "hint: %s\n", msg)
}
