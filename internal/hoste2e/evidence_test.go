//go:build e2e

package hoste2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/trail"
)

// record is one hook observation from the workspace's activity trail with both of its
// blobs decoded: what the host sent and what the guard answered.
type record struct {
	trail.Event
	Request  map[string]any
	Response map[string]any
}

func (r record) field(side map[string]any, key string) string {
	s, _ := side[key].(string)
	return s
}

// tool is magus's normalized name for the call (shell.command, file.write), not the
// host's tool name.
func (r record) tool() string     { return r.field(r.Request, "tool") }
func (r record) command() string  { return r.field(r.Request, "command") }
func (r record) path() string     { return r.field(r.Request, "path") }
func (r record) decision() string { return r.field(r.Response, "decision") }

// subject is what the call acted on, the key two judgments of one call share.
func (r record) subject() string { return r.tool() + "\x00" + r.command() + "\x00" + r.path() }

// records reads every agent_command and agent_spawn event the host's own hooks wrote
// after setup, oldest first. Setup's own records (the rule probe, and the synthetic
// events `magus agent harness verify` sends under the host's name) predate w.ready.
func (w *workspace) records(t *testing.T) []record {
	t.Helper()
	events, err := trail.ReadRecent(w.cacheDir(), 100000)
	if err != nil {
		t.Fatalf("read the activity trail: %v", err)
	}
	var out []record
	for i := len(events) - 1; i >= 0; i-- {
		e := events[i]
		if e.Host != w.host || e.Ts < w.ready || (e.Kind != trail.KindAgentCommand && e.Kind != trail.KindAgentSpawn) {
			continue
		}
		r := record{Event: e, Request: w.blob(t, e.RequestRef), Response: w.blob(t, e.ResponseRef)}
		out = append(out, r)
	}
	return out
}

func (w *workspace) blob(t *testing.T, ref string) map[string]any {
	t.Helper()
	if ref == "" {
		return map[string]any{}
	}
	body, err := trail.ReadBlob(w.cacheDir(), ref)
	if err != nil {
		t.Logf("trail blob %s: %v", ref, err)
		return map[string]any{}
	}
	m := map[string]any{}
	if err := json.Unmarshal(body, &m); err != nil {
		t.Logf("trail blob %s: %v", ref, err)
	}
	return m
}

func (w *workspace) dumpRecords(t *testing.T) {
	t.Helper()
	for _, r := range w.records(t) {
		t.Logf("  %d %s %s tool=%q cmd=%q path=%q decision=%q lease=%q decided_by=%q",
			r.Ts, r.Kind, r.Session, r.tool(), clip(r.command()), filepath.Base(r.path()), r.decision(), r.Lease, r.DecidedBy)
	}
}

// markers lists the guard's per-session marker files whose kind starts with kind.
func (w *workspace) markers(t *testing.T, kind string) []string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(w.cacheDir(), "advisories", "*."+kind+"*"))
	if err != nil {
		t.Fatal(err)
	}
	return matches
}

// spawnedAgent is the record the guard files under a subagent's id once the spawn call
// returns; ContextTokens is set when a later hook saw the child's transcript.
type spawnedAgent struct {
	Description   string `json:"description"`
	ContextTokens *int64 `json:"context_tokens"`
}

func (w *workspace) spawnedAgents(t *testing.T) []spawnedAgent {
	t.Helper()
	var out []spawnedAgent
	for _, path := range w.markers(t, "agent-spawned-") {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var rec spawnedAgent
		if err := json.Unmarshal(body, &rec); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, rec)
	}
	return out
}

type checkpoint struct {
	Host      string `json:"host"`
	Session   string `json:"host_session"`
	Workspace string `json:"workspace"`
}

// checkpoints reads `magus session -o json` against the workspace's own state.
func (w *workspace) checkpoints(t *testing.T) []checkpoint {
	t.Helper()
	var listing struct {
		Checkpoints []checkpoint `json:"checkpoints"`
	}
	if err := json.Unmarshal([]byte(w.magus(t, "session", "-o", "json", "--limit", "0")), &listing); err != nil {
		t.Fatalf("magus session -o json: %v", err)
	}
	return listing.Checkpoints
}

// proofreadReplies are the replies the stub proofread received from proofread-reply.buzz.
func (w *workspace) proofreadReplies(t *testing.T) []string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(w.proofreadCalls(), "*.reply"))
	var out []string
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(body))
	}
	return out
}

func clip(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
