//go:build e2e

package hoste2e

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
)

// conversationName picks one of the scripted sessions below.
type conversationName string

const (
	convTools conversationName = "tools"
	convSpawn conversationName = "spawn"
	convBrief conversationName = "brief"
)

// prompts is what the conversation sends a host. A Claude Code session reaches the
// brief only through a compaction, so convBrief swaps in that host's longer script.
func (c conversationName) prompts(hostID string) []string {
	if turns, ok := briefTurns[hostID]; ok && c == convBrief {
		return turns
	}
	return conversations[c]
}

// The prompts are scripts, not tasks: each step names the tool to use so the hook it
// should fire is not left to the model's choice.
var conversations = map[conversationName][]string{
	convTools: {`This is an automated test of tool hooks. Use your tools for each numbered step, in order. If a step is refused, or a hook adds a note, do not retry it; go on to the next step.
1. Run the shell command: echo e2e-deny-probe
2. Run the shell command: echo e2e-advise-probe
3. Read the file probe.txt with your file-read tool; use the shell only if you have no such tool.
4. Create the file gen/out.txt containing the line "hand edit", with your file-editing tool (write, edit or patch), not the shell.
5. Create the file blocked.txt containing the line "blocked", with your file-editing tool (write, edit or patch), not the shell.
6. Call the status tool of the magus MCP server once.
Finally reply with one line listing every code word (shaped like E2E-WORD-XXXXXXXX) that any refusal, error or hook note showed you. Context: we are checking this workspace's module boundary.`},
	convSpawn: {`This is an automated test of subagent hooks.
1. Load the skill named magus-multi-agent with your skill tool. With no skill tool, read its SKILL.md under .claude/skills, .agents/skills, .cursor/skills or .opencode/skills.
2. Start exactly one subagent with your subagent tool. Its description (title) is: e2e/probe ` + jobID + `
Its prompt is exactly these two lines:
lease: ` + jobID + `
Reply with the single word OK and use no tools.
3. Wait for the subagent to finish. If the spawn is refused, do not retry it.
Finally reply with one line listing every code word (shaped like E2E-WORD-XXXXXXXX) that any refusal, error or hook note showed you.`},
	convBrief: {briefQuestion},
}

const briefQuestion = `Use no tools. Your context may hold a magus brief that lists the leases live in this checkout. Reply with the code word shaped like E2E-BRIEF-XXXXXXXX from that list, or NONE if you have no such brief.`

// briefTurns brings a Claude Code session to a compaction, which is where its harness
// hands the brief over, before asking for it.
var briefTurns = map[string][]string{
	"claude-code": {"Reply with the single word ready.", "/compact", briefQuestion},
}

type scenario struct {
	name  string
	conv  conversationName
	check func(t *testing.T, h *hostDriver, s *session)
}

