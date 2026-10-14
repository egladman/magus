package trail

import (
	"cmp"
	"iter"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// GuardFeedback is a deduplicated pattern in pre-tool observations. It is not
// a claim that the host executed a replacement command: pre-tool hooks cannot
// observe that outcome. Followed counts only later requests for `magus run` in
// the same host session.
type GuardFeedback struct {
	Rule             string    `json:"rule"`
	Tool             string    `json:"tool"`
	Denied           int       `json:"denied"`
	Sessions         int       `json:"sessions"`
	FollowedSessions int       `json:"followed_sessions"`
	Latest           time.Time `json:"latest"`
	Evidence         []string  `json:"evidence,omitempty"`
}

// NeedsReview holds the deliberately conservative threshold for flagging a pattern:
// three repeats in one session, or the same pattern in two host sessions. A one-off
// denial is a correction in progress, not evidence that a skill, rule, or host
// integration should change.
func (f GuardFeedback) NeedsReview() bool {
	return f.Denied >= 3 || f.Sessions >= 2
}

// RecentGuardFeedback derives candidates from existing hook activity. It does
// not write a second journal: agent-command activity is the source of truth,
// and a derived view avoids divergence between what the guard saw and what a
// later report reads.
func RecentGuardFeedback(base, session string, limit int) ([]GuardFeedback, error) {
	events, err := ReadRecent(base, limit)
	if err != nil || len(events) == 0 {
		return nil, err
	}
	// ReadRecent is newest first; follow-up can only be recognized in causal
	// order, so inspect the retained window oldest first.
	slices.Reverse(events)

	type sessionKey struct{ host, session string }
	type candidate struct {
		GuardFeedback
		perSession map[sessionKey]int
		followed   map[sessionKey]bool
	}
	candidates := map[string]*candidate{}
	for command := range agentCommands(base, events) {
		event, request, response := command.event, command.request, command.response
		if session != "" && request.Session != session {
			continue
		}
		key := sessionKey{host: request.Host, session: request.Session}
		// No host session means no safe causal join. Keep the observation in the
		// activity trail, but do not guess which later command followed it.
		//
		// A missing HOST is refused for the same reason and one of its own: this
		// report exists to decide whether a host integration should change, and an
		// event no host claims cannot answer that. It also keeps a probe or a
		// hand-run hook from counting as one of the sessions NeedsReview weighs,
		// which would let a single operator manufacture the repeat that promotes a
		// candidate.
		if key.session == "" || key.host == "" {
			continue
		}
		if response.Decision != "deny" && isMagusRunRequest(request.Command) {
			for _, c := range candidates {
				if c.Rule == "raw-tool" && c.perSession[key] > 0 {
					c.followed[key] = true
				}
			}
			continue
		}
		if response.Decision != "deny" || response.Rule == "" {
			continue
		}
		fingerprint := response.Rule + "\x00" + request.Tool
		c := candidates[fingerprint]
		if c == nil {
			c = &candidate{GuardFeedback: GuardFeedback{Rule: response.Rule, Tool: request.Tool}, perSession: map[sessionKey]int{}, followed: map[sessionKey]bool{}}
			candidates[fingerprint] = c
		}
		c.Denied++
		c.perSession[key]++
		if at := time.UnixMilli(event.Ts); c.Latest.Before(at) {
			c.Latest = at
		}
	}

	out := make([]GuardFeedback, 0, len(candidates))
	for _, c := range candidates {
		c.Sessions = len(c.perSession)
		for key, count := range c.perSession {
			if c.followed[key] {
				c.FollowedSessions++
			}
			c.Evidence = append(c.Evidence, key.host+":"+key.session+" ("+itoa(count)+" denials)")
		}
		slices.Sort(c.Evidence)
		out = append(out, c.GuardFeedback)
	}
	slices.SortFunc(out, func(a, b GuardFeedback) int {
		if c := b.Latest.Compare(a.Latest); c != 0 {
			return c
		}
		if c := cmp.Compare(b.Denied, a.Denied); c != 0 {
			return c
		}
		return cmp.Compare(a.Rule+"\x00"+a.Tool, b.Rule+"\x00"+b.Tool)
	})
	return out, nil
}

// LeaseDenials are the guard's denials of a caller acting under lease since since (unix
// seconds), oldest first and at most limit of them, the newest kept. The bool says the
// retained trail reaches back to since, so an empty result means none rather than unknown:
// the whole trail fits the retained window, or it holds an agent command at or before since.
// A missing trail is an empty one; an empty base or lease has no trail to read.
func LeaseDenials(base, lease string, since int64, limit int) ([]types.JobDenial, bool, error) {
	if base == "" || lease == "" || limit <= 0 {
		return nil, false, nil
	}
	events, err := ReadRecent(base, maxEvents)
	if err != nil {
		return nil, false, err
	}
	slices.Reverse(events)
	from := since * 1000
	// Rotation keeps a floor of every kind, so only an agent command dates how far back the
	// agent commands reach.
	known := len(events) < maxEvents || slices.ContainsFunc(events, func(e Event) bool {
		return e.Kind == KindAgentCommand && e.Ts <= from
	})
	held := slices.DeleteFunc(events, func(e Event) bool { return e.Lease != lease || e.Ts < from })
	var out []types.JobDenial
	for command := range agentCommands(base, held) {
		if command.response.Decision != "deny" {
			continue
		}
		out = append(out, types.JobDenial{
			Rule:       command.response.Rule,
			Reason:     command.response.Reason,
			Action:     command.event.Action,
			VerdictRef: command.event.VerdictRef,
			Ts:         command.event.Ts / 1000,
		})
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, known, nil
}

// agentCommand is one agent-command event with its request and response blobs decoded.
type agentCommand struct {
	event    Event
	request  agentCommandRequest
	response agentCommandResponse
}

// agentCommands yields the agent-command events among events, in their order, whose request
// and response blobs both read and decode. An event whose blobs rotated away is skipped.
func agentCommands(base string, events []Event) iter.Seq[agentCommand] {
	return func(yield func(agentCommand) bool) {
		for _, event := range events {
			if event.Kind != KindAgentCommand || event.RequestRef == "" || event.ResponseRef == "" {
				continue
			}
			command := agentCommand{event: event}
			raw, err := ReadBlob(base, event.RequestRef)
			if err != nil || json.Unmarshal(raw, &command.request) != nil {
				continue
			}
			raw, err = ReadBlob(base, event.ResponseRef)
			if err != nil || json.Unmarshal(raw, &command.response) != nil {
				continue
			}
			if !yield(command) {
				return
			}
		}
	}
}

func isMagusRunRequest(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "magus" && fields[1] == "run"
}

func itoa(v int) string {
	// A tiny local formatter avoids making the aggregation depend on CLI output
	// helpers. The range is bounded by the retained trail window.
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
