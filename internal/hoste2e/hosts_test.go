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

// hostDriver knows how to find, isolate, wire and drive one agent host headless.
type hostDriver struct {
	id     string // the harness spell and the --agent-name its wiring passes
	spell  string
	binary string
	model  string

	path    string
	version string
	note    string
	// unauthenticated is the host's own words when it refused a turn for want of a
	// login; every later scenario skips, since the suite never logs a host in.
	unauthenticated string

	// env isolates the host's own config and state from the user's.
	env func(t *testing.T, w *workspace) []string
	// wire adds what magus leaves to the user, the MCP registration, after the merge.
	wire func(t *testing.T, w *workspace)
	// turn sends one prompt into conv and returns the reply text the host printed.
	turn func(t *testing.T, w *workspace, h *hostDriver, conv *conversation, prompt string) (string, hostRun)
	// skips names the scenarios this suite cannot drive on this host, with the reason.
	skips map[string]string
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

// locate resolves the binary: MAGUS_E2E_<HOST>_BIN first, then candidates in order,
// keeping the first that answers --version. It never installs or upgrades anything.
func (h *hostDriver) locate(envName string, candidates ...[2]string) {
	if p := os.Getenv("MAGUS_E2E_" + envName + "_BIN"); p != "" {
		candidates = append([][2]string{{p, "MAGUS_E2E_" + envName + "_BIN"}}, candidates...)
	}
	if p, err := exec.LookPath(h.binary); err == nil {
		candidates = append(candidates, [2]string{p, "PATH"})
	}
	var tried []string
	for _, c := range candidates {
		if c[0] == "" {
			continue
		}
		v, err := versionOf(c[0])
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%s): %v", c[0], c[1], err))
			continue
		}
		h.path, h.version = c[0], v
		h.note = "from " + c[1]
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
		reply, run := h.turn(t, w, h, conv, p)
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

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "..." + s[len(s)-n:]
}