// scenarios assert what Claude Code gets today; parity is the bar for every host.
var scenarios = []scenario{
	{name: "shell-deny", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool {
			return strings.Contains(r.Request.Command, "e2e-deny-probe") && r.Response.Decision == "deny"
		}) {
			t.Error("no deny recorded for the probe command")
		}
		if !s.replied(s.w.code("DENY")) {
			t.Errorf("the deny reason never reached the model: reply lacks %s", s.w.code("DENY"))
		}
	}},
	{name: "judged-once-per-call", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		// Two hooks wired to one call judge it within one hook round trip of each other;
		// a model issuing the same call again waits on a model turn first.
		const window = 1500
		last := map[string]int64{}
		var twice []string
		for _, r := range s.w.records(t) {
			if r.Response.Decision == "" {
				continue
			}
			if at, ok := last[r.subject()]; ok && r.Ts-at < window {
				twice = append(twice, r.Request.Tool+" "+clip(r.Request.Command+filepath.Base(r.Request.Path)))
			}
			last[r.subject()] = r.Ts
		}
		if len(twice) > 0 {
			s.w.dumpRecords(t)
			t.Errorf("one call judged twice, within %dms: %v", window, twice)
		}
	}},
	{name: "shell-advise", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool {
			return strings.Contains(r.Request.Command, "e2e-advise-probe") && r.Response.Decision == "advise"
		}) {
			t.Error("no advise recorded for the probe command")
		}
		if !s.replied(s.w.code("ADVISE")) {
			t.Errorf("the advice never reached the model: reply lacks %s", s.w.code("ADVISE"))
		}
	}},
	{name: "read-judged", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		// The guard restates a read tool's call as `cat <path>`, so a read and a shell cat
		// record alike.
		if !hasRecord(t, s, func(r record) bool {
			return strings.Contains(r.Request.Path+r.Request.Command, "probe.txt") && r.Response.Decision != ""
		}) {
			t.Error("no judged read of probe.txt")
		}
	}},
	{name: "write-output-judged", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool {
			return strings.HasSuffix(r.Request.Path, "gen/out.txt") && r.Response.Decision == "advise"
		}) {
			t.Error("the write to the declared output gen/out.txt was not judged (want an advise)")
		}
	}},
	{name: "write-deny", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool {
			return strings.HasSuffix(r.Request.Path, "blocked.txt") && r.Response.Decision == "deny"
		}) {
			t.Error("no deny recorded for the write to blocked.txt")
		}
		if fileExists(filepath.Join(s.w.dir, "blocked.txt")) {
			t.Error("blocked.txt exists: the denied write went through")
		}
		if !s.replied(s.w.code("WRITE")) {
			t.Errorf("the write deny reason never reached the model: reply lacks %s", s.w.code("WRITE"))
		}
	}},
	{name: "mcp-judged", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool {
			call := strings.ToLower(r.Request.Tool + " " + r.Request.Command)
			return strings.Contains(call, "mcp") && strings.Contains(call, "status") && r.Response.Decision != ""
		}) {
			t.Error("no judged call of the magus MCP status tool")
		}
	}},
	{name: "prompt-recorded", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		if len(s.w.markers(t, "topic-architecture")) == 0 {
			t.Error("the prompt was not recorded: no architecture topic marked for the session")
		}
	}},
	{name: "reply-check", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		replies := s.w.proofreadReplies(t)
		if len(replies) == 0 {
			t.Fatal("proofread-reply.buzz never ran proofread at turn end")
		}
		if strings.TrimSpace(replies[len(replies)-1]) == "" {
			t.Error("proofread received an empty reply")
		}
	}},
	{name: "checkpoint", conv: convTools, check: func(t *testing.T, h *hostDriver, s *session) {
		// OpenCode records it from session.idle, which can land after `opencode run`
		// has returned, so the listing gets a short grace period.
		deadline := time.Now().Add(15 * time.Second)
		for {
			for _, c := range s.w.checkpoints(t) {
				if c.Host == h.id && c.Workspace == s.w.dir {
					return
				}
			}
			if time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Second)
		}
		t.Errorf("magus session lists no %s checkpoint for %s", h.id, s.w.dir)
	}},
	{name: "skill-load-recorded", conv: convSpawn, check: func(t *testing.T, h *hostDriver, s *session) {
		if len(s.w.markers(t, "skill-magus-multi-agent")) == 0 {
			t.Error("the magus-multi-agent skill load was not recorded")
		}
	}},
	{name: "spawn-judged", conv: convSpawn, check: func(t *testing.T, h *hostDriver, s *session) {
		if !hasRecord(t, s, func(r record) bool { return r.Kind == trail.KindAgentSpawn && r.Lease == jobID && r.DecidedBy != "" }) {
			t.Errorf("no judged agent_spawn record carrying lease %s", jobID)
		}
		if !s.replied(s.w.code("SPAWN")) {
			t.Logf("the spawn rule's advice did not reach the model: reply lacks %s", s.w.code("SPAWN"))
		}
	}},
	{name: "spawn-child-recorded", conv: convSpawn, check: func(t *testing.T, h *hostDriver, s *session) {
		if len(s.w.spawnedAgents(t)) == 0 {
			t.Error("no spawned child id recorded")
		}
	}},
	{name: "subagent-context-filed", conv: convSpawn, check: func(t *testing.T, h *hostDriver, s *session) {
		for _, a := range s.w.spawnedAgents(t) {
			if a.ContextTokens != nil && *a.ContextTokens > 0 {
				return
			}
		}
		t.Error("no subagent context size filed")
	}},
	{name: "brief-delivered", conv: convBrief, check: func(t *testing.T, h *hostDriver, s *session) {
		if !s.replied(s.w.code("BRIEF")) {
			t.Errorf("the brief never reached the model: reply lacks %s", s.w.code("BRIEF"))
		}
	}},
}

func hasRecord(t *testing.T, s *session, match func(record) bool) bool {
	t.Helper()
	if slices.ContainsFunc(s.w.records(t), match) {
		return true
	}
	s.w.dumpRecords(t)
	return false
}

// outcome is how one scenario ended.
type outcome string

