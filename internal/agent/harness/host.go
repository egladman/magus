//go:build e2e

package harness

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/testkit/e2e"
)

// session is one conversation's single run on one host. Every scenario
// reading that conversation's evidence shares its workspace.
type session struct {
	w       *workspace
	replies []string
	runs    []e2e.Result
}

var authFailure = regexp.MustCompile(`(?i)failed to authenticate[^\n"]*|not logged in[^\n"]*|authentication required[^\n"]*|401 unauthorized[^\n"]*`)

func (s *session) lastReply() string {
	if len(s.replies) == 0 {
		return ""
	}
	return s.replies[len(s.replies)-1]
}

func (s *session) replied(code string) bool {
	for _, r := range s.replies {
		if strings.Contains(r, code) {
			return true
		}
	}
	return false
}

func (h *hostDriver) play(t, owner *testing.T, prompts []string) *session {
	t.Helper()
	w := newWorkspace(t, owner, h)
	s := &session{w: w}
	conv := newConversation()
	for _, p := range prompts {
		reply, run := h.turn(t, w, conv, p)
		conv.turns++
		s.replies = append(s.replies, reply)
		s.runs = append(s.runs, run)
		if run.Err != nil {
			t.Logf("%s turn %d: %v\nstderr tail: %s", h.id, conv.turns, run.Err, tail(run.Stderr, 1500))
		}
		if m := authFailure.FindString(strings.Join([]string{reply, run.Stdout, run.Stderr}, "\n")); m != "" {
			h.unauthenticated = m
			return s
		}
	}
	t.Logf("%s reply: %s", h.id, clip(s.lastReply()))
	return s
}

// notLoggedIn is the skip detail for a host that refused a turn.
func (h *hostDriver) notLoggedIn() string {
	detail := h.binary + " is not logged in"
	if h.login != "" {
		detail = fmt.Sprintf("%s (it reads %s)", detail, h.login)
	}
	return fmt.Sprintf("%s: %s", detail, h.unauthenticated)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
