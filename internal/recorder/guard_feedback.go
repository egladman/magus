package recorder

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
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
func RecentGuardFeedback(ctx context.Context, base, session string, limit int) ([]GuardFeedback, error) {
	events, err := recentCalls(ctx, base, limit)
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
	for command, err := range agentCommands(base, events) {
		if err != nil {
			return nil, err
		}
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
		if response.Decision != verdict.Deny && isMagusRunRequest(request.Command) {
			for _, c := range candidates {
				if c.Rule == "raw-tool" && c.perSession[key] > 0 {
					c.followed[key] = true
				}
			}
			continue
		}
		if response.Decision != verdict.Deny || response.Rule == "" {
			continue
		}
		fingerprint := fmt.Sprintf("%s\x00%s", response.Rule, request.Tool)
		c := candidates[fingerprint]
		if c == nil {
			c = &candidate{GuardFeedback: GuardFeedback{Rule: response.Rule, Tool: request.Tool}, perSession: map[sessionKey]int{}, followed: map[sessionKey]bool{}}
			candidates[fingerprint] = c
		}
		c.Denied++
		c.perSession[key]++
		if at := event.Time; c.Latest.Before(at) {
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
			c.Evidence = append(c.Evidence, fmt.Sprintf("%s:%s (%d denials)", key.host, key.session, count))
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
		return cmp.Compare(fmt.Sprintf("%s\x00%s", a.Rule, a.Tool), fmt.Sprintf("%s\x00%s", b.Rule, b.Tool))
	})
	return out, nil
}

// LeaseDenials are the guard's denials of a caller acting under lease since since (unix
// seconds), oldest first and at most limit of them, the newest kept. The bool says the
// retained trail reaches back to since, so an empty result means none rather than unknown:
// the whole trail fits the retained window, or it holds an agent command at or before since.
// A missing trail is an empty one; an empty base or lease has no trail to read.
func LeaseDenials(ctx context.Context, base, lease string, since int64, limit int) ([]job.Denial, bool, error) {
	if base == "" || lease == "" || limit <= 0 {
		return nil, false, nil
	}
	events, err := recentCalls(ctx, base, maxEvents)
	if err != nil {
		return nil, false, err
	}
	slices.Reverse(events)
	from := since * 1000
	// Rotation keeps a floor of every kind, so only an agent command dates how far back the
	// agent commands reach.
	known := len(events) < maxEvents || slices.ContainsFunc(events, func(e journal.Call) bool {
		return e.Kind == journal.CallKindAgentCommand && e.Time.UnixMilli() <= from
	})
	held := slices.DeleteFunc(events, func(e journal.Call) bool { return e.Lease != lease || e.Time.UnixMilli() < from })
	var out []job.Denial
	for command, err := range agentCommands(base, held) {
		if err != nil {
			return nil, false, err
		}
		if command.response.Decision != verdict.Deny {
			continue
		}
		out = append(out, job.Denial{
			Rule:       command.response.Rule,
			Reason:     command.response.Reason,
			Action:     command.event.Name,
			VerdictRef: verdictRef(command.event),
			Ts:         command.event.Time.Unix(),
		})
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, known, nil
}

// agentCommand is one agent-command event with its request and response blobs decoded.
type agentCommand struct {
	event    journal.Call
	request  AgentCommandRequest
	response AgentCommandResponse
}

// agentCommands yields the agent-command events among events, in their order, whose
// request and response blobs both read and decode. An event whose blobs rotated away is
// skipped; a blob holding a retired guard decision is yielded as a
// [*retiredDecisionError], and nothing after it.
func agentCommands(base string, events []journal.Call) iter.Seq2[agentCommand, error] {
	return func(yield func(agentCommand, error) bool) {
		var retired *retiredDecisionError
		for _, event := range events {
			if event.Kind != journal.CallKindAgentCommand || event.Request.Ref == "" || event.Response.Ref == "" {
				continue
			}
			command := agentCommand{event: event}
			raw, err := event.Request.Read(callsDir(base))
			if err != nil {
				continue
			}
			if err := decodeAgentBlob(base, event.Request.Ref, raw, &command.request); errors.As(err, &retired) {
				yield(agentCommand{}, err)
				return
			} else if err != nil {
				continue
			}
			raw, err = event.Response.Read(callsDir(base))
			if err != nil {
				continue
			}
			if err := decodeAgentBlob(base, event.Response.Ref, raw, &command.response); errors.As(err, &retired) {
				yield(agentCommand{}, err)
				return
			} else if err != nil {
				continue
			}
			if !yield(command, nil) {
				return
			}
		}
	}
}

func isMagusRunRequest(command string) bool {
	fields := strings.Fields(command)
	return len(fields) >= 2 && fields[0] == "magus" && fields[1] == "run"
}
