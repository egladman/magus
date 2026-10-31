//go:build e2e

package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit/e2e"
)

type hostReport struct {
	ID      string `json:"id"`
	Binary  string `json:"binary"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model"`
	Note    string `json:"note,omitempty"`
}

// bindableSuite is a per-host suite: its base takes the driver and the
// report before suite.Run starts it.
type bindableSuite interface {
	suite.TestingSuite
	bind(h *hostDriver, report *e2e.Report)
}

// TestHosts runs each installed host's suite, in parallel across hosts. It
// spends model tokens: run it on demand, never in a normal test run.
//
// MAGUS_E2E_HOSTS limits the hosts (comma-separated ids).
// MAGUS_E2E_<HOST>_MODEL and MAGUS_E2E_<HOST>_BIN override a host's model
// and binary. MAGUS_E2E_TIMEOUT bounds one host invocation, MAGUS_E2E_MAGUS
// names the magus binary, MAGUS_E2E_KEEP=1 keeps workspaces, and
// MAGUS_E2E_REPORT names the report's path.
func TestHosts(t *testing.T) {
	only := os.Getenv("MAGUS_E2E_HOSTS")
	report := e2e.NewReport()
	var hosts []hostReport
	drive := func(t *testing.T, h *hostDriver, s bindableSuite) {
		if only != "" && !slices.Contains(strings.Split(only, ","), h.id) {
			return
		}
		hosts = append(hosts, hostReport{ID: h.id, Binary: h.binary, Path: h.install.Path, Version: h.install.Version, Model: h.model, Note: h.install.Note})
		s.bind(h, report)
		t.Run(h.id, func(t *testing.T) {
			t.Parallel()
			suite.Run(t, s)
		})
	}
	t.Run("host", func(t *testing.T) {
		drive(t, claudeDriver(), new(claudeCodeSuite))
		drive(t, codexDriver(), new(codexSuite))
		drive(t, cursorDriver(), new(cursorSuite))
		drive(t, opencodeDriver(), new(openCodeSuite))
	})
	writeReport(t, hosts, report)
}

func writeReport(t *testing.T, hosts []hostReport, report *e2e.Report) {
	t.Helper()
	magus := os.Getenv("MAGUS_E2E_MAGUS")
	if magus == "" {
		magus = checkoutPath(t, "magus")
	}
	magusVersion, err := e2e.BinaryVersion(magus, scrubbedEnv())
	if err != nil {
		t.Logf("magus version: %v", err)
	}
	report.Attach("magus", magus)
	report.Attach("magus_version", magusVersion)
	report.Attach("hosts", hosts)
	path := os.Getenv("MAGUS_E2E_REPORT")
	if path == "" {
		path = filepath.Join(os.TempDir(), "magus-host-e2e", "report.json")
	}
	if err := report.Write(path); err != nil {
		t.Fatal(err)
	}
	t.Logf("version report: %s", path)
	for _, h := range hosts {
		t.Logf("  %-12s %-40s %s (%s)", h.ID, h.Version, h.Path, h.Note)
	}
	for _, c := range report.Cases() {
		t.Logf("  %-12s %-24s %s %s", c.Subject, c.Name, c.Status, c.Detail)
	}
}

// hostSuite holds the scenarios, which assert what Claude Code gets today;
// parity is the bar for every host. A host that cannot drive one declares
// a method of the same name that calls skipScenario.
type hostSuite struct {
	e2e.Suite
	host     *hostDriver
	report   *e2e.Report
	sessions map[conversationName]*session
	failed   map[conversationName]bool
}

func (s *hostSuite) bind(h *hostDriver, report *e2e.Report) {
	s.host, s.report = h, report
}

func (s *hostSuite) SetupSuite() {
	s.Suite.SetupSuite()
	s.RequireBinary(s.host.install)
	s.sessions = map[conversationName]*session{}
	s.failed = map[conversationName]bool{}
	i := s.host.install
	s.T().Logf("%s: %s (%s), %s; model %s", s.host.id, i.Path, i.Version, i.Note, s.host.model)
}

// scenario files a report row for the running method and returns the
// conversation's session, playing it on first use.
func (s *hostSuite) scenario(name string, conv conversationName) *session {
	t := s.T()
	t.Helper()
	row := s.report.Track(t, s.host.id, name)
	s.skipIfLoggedOut(row)
	if s.failed[conv] {
		t.Fatalf("the %s conversation failed to set up in an earlier scenario", conv)
	}
	played, ok := s.sessions[conv]
	if !ok {
		s.failed[conv] = true
		played = s.host.play(t, s.Owner(), conv.prompts(s.host))
		s.sessions[conv], s.failed[conv] = played, false
	}
	s.skipIfLoggedOut(row)
	return played
}

