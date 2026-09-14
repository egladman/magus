package trail

import (
	"cmp"
	"slices"
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
)

// GuardFeedback is a deduplicated pattern in pre-tool observations. It is not
// a claim that the host executed a replacement command: pre-tool hooks cannot
// observe that outcome. Followed counts only later requests for `magus run` in
// the same host session.
type GuardFeedback struct {
	Rule             string    `json:"rule"`
	Surface          string    `json:"surface"`
	Denied           int       `json:"denied"`
	Sessions         int       `json:"sessions"`
	FollowedSessions int       `json:"followed_sessions"`
	Latest           time.Time `json:"latest"`
	Evidence         []string  `json:"evidence,omitempty"`
}

// NeedsReview holds the deliberately conservative threshold for a proposed
// improvement: three repeats in one session, or the same pattern in two host
// sessions. A one-off denial is a correction in progress, not evidence that a
// skill, rule, or host integration should change.
func (f GuardFeedback) NeedsReview() bool {
	return f.Denied >= 3 || f.Sessions >= 2
}

// RecentGuardFeedback derives candidates from existing hook activity. It does
// not write a second journal: agent-command activity is the source of truth,
// and a derived view avoids divergence between what the guard saw and what a
// later improvement review reports.
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
	for _, event := range events {
		if event.Kind != KindAgentCommand || event.RequestRef == "" || event.ResponseRef == "" {
			continue
		}
		var request agentCommandRequest
		var response agentCommandResponse
		raw, readErr := ReadBlob(base, event.RequestRef)
		if readErr != nil || json.Unmarshal(raw, &request) != nil {
			continue
		}
		raw, readErr = ReadBlob(base, event.ResponseRef)
		if readErr != nil || json.Unmarshal(raw, &response) != nil {
			continue
		}
		if session != "" && request.Session != session {
			continue
		}
		key := sessionKey{host: request.Host, session: request.Session}
		// No host session means no safe causal join. Keep the observation in the
		// activity trail, but do not guess which later command followed it.
		if key.session == "" {
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
			c = &candidate{GuardFeedback: GuardFeedback{Rule: response.Rule, Surface: request.Tool}, perSession: map[sessionKey]int{}, followed: map[sessionKey]bool{}}
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
		return cmp.Compare(a.Rule+"\x00"+a.Surface, b.Rule+"\x00"+b.Surface)
	})
	return out, nil
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
