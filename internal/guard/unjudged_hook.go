package guard

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/ward"
)

// Unjudged is how a hook answers an event no guard policy could judge.
type Unjudged int

const (
	// unjudgedRefused refuses the event: every call but the repair, a read included.
	unjudgedRefused Unjudged = iota
	// unjudgedRepair lets the repair line run.
	unjudgedRepair
	// unjudgedStop lets an agent end its turn and says why nothing judged it. Ending
	// is the one act that leaves nothing unjudged behind, and refusing a stop sends
	// the agent back to work no rule can judge.
	unjudgedStop
)

// ExitCode is the hook's exit for u: 0 for the repair; 2 for a refusal, the code a
// `magus shell` deny exits with and the shipped wirings read as the host's block; and 1
// for a stop, the failure a host shows and does not block on.
func (u Unjudged) ExitCode() int {
	switch u {
	case unjudgedRepair:
		return 0
	case unjudgedStop:
		return 1
	case unjudgedRefused:
	}
	return 2
}

// hookEventWait bounds the read of an event no host closed, so a caller that left stdin
// open is answered rather than hung.
const hookEventWait = 2 * time.Second

// Decision is the `magus shell` verdict that carries u: a pass for the repair, an advise
// for a stop, whose reason reaches the agent as it ends, and a deny for a refusal.
func (u Unjudged) Decision() string {
	switch u {
	case unjudgedRepair:
		return "pass"
	case unjudgedStop:
		return "advise"
	case unjudgedRefused:
	}
	return "deny"
}

// UnjudgedHook is [UnjudgedEvent] for the event on r, read until r closes; an event r
// has not closed on within two seconds reads as empty.
func UnjudgedHook(root string, r io.Reader, cause error) (answer Unjudged, reason string) {
	return UnjudgedEvent(root, readHookEvent(r, hookEventWait), cause)
}

// UnjudgedEvent answers a hook event that no guard policy can judge, because the magus
// asked could not load root: cause is why. It reads event as `magus shell` does, a host
// envelope keyed by field or a bare command line. A command equal to one of the
// workspace's repair lines, once trimmed, is the repair, and a stop event is a stop;
// every other event is refused, a read included. reason names cause and the repair, ""
// for the repair.
func UnjudgedEvent(root, event string, cause error) (answer Unjudged, reason string) {
	lines := repairLines(root)
	command, isCommand := unjudgedCommand(event)
	if isCommand && slices.Contains(lines, strings.TrimSpace(command)) {
		return unjudgedRepair, ""
	}
	quoted := make([]string, len(lines))
	for i, l := range lines {
		quoted[i] = fmt.Sprintf("`%s`", l)
	}
	repair := fmt.Sprintf("Until a magus loads this workspace only its repair runs, alone on its line: %s",
		strings.Join(quoted, " or "))
	if _, stops := decodeStop(event); stops {
		return unjudgedStop, fmt.Sprintf("magus guard could not judge this stop, so it ends unjudged: %v\n%s", cause, repair)
	}
	return unjudgedRefused, fmt.Sprintf("magus guard could not judge this call, so it is refused: %v\n%s", cause, repair)
}

// repairLines are the shell lines that get root a magus able to judge it again, each to
// run alone on its line. They come from what root declares about how its magus is
// obtained: a checkout of magus's own module builds it from source (the relink over an
// existing ./magus, the bootstrap, then the checkout.bootstrap hook magus.yaml names),
// and any other workspace installs a release, pinned when required_version bounds it.
// Each source line appears bare and aimed at root, the two spellings the guard names.
func repairLines(root string) []string {
	if root == "" || !ownSourceRoot(root) {
		return []string{config.ReleaseCommand(config.RequiredVersionAt(root))}
	}
	var lines []string
	if hasMagusBinary(root) {
		lines = append(lines, ward.RelinkAt(root), ward.RelinkAt(""))
	}
	lines = append(lines, config.SourceCommand(root, "", "", true), ward.BootstrapCommand)
	if hook := config.CheckoutHookAt(root); hook != "" {
		lines = append(lines, config.ReadyCheckoutCommand(hook))
	}
	return lines
}

// readHookEvent is what r holds once closed, or "" when it stays open past wait or
// fails: an empty event judges nothing, so it is refused.
func readHookEvent(r io.Reader, wait time.Duration) string {
	read := make(chan []byte, 1)
	go func() {
		b, err := io.ReadAll(r)
		if err != nil {
			b = nil
		}
		read <- b
	}()
	select {
	case b := <-read:
		return string(b)
	case <-time.After(wait):
		return ""
	}
}

// unjudgedCommand is the shell line event carries, false for a write, a read, a spawn,
// an MCP call or an event with nothing to judge. Text that is no envelope is the line.
func unjudgedCommand(event string) (string, bool) {
	req, ok := decodeHookEnvelope(event, agent.ToolRows{})
	if !ok {
		return event, strings.TrimSpace(event) != ""
	}
	if req.Value == "" || req.IsPath || req.IsSpawn || req.IsContinue || req.MCPTool != "" ||
		req.Read != "" || req.Enters || req.NothingToJudge {
		return "", false
	}
	return req.Value, true
}