func (s *hostSuite) skipIfLoggedOut(row *e2e.Case) {
	if s.host.unauthenticated != "" {
		row.Detail = s.host.notLoggedIn()
		s.T().Skip(row.Detail)
	}
}

// skipScenario files the report row of a scenario this host cannot drive.
func (s *hostSuite) skipScenario(name, reason string) {
	row := s.report.Track(s.T(), s.host.id, name)
	row.Detail = reason
	s.T().Skip(reason)
}

func (s *hostSuite) TestShellDeny() {
	played := s.scenario("shell-deny", convTools)
	s.True(hasRecord(s.T(), played, func(r record) bool {
		return strings.Contains(r.Request.Command, "e2e-deny-probe") && r.Response.Decision == "deny"
	}), "no deny recorded for the probe command")
	s.Truef(played.replied(played.w.code("DENY")), "the deny reason never reached the model: reply lacks %s", played.w.code("DENY"))
}

func (s *hostSuite) TestJudgedOncePerCall() {
	played := s.scenario("judged-once-per-call", convTools)
	// Two hooks wired to one call judge it within one hook round trip.
	// A model issuing the same call again waits on a model turn first.
	const window = 1500
	last := map[string]int64{}
	var twice []string
	for _, r := range played.w.records(s.T()) {
		if r.Response.Decision == "" {
			continue
		}
		if at, ok := last[r.subject()]; ok && r.Ts-at < window {
			twice = append(twice, fmt.Sprintf("%s %s", r.Request.Tool, clip(r.Request.Command+filepath.Base(r.Request.Path))))
		}
		last[r.subject()] = r.Ts
	}
	if len(twice) > 0 {
		played.w.dumpRecords(s.T())
	}
	s.Emptyf(twice, "one call judged twice, within %dms", window)
}

func (s *hostSuite) TestShellAdvise() {
	played := s.scenario("shell-advise", convTools)
	s.True(hasRecord(s.T(), played, func(r record) bool {
		return strings.Contains(r.Request.Command, "e2e-advise-probe") && r.Response.Decision == "advise"
	}), "no advise recorded for the probe command")
	s.Truef(played.replied(played.w.code("ADVISE")), "the advice never reached the model: reply lacks %s", played.w.code("ADVISE"))
}

func (s *hostSuite) TestReadJudged() {
	played := s.scenario("read-judged", convTools)
	// The guard restates a read tool's call as `cat <path>`, so a read and
	// a shell cat record alike.
	s.True(hasRecord(s.T(), played, func(r record) bool {
		return strings.Contains(r.Request.Path+r.Request.Command, "probe.txt") && r.Response.Decision != ""
	}), "no judged read of probe.txt")
}

func (s *hostSuite) TestWriteOutputJudged() {
	played := s.scenario("write-output-judged", convTools)
	s.True(hasRecord(s.T(), played, func(r record) bool {
		return strings.HasSuffix(r.Request.Path, "gen/out.txt") && r.Response.Decision == "advise"
	}), "the write to the declared output gen/out.txt was not judged (want an advise)")
}

func (s *hostSuite) TestWriteDeny() {
	played := s.scenario("write-deny", convTools)
	s.True(hasRecord(s.T(), played, func(r record) bool {
		return strings.HasSuffix(r.Request.Path, "blocked.txt") && r.Response.Decision == "deny"
	}), "no deny recorded for the write to blocked.txt")
	s.False(fileExists(filepath.Join(played.w.dir, "blocked.txt")), "blocked.txt exists: the denied write went through")
	s.Truef(played.replied(played.w.code("WRITE")), "the write deny reason never reached the model: reply lacks %s", played.w.code("WRITE"))
}

func (s *hostSuite) TestMCPJudged() {
	played := s.scenario("mcp-judged", convTools)
	s.True(hasRecord(s.T(), played, func(r record) bool {
		call := strings.ToLower(fmt.Sprintf("%s %s", r.Request.Tool, r.Request.Command))
		return strings.Contains(call, "mcp") && strings.Contains(call, "status") && r.Response.Decision != ""
	}), "no judged call of the magus MCP status tool")
}

func (s *hostSuite) TestPromptRecorded() {
	played := s.scenario("prompt-recorded", convTools)
	s.NotEmpty(played.w.markers(s.T(), "topic-architecture"), "the prompt was not recorded: no architecture topic marked for the session")
}