const (
	outcomePass outcome = "pass"
	outcomeSkip outcome = "skip"
	outcomeFail outcome = "fail"
)

// result is one host x scenario row of the report.
type result struct {
	Host     string  `json:"host"`
	Scenario string  `json:"scenario"`
	Status   outcome `json:"status"`
	Detail   string  `json:"detail,omitempty"`
}

type hostReport struct {
	ID      string `json:"id"`
	Binary  string `json:"binary"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model"`
	Note    string `json:"note,omitempty"`
}

// TestHosts drives each installed host through every scenario. It spends model tokens:
// run it on demand with `-tags e2e -run TestHosts`, never in a normal test run.
//
// MAGUS_E2E_HOSTS limits the hosts (comma-separated ids), MAGUS_E2E_<HOST>_MODEL and
// MAGUS_E2E_<HOST>_BIN override a host's model and binary, MAGUS_E2E_TIMEOUT bounds one
// host invocation, MAGUS_E2E_MAGUS names the magus binary, MAGUS_E2E_KEEP=1 keeps the
// workspaces, and MAGUS_E2E_REPORT names the version report's path.
func TestHosts(t *testing.T) {
	only := os.Getenv("MAGUS_E2E_HOSTS")
	var (
		mu      sync.Mutex
		results []result
		hosts   []hostReport
	)
	t.Run("host", func(t *testing.T) {
		for _, h := range drivers() {
			if only != "" && !slices.Contains(strings.Split(only, ","), h.id) {
				continue
			}
			hosts = append(hosts, hostReport{ID: h.id, Binary: h.binary, Path: h.path, Version: h.version, Model: h.model, Note: h.note})
			t.Run(h.id, func(t *testing.T) {
				t.Parallel()
				if h.path == "" {
					t.Skipf("%s: %s", h.binary, h.note)
				}
				t.Logf("%s: %s (%s), %s; model %s", h.id, h.path, h.version, h.note, h.model)
				owner := t
				sessions := map[conversationName]*session{}
				failed := map[conversationName]bool{}
				for _, sc := range scenarios {
					t.Run(sc.name, func(t *testing.T) {
						detail := ""
						t.Cleanup(func() {
							mu.Lock()
							defer mu.Unlock()
							results = append(results, result{Host: h.id, Scenario: sc.name, Status: status(t), Detail: detail})
						})
						if reason, ok := h.skips[sc.name]; ok {
							detail = reason
							t.Skip(reason)
						}
						if h.unauthenticated != "" {
							detail = h.notLoggedIn()
							t.Skip(detail)
						}
						if failed[sc.conv] {
							t.Fatalf("the %s conversation failed to set up in an earlier scenario", sc.conv)
						}
						s, ok := sessions[sc.conv]
						if !ok {
							failed[sc.conv] = true
							s = h.play(t, owner, sc.conv.prompts(h.id))
							sessions[sc.conv], failed[sc.conv] = s, false
						}
						if h.unauthenticated != "" {
							detail = h.notLoggedIn()
							t.Skip(detail)
						}
						sc.check(t, h, s)
					})
				}
			})
		}
	})
	writeReport(t, hosts, results)
}

func status(t *testing.T) outcome {
	switch {
	case t.Skipped():
		return outcomeSkip
	case t.Failed():
		return outcomeFail
	}
	return outcomePass
}

func writeReport(t *testing.T, hosts []hostReport, results []result) {
	t.Helper()
	slices.SortFunc(results, func(a, b result) int {
		return strings.Compare(a.Host+"\x00"+a.Scenario, b.Host+"\x00"+b.Scenario)
	})
	magus := os.Getenv("MAGUS_E2E_MAGUS")
	if magus == "" {
		magus = repoPath(t, "magus")
	}
	magusVersion, _ := versionOf(magus)
	report := map[string]any{
		"generated_at":  time.Now().UTC().Format(time.RFC3339),
		"magus":         magus,
		"magus_version": magusVersion,
		"hosts":         hosts,
		"results":       results,
	}
	path := os.Getenv("MAGUS_E2E_REPORT")
	if path == "" {
		path = filepath.Join(os.TempDir(), "magus-host-e2e", "report.json")
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(body)+"\n")
	t.Logf("version report: %s", path)
	for _, h := range hosts {
		t.Logf("  %-12s %-40s %s (%s)", h.ID, h.Version, h.Path, h.Note)
	}
	for _, r := range results {
		t.Logf("  %-12s %-24s %s %s", r.Host, r.Scenario, r.Status, r.Detail)
	}
}
