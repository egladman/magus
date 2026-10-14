//go:build e2e

package hoste2e

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"
)

// hostSpec is what the suite knows about a host before it looks for one: how to
// isolate, wire and drive it.
type hostSpec struct {
	id     string // the harness spell and the --agent-name its wiring passes
	binary string
	model  string
	// login names the logins the host reads, for the skip a refused turn reports.
	login string

	// env isolates the host's own config and state from the user's.
	env func(t *testing.T, w *workspace) []string
	// wire adds what magus leaves to the user, the MCP registration, after the merge.
	wire func(t *testing.T, w *workspace)
	// turn sends one prompt into conv and returns the reply text the host printed.
	turn func(t *testing.T, w *workspace, conv *conversation, prompt string) (string, hostRun)
	// skips names the scenarios this suite cannot drive on this host, with the reason.
	skips map[string]string
}

// hostInstall is what the run finds out about a host on this machine.
type hostInstall struct {
	path    string
	version string
	note    string
	// unauthenticated is the host's own words when it refused a turn for want of a
	// login; every later scenario skips, since the suite never logs a host in.
	unauthenticated string
}

// hostDriver finds, isolates, wires and drives one agent host headless.
type hostDriver struct {
	hostSpec
	hostInstall
}

// conversation is one host session; a host that resumes keys the later turns on id.
type conversation struct {
	id    string
	turns int
}

func newConversation() *conversation {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return &conversation{id: fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])}
}

func envModel(name, fallback string) string {
	if m := os.Getenv("MAGUS_E2E_" + name + "_MODEL"); m != "" {
		return m
	}
	return fallback
}

func drivers() []*hostDriver {
	return []*hostDriver{claudeDriver(), codexDriver(), cursorDriver(), opencodeDriver()}
}

// candidate is a place a host binary might be, and the name the note gives for it.
type candidate struct {
	path   string
	source string
}

// locate resolves the binary: MAGUS_E2E_<HOST>_BIN first, then candidates in order,
// keeping the first that answers --version. It never installs or upgrades anything.
func (h *hostDriver) locate(envName string, candidates ...candidate) {
	if p := os.Getenv("MAGUS_E2E_" + envName + "_BIN"); p != "" {
		candidates = append([]candidate{{path: p, source: "MAGUS_E2E_" + envName + "_BIN"}}, candidates...)
	}
	if p, err := exec.LookPath(h.binary); err == nil {
		candidates = append(candidates, candidate{path: p, source: "PATH"})
	}
	var tried []string
	for _, c := range candidates {
		if c.path == "" {
			continue
		}
		v, err := versionOf(c.path)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%s): %v", c.path, c.source, err))
			continue
		}
		h.path, h.version = c.path, v
		h.note = "from " + c.source
		if len(tried) > 0 {
			h.note += "; passed over " + strings.Join(tried, "; ")
		}
		return
	}
	h.note = "absent: " + strings.Join(append(tried, h.binary+" not on PATH"), "; ")
}

func versionOf(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--version")
	cmd.Env = scrubbedEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, clip(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// session runs the prompts of one named conversation once per host and keeps the
// workspace, so every scenario reading that conversation's evidence shares one run.
type session struct {
	w       *workspace
	replies []string
	runs    []hostRun
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
		if run.err != nil {
			t.Logf("%s turn %d: %v\nstderr tail: %s", h.id, conv.turns, run.err, tail(run.stderr, 1500))
		}
		if m := authFailure.FindString(reply + "\n" + run.stdout + "\n" + run.stderr); m != "" {
			h.unauthenticated = m
			return s
		}
	}
	t.Logf("%s reply: %s", h.id, clip(s.lastReply()))
	return s
}

// notLoggedIn is the skip detail for a host that refused a turn for want of a login.
func (h *hostDriver) notLoggedIn() string {
	detail := h.binary + " is not logged in"
	if h.login != "" {
		detail += " (it reads " + h.login + ")"
	}
	return detail + ": " + h.unauthenticated
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