func (s *hostSuite) TestReplyCheck() {
	played := s.scenario("reply-check", convTools)
	replies := played.w.proofreadReplies(s.T())
	s.Require().NotEmpty(replies, "proofread-reply.buzz never ran proofread at turn end")
	s.NotEmpty(strings.TrimSpace(replies[len(replies)-1]), "proofread received an empty reply")
}

func (s *hostSuite) TestCheckpoint() {
	played := s.scenario("checkpoint", convTools)
	// OpenCode records it from session.idle, which can land after `opencode
	// run` has returned, so the listing gets a short grace period.
	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, c := range played.w.checkpoints(s.T()) {
			if c.Host == s.host.id && c.Workspace == played.w.dir {
				return
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	s.Failf("no checkpoint", "magus session lists no %s checkpoint for %s", s.host.id, played.w.dir)
}

func (s *hostSuite) TestSkillLoadRecorded() {
	played := s.scenario("skill-load-recorded", convSpawn)
	s.NotEmpty(played.w.markers(s.T(), "skill-magus-multi-agent"), "the magus-multi-agent skill load was not recorded")
}

func (s *hostSuite) TestSpawnJudged() {
	played := s.scenario("spawn-judged", convSpawn)
	s.Truef(hasRecord(s.T(), played, func(r record) bool {
		return r.Kind == trail.KindAgentSpawn && r.Lease == jobID && r.DecidedBy != ""
	}), "no judged agent_spawn record carrying lease %s", jobID)
	if !played.replied(played.w.code("SPAWN")) {
		s.T().Logf("the spawn rule's advice did not reach the model: reply lacks %s", played.w.code("SPAWN"))
	}
}

func (s *hostSuite) TestSpawnChildRecorded() {
	played := s.scenario("spawn-child-recorded", convSpawn)
	s.NotEmpty(played.w.spawnedAgents(s.T()), "no spawned child id recorded")
}

func (s *hostSuite) TestSubagentContextFiled() {
	played := s.scenario("subagent-context-filed", convSpawn)
	s.True(slices.ContainsFunc(played.w.spawnedAgents(s.T()), func(a spawnedAgent) bool {
		return a.ContextTokens != nil && *a.ContextTokens > 0
	}), "no subagent context size filed")
}

func (s *hostSuite) TestBriefDelivered() {
	played := s.scenario("brief-delivered", convBrief)
	s.Truef(played.replied(played.w.code("BRIEF")), "the brief never reached the model: reply lacks %s", played.w.code("BRIEF"))
}

// A subagent's stop is a pause: its parent continues it, its row stays open,
// and the call the answer prompts still runs under the job.
func (s *hostSuite) TestChildContinued() {
	played := s.scenario("child-continued", convContinue)
	s.Len(played.w.spawnedAgents(s.T()), 1, "the parent started a second child instead of continuing the first")
	s.Truef(hasRecord(s.T(), played, func(r record) bool {
		return strings.Contains(r.Request.Command, continueProbe+"-"+continueAnswer) && r.Lease == jobID
	}), "no call carrying the answer under lease %s: the child was not continued, or lost its binding", jobID)
	row := played.w.jobRow(s.T(), jobID)
	s.Truef(job.Editing(row), "row %s stopped being open: state %s, ended %q", jobID, row.State, row.EndReason)
}

// ask runs a person-only command under one of the host's modes, in a
// workspace of its own.
func (s *hostSuite) ask(c askCase) {
	t := s.T()
	t.Helper()
	w := newWorkspace(t, t, s.host)
	if c.setup != nil {
		c.setup(t, w)
	}
	run := s.host.askTurn(t, w, c)
	output := strings.Join([]string{run.Stdout, run.Stderr}, "\n")
	if m := authFailure.FindString(output); m != "" {
		t.Skipf("%s is not logged in: %s", s.host.binary, m)
	}
	ran := w.askProbeRan(t)
	shown := strings.Contains(output, askPersonText)
	t.Logf("%s/%s: %s, ran=%t prompted=%t person-text=%t reached-model=%t",
		s.host.id, c.name, s.host.install.Version, ran, run.prompted, shown, strings.Contains(output, w.code("ASK")))
	w.dumpRecords(t)
	if c.want != askObserves {
		s.Falsef(ran, "%s ran with nobody approving it", askCommand)
	}
	if c.want == askPrompts {
		s.True(run.prompted, "the host raised no permission prompt")
	}
	if c.want == askRefuses {
		s.Truef(shown, "the host's output lacks the person's text %q", askPersonText)
	}
	if c.check != nil {
		c.check(t, w)
	}
}
