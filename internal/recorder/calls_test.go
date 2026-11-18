package recorder

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func TestMain(m *testing.M) { testkit.Main(m) }

type CallsSuite struct{ suite.Suite }

func TestCalls(t *testing.T) { suite.Run(t, new(CallsSuite)) }

// captureWarnings installs a slog handler for the duration of fn and returns what it logged.
// A producer here reports a fact it could not record through slog rather than an error (every
// entry point is best-effort by contract), so the default logger is the only place to observe it.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// stamped fills what the trail decides rather than the producer: the OS account and entry point
// Append stamps (see origin_test.go), and the clock and content-addressed refs of the one event
// read back.
func stamped(t *testing.T, got, want journal.Call) journal.Call {
	t.Helper()
	want.Time = got.Time
	want.Origin = StampOrigin(t.Context(), want.Origin)
	want.Request, want.Response = got.Request, got.Response
	return want
}

func writeBlobRef(ctx context.Context, base, prefix string, data []byte) string {
	return WritePayload(ctx, base, prefix, data).Ref
}

// readBlob is the payload ref names in the log under base.
func readBlob(base, ref string) ([]byte, error) {
	return journal.Payload{Ref: ref}.Read(callsDir(base))
}

func atMs(ms int64) time.Time { return time.UnixMilli(ms) }

// seedEvents writes n calls straight into the log in one write, not n
// opens. It returns them oldest first.
func seedEvents(t *testing.T, base string, n int) []journal.Call {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(base, dir), 0o755))
	events := make([]journal.Call, 0, n)
	var buf []byte
	for i := 1; i <= n; i++ {
		e := journal.Call{Time: atMs(int64(i)), Kind: journal.CallKindMCPTool, Name: "t", Outcome: journal.OutcomeOK}
		events = append(events, e)
		line, err := e.MarshalJSON()
		require.NoError(t, err)
		buf = append(append(buf, line...), '\n')
	}
	require.NoError(t, os.WriteFile(eventsPath(base), buf, 0o644))
	return events
}

func (s *CallsSuite) TestRecentCallsIsNewestFirst() {
	t := s.T()
	dir := t.TempDir()
	AppendCall(t.Context(), dir, journal.Call{Time: atMs(1), Kind: journal.CallKindMCPTool, Name: "query", Outcome: journal.OutcomeOK})
	AppendCall(t.Context(), dir, journal.Call{Time: atMs(2), Kind: journal.CallKindTokenLifecycle, Name: "connector.create", Outcome: journal.OutcomeOK})
	AppendCall(t.Context(), dir, journal.Call{Time: atMs(3), Kind: journal.CallKindJob, Workspace: "/ws", Name: "graph build", Outcome: journal.OutcomeError, Error: "boom"})

	events, err := RecentCalls(t.Context(), dir, 10)
	if err != nil {
		t.Fatalf("RecentCalls: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	if events[0].Name != "graph build" || events[2].Name != "query" { // newest first
		t.Errorf("order = %q..%q, want graph build..query", events[0].Name, events[2].Name)
	}
	want := stamped(t, events[0], journal.Call{Time: atMs(3), Kind: journal.CallKindJob, Workspace: "/ws", Name: "graph build", Outcome: journal.OutcomeError, Error: "boom"})
	assert.Equal(t, want, events[0], "event not round-tripped")
}

// TestTrailIsPrivateToItsOwner pins the modes AppendCall and WritePayload
// create: the log records what an agent ran and what a tool returned.
func (s *CallsSuite) TestTrailIsPrivateToItsOwner() {
	t := s.T()
	dir := filepath.Join(t.TempDir(), "state")
	for i := range 3 {
		AppendCall(t.Context(), dir, journal.Call{Time: atMs(int64(i)), Kind: journal.CallKindMCPTool, Name: "query", Outcome: journal.OutcomeOK})
	}
	ref := WritePayload(t.Context(), dir, "mcp", []byte("a payload")).Ref
	require.NotEmpty(t, ref)

	perm := func(p string) os.FileMode {
		info, err := os.Stat(p)
		require.NoError(t, err)
		return info.Mode().Perm()
	}
	assert.Equal(t, os.FileMode(0o700), perm(filepath.Dir(eventsPath(dir))))
	assert.Equal(t, os.FileMode(0o700), perm(blobsPath(dir)))
	assert.Equal(t, os.FileMode(0o600), perm(eventsPath(dir)))
	assert.Equal(t, os.FileMode(0o600), perm(filepath.Join(blobsPath(dir), ref)))
}

func (s *CallsSuite) TestAppend_EmptyBaseIsNoop() {
	t := s.T()
	AppendCall(t.Context(), "", journal.Call{Name: "x"}) // must not panic or create anything
}

func (s *CallsSuite) TestAppendAgentCommand_NormalizesHookObservation() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{
		Workspace: "/repo/magus",
		Host:      "codex",
		Session:   "abc123",
		Event:     "PreToolUse",
		Tool:      "Bash",
		Command:   "magus run test .",
		Decision:  verdict.Defer,
	})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	// Host and session ride the event LINE as well as the request blob, so a reader can group a
	// page of observations by host without a blob fetch per row. An observation naming no entry
	// point came through the hook.
	require.Equal(t, stamped(t, event, journal.Call{
		Kind:      journal.CallKindAgentCommand,
		Origin:    job.Origin{EntryPoint: job.EntryPointHook, Host: "codex", Session: "abc123"},
		Workspace: "/repo/magus",
		Name:      "Bash",
		Outcome:   journal.OutcomeOK,
		Preview:   "guard: defer",
	}), event)

	request, err := readBlob(dir, event.Request.Ref)
	require.NoError(t, err)
	var gotRequest AgentCommandRequest
	require.NoError(t, codec.Unmarshal(request, &gotRequest))
	require.Equal(t, AgentCommandRequest{
		APIVersion: agentCommandAPIVersion,
		Host:       "codex",
		Session:    "abc123",
		Event:      "PreToolUse",
		Tool:       "Bash",
		Command:    "magus run test .",
	}, gotRequest)

	response, err := readBlob(dir, event.Response.Ref)
	require.NoError(t, err)
	var gotResponse AgentCommandResponse
	require.NoError(t, codec.Unmarshal(response, &gotResponse))
	require.Equal(t, AgentCommandResponse{APIVersion: agentCommandAPIVersion, Decision: verdict.Defer}, gotResponse)
}

func (s *CallsSuite) TestAppendAgentCommand_RequiresCommandOrPath() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Decision: verdict.Defer})
	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Empty(t, events)
}

func (s *CallsSuite) TestAppendAgentCommand_RecordsHowLongTheGuardJudged() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "ls", Decision: verdict.Defer, GuardMs: 278})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.NotNil(t, events[0].Guard)
	assert.Equal(t, 278*time.Millisecond, events[0].Guard.Duration)
	assert.Zero(t, events[0].Duration, "guard time is not tool time")
}

func (s *CallsSuite) TestAppendAgentCommand_RecordsHowLongTheToolRan() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Event: "PostToolUse", ToolMs: 1500})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	event.Time, event.Origin, event.Workspace = time.Time{}, job.Origin{}, ""
	event.Request, event.Response = journal.Payload{}, journal.Payload{}
	assert.Equal(t, journal.Call{Kind: journal.CallKindAgentCommand, Name: "Bash", Outcome: journal.OutcomeOK, Duration: 1500 * time.Millisecond, Preview: "observed"}, event)
}

// A hook row names the magus that judged the call and the directory the host reported, and
// leaves both off when the producer did not know them.
func (s *CallsSuite) TestAppendAgentCommand_RecordsTheJudgingBinaryAndDirectory() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{
		Tool: "Bash", Command: "ls", Decision: verdict.Defer,
		Binary: "/work/tree/magus", BinaryVersion: "v0.5.0-rc.3-62-gcb43888d2", Cwd: "/work/tree/console",
		Phases: []journal.Phase{{Name: "load_rules", ElapsedMs: 812, BudgetMs: 6500}, {Name: "approved_rule", ElapsedMs: 2100, BudgetMs: 2100, Lapsed: true}},
	})
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "pwd", Decision: verdict.Defer})
	AppendAgentSpawn(t.Context(), dir, AgentSpawn{
		Child: "Explore", Context: "look",
		Binary: "/work/tree/magus", BinaryVersion: "v0.5.0", Cwd: "/work/tree",
	})

	events, err := RecentCalls(t.Context(), dir, 10)
	require.NoError(t, err)
	require.Len(t, events, 3)
	var commands, spawns []journal.Call
	for _, e := range events {
		switch e.Kind {
		case journal.CallKindAgentCommand:
			commands = append(commands, e)
		case journal.CallKindAgentSpawn:
			spawns = append(spawns, e)
		}
	}
	require.Len(t, commands, 2)
	require.Len(t, spawns, 1)
	judged, unknown := commands[0], commands[1]
	if judged.Guard == nil {
		judged, unknown = unknown, judged
	}
	require.Equal(t, stamped(t, judged, journal.Call{
		Kind:    journal.CallKindAgentCommand,
		Origin:  job.Origin{EntryPoint: job.EntryPointHook},
		Name:    "Bash",
		Outcome: journal.OutcomeOK,
		Preview: "guard: defer",
		Guard: &journal.Guard{
			Binary:        "/work/tree/magus",
			BinaryVersion: "v0.5.0-rc.3-62-gcb43888d2",
			Cwd:           "/work/tree/console",
			Phases:        []journal.Phase{{Name: "load_rules", ElapsedMs: 812, BudgetMs: 6500}, {Name: "approved_rule", ElapsedMs: 2100, BudgetMs: 2100, Lapsed: true}},
		},
	}), judged)
	require.Nil(t, unknown.Guard)
	require.Equal(t, stamped(t, spawns[0], journal.Call{
		Kind:    journal.CallKindAgentSpawn,
		Origin:  job.Origin{EntryPoint: job.EntryPointHook},
		Name:    "Explore",
		Outcome: journal.OutcomeOK,
		Guard: &journal.Guard{
			Binary:        "/work/tree/magus",
			BinaryVersion: "v0.5.0",
			Cwd:           "/work/tree",
		},
	}), spawns[0])
}

func (s *CallsSuite) TestAppendAgentCommand_PathUsesFallbackEntryPointAndAction() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Path: "AGENTS.md", Decision: verdict.Advise, Context: "record the decision"})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]
	// No host session means unattributed, never a default party.
	require.Equal(t, stamped(t, event, journal.Call{
		Kind:    journal.CallKindAgentCommand,
		Origin:  job.Origin{EntryPoint: job.EntryPointHook},
		Name:    "command",
		Outcome: journal.OutcomeOK,
		Preview: "guard: advise",
	}), event)

	request, err := readBlob(dir, event.Request.Ref)
	require.NoError(t, err)
	var gotRequest AgentCommandRequest
	require.NoError(t, codec.Unmarshal(request, &gotRequest))
	require.Equal(t, AgentCommandRequest{APIVersion: agentCommandAPIVersion, Path: "AGENTS.md"}, gotRequest)

	response, err := readBlob(dir, event.Response.Ref)
	require.NoError(t, err)
	var gotResponse AgentCommandResponse
	require.NoError(t, codec.Unmarshal(response, &gotResponse))
	require.Equal(t, AgentCommandResponse{
		APIVersion: agentCommandAPIVersion,
		Decision:   verdict.Advise,
		Context:    "record the decision",
	}, gotResponse)
}

func (s *CallsSuite) TestAppendAgentCommand_RecordsTheCostFacts() {
	t := s.T()
	dir := t.TempDir()
	chars := 9100
	want := types.AgentFacts{
		Thread:         types.AgentThreadMain,
		ThreadFrom:     types.ThreadReported,
		ContextTokens:  412_000,
		ContextModel:   "claude-opus-5-5",
		Billing:        types.AgentBillingMetered,
		DelegableClass: types.DelegableSearch,
		ResultChars:    &chars,
		Shadow:         map[types.GuardProfile]verdict.Decision{types.GuardProfileStrict: verdict.Deny, types.GuardProfileLight: verdict.Defer},
	}
	AppendAgentCommand(t.Context(), dir, AgentCommand{Command: "grep x", Decision: verdict.Defer, AgentFacts: want})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	raw, err := readBlob(dir, events[0].Request.Ref)
	require.NoError(t, err)
	var got AgentCommandRequest
	require.NoError(t, codec.Unmarshal(raw, &got))
	assert.Equal(t, want, got.AgentFacts)
	for _, key := range []string{"thread", "thread_from", "context_tokens", "context_model", "billing", "delegable_class", "result_chars", "shadow"} {
		assert.Contains(t, string(raw), fmt.Sprintf(`"%s"`, key), "the facts sit inline in the request blob")
	}
}

func (s *CallsSuite) TestAppendAgentCommand_OmitsCostFactsItWasNotGiven() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Command: "ls"})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	raw, err := readBlob(dir, events[0].Request.Ref)
	require.NoError(t, err)
	for _, key := range []string{"thread", "thread_from", "context_tokens", "context_model", "billing", "delegable_class", "result_chars", "shadow"} {
		assert.NotContains(t, string(raw), fmt.Sprintf(`"%s"`, key))
	}
}

// A result of zero characters was observed, which is not the same as no observation.
func (s *CallsSuite) TestAppendAgentCommand_KeepsAnObservedEmptyResult() {
	t := s.T()
	dir := t.TempDir()
	empty := 0
	AppendAgentCommand(t.Context(), dir, AgentCommand{Command: "ls", AgentFacts: types.AgentFacts{ResultChars: &empty}})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	raw, err := readBlob(dir, events[0].Request.Ref)
	require.NoError(t, err)
	var got AgentCommandRequest
	require.NoError(t, codec.Unmarshal(raw, &got))
	require.NotNil(t, got.ResultChars)
	assert.Zero(t, *got.ResultChars)
}

func (s *CallsSuite) TestAppendAgentSpawn_RecordsTheCostFacts() {
	t := s.T()
	dir := t.TempDir()
	want := types.AgentFacts{
		Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
		ContextTokens: 88_000, ContextModel: "claude-opus-5-5", Billing: types.AgentBillingQuota,
	}
	AppendAgentSpawn(t.Context(), dir, AgentSpawn{
		Child: "Explore", Context: "look around", DeclaredModel: "claude-haiku-5-5", AgentFacts: want,
	})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	raw, err := readBlob(dir, events[0].Request.Ref)
	require.NoError(t, err)
	var got AgentSpawnRequest
	require.NoError(t, codec.Unmarshal(raw, &got))
	wantRequest := got
	wantRequest.AgentFacts, wantRequest.DeclaredModel = want, "claude-haiku-5-5"
	assert.Equal(t, wantRequest, got)
}

// TestAppendAgentSpawn_RecordsHandedContext is the lease-audit round trip: the event line
// says who handed work to whom and which lease it belongs to, and the context itself is reachable
// only through the blob, never inlined, because a lease prompt is routinely kilobytes.
func (s *CallsSuite) TestAppendAgentSpawn_RecordsHandedContext() {
	t := s.T()
	dir := t.TempDir()
	handed := "lease: notes-store-6b\n\nAudit the notes store write boundary and report back."
	AppendAgentSpawn(t.Context(), dir, AgentSpawn{
		Workspace: "/repo/magus",
		Host:      "claude-code",
		Session:   "abc123",
		Event:     "PreToolUse",
		Tool:      "Task",
		Child:     "Explore",
		Context:   handed,
	})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	event := events[0]

	// Whole-struct assertion with the content-addressed and clock-dependent fields lifted out
	// first, so a new field on Event cannot be silently dropped by this producer.
	requestRef := event.Request.Ref
	event.Time, event.Request = time.Time{}, journal.Payload{}
	require.Equal(t, journal.Call{
		Kind:      journal.CallKindAgentSpawn,
		Origin:    StampOrigin(t.Context(), job.Origin{EntryPoint: job.EntryPointHook, Host: "claude-code", Session: "abc123"}),
		Workspace: "/repo/magus",
		Name:      "Explore",
		Lease:     "notes-store-6b",
		Outcome:   journal.OutcomeOK,
	}, event) // Preview included: the handed context belongs in the blob, never on the line.

	request, err := readBlob(dir, requestRef)
	require.NoError(t, err)
	var gotRequest AgentSpawnRequest
	require.NoError(t, codec.Unmarshal(request, &gotRequest))
	require.Equal(t, AgentSpawnRequest{
		APIVersion: agentSpawnAPIVersion,
		Host:       "claude-code",
		Session:    "abc123",
		Event:      "PreToolUse",
		Tool:       "Task",
		Child:      "Explore",
		Lease:      "notes-store-6b",
		Context:    handed,
	}, gotRequest)
}

func (s *CallsSuite) TestAppendAgentSpawn_RequiresContextAndFallsBackToAGenericAction() {
	t := s.T()
	dir := t.TempDir()
	AppendAgentSpawn(t.Context(), dir, AgentSpawn{Host: "claude-code", Child: "Explore"})
	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Empty(t, events, "a spawn with no handed context has nothing to audit")

	AppendAgentSpawn(t.Context(), dir, AgentSpawn{Context: "go and do the thing"})
	events, err = RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	// An unlabelled callee still says a spawn happened, and names no lease.
	require.Equal(t, stamped(t, events[0], journal.Call{
		Kind:    journal.CallKindAgentSpawn,
		Origin:  job.Origin{EntryPoint: job.EntryPointHook},
		Name:    "agent.spawn",
		Outcome: journal.OutcomeOK,
	}), events[0])
}

// spawnName is the action AppendAgentSpawn records for spawn.
func (s *CallsSuite) spawnName(spawn AgentSpawn) string {
	dir := s.T().TempDir()
	AppendAgentSpawn(s.T().Context(), dir, spawn)
	events, err := RecentCalls(s.T().Context(), dir, 1)
	s.Require().NoError(err)
	s.Require().Len(events, 1)
	return events[0].Name
}

// A continuation is recorded because the caller says so, not because it named a target: a
// host may address an agent it cannot name, and a spawn may carry one.
func (s *CallsSuite) TestAppendAgentSpawn_AContinueNamingNoTargetIsAContinue() {
	s.Equal(NameAgentContinue, s.spawnName(AgentSpawn{Continue: true, Context: "carry on"}))
}

func (s *CallsSuite) TestAppendAgentSpawn_ASpawnCarryingATargetIsASpawn() {
	s.Equal("Explore", s.spawnName(AgentSpawn{Child: "Explore", Target: "a1b2c3", Context: "audit"}))
}

func (s *CallsSuite) TestLeaseFromContextReadsAMarkerOnTheFirstLine() {
	s.Equal("mgs1021", leaseFromContext("lease: mgs1021\nthen the work"))
	s.Equal("feat/spawn-capture", leaseFromContext("  \tlease: feat/spawn-capture  \nrest"), "indented")
	s.Equal("a.b-1/c", leaseFromContext("\n  \n\nlease: a.b-1/c\n"), "after leading blanks")
	s.Equal("one", leaseFromContext("lease: one\nlease: two"), "the first line wins")
}

// A malformed marker yields no lease, never a wrong one.
func (s *CallsSuite) TestLeaseFromContextRefusesAMalformedMarker() {
	s.Empty(leaseFromContext("just a prompt with no marker"))
	s.Empty(leaseFromContext("lease:\nrest"), "empty id")
	s.Empty(leaseFromContext("lease:   \nrest"), "only whitespace id")
	s.Empty(leaseFromContext("lease: mgs1021 the notes one"), "prose after the id")
	s.Empty(leaseFromContext("see lease: mgs1021 in the ledger"), "not at line start")
	s.Empty(leaseFromContext("leases: mgs1021"), "wrong key")
	s.Empty(leaseFromContext("lease: mgs1021!"), "illegal characters")
}

// The marker has to LEAD: a well-formed one quoted below the opening line,
// or pushed past the head by a pathological first line, is not this spawn's.
func (s *CallsSuite) TestLeaseFromContextRefusesAMarkerThatDoesNotLead() {
	s.Empty(leaseFromContext("do this\nlease: a.b-1/c\n"))
	s.Empty(leaseFromContext(strings.Repeat(" ", leaseScanBytes) + "lease: mgs1021"))
}

func (s *CallsSuite) TestLeaseFromEnv_ReadsAValidID() {
	t := s.T()
	t.Setenv(EnvBaggage, BaggageLease+"=  feat/spawn-capture  ")
	require.Equal(t, "feat/spawn-capture", LeaseFromEnv(), "surrounding whitespace is formatting, not part of the id")

	t.Setenv(EnvBaggage, "")
	require.Empty(t, LeaseFromEnv(), "an unset channel is a session that claims no lease, not an error")
}

// The note is what keeps a typo'd lease from looking like a fleet that simply never
// attributed anything: the value is dropped either way, and only the note says why.
func (s *CallsSuite) TestLeaseFromEnv_DropsAnInvalidIDWithANote() {
	t := s.T()
	t.Setenv(EnvBaggage, BaggageLease+"=not a lease id")
	baggageLeaseNoteOnce = sync.Once{} // another test in this binary may already have spent it
	t.Cleanup(func() { baggageLeaseNoteOnce = sync.Once{} })

	logged := captureWarnings(t, func() {
		require.Empty(t, LeaseFromEnv())
		require.Empty(t, LeaseFromEnv())
	})

	assert.Equal(t, 1, strings.Count(logged, EnvBaggage), "the environment cannot change under a running worker, so the note cannot repeat")
	assert.Contains(t, logged, "letters, digits", "the note names the rule the value failed")
	assert.NotContains(t, logged, "not a lease id", "the value failed the charset that makes a lease safe to log unredacted")
}

// The producer resolved the lease through job.LeaseQuery, so the trail records what it was
// handed and never consults BAGGAGE itself: a second reading here would be a second order.
func (s *CallsSuite) TestAppendAgentCommand_RecordsOnlyTheSuppliedLease() {
	t := s.T()
	t.Setenv(EnvBaggage, BaggageLease+"=fleet/from-env")
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "ls", Lease: "fleet/supplied", LeaseFrom: job.LeaseSourceMarker})
	// A supplied lease that fails the charset is not stamped, and nothing stands in for it.
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "ls -l", Lease: "not a lease id", LeaseFrom: job.LeaseSourceFlag})
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "magus run test .", Decision: verdict.Defer})

	events, err := RecentCalls(t.Context(), dir, 3)
	require.NoError(t, err)
	require.Len(t, events, 3)
	assert.Empty(t, events[0].Lease, "newest first: an unresolved call is uncorrelated, never the environment's")
	assert.Empty(t, events[1].Lease)
	assert.Empty(t, events[1].LeaseFrom, "a dropped lease drops its source")
	assert.Equal(t, stamped(t, events[2], journal.Call{
		Kind:      journal.CallKindAgentCommand,
		Origin:    job.Origin{EntryPoint: job.EntryPointHook},
		Name:      "Bash",
		Lease:     "fleet/supplied",
		LeaseFrom: job.LeaseSourceMarker,
		Outcome:   journal.OutcomeOK,
		Preview:   "observed",
	}), events[2])
}

func (s *CallsSuite) TestAppendAgentCommand_NoLeaseAnywhereStaysUncorrelated() {
	t := s.T()
	t.Setenv(EnvBaggage, "")
	dir := t.TempDir()
	AppendAgentCommand(t.Context(), dir, AgentCommand{Tool: "Bash", Command: "ls"})

	events, err := RecentCalls(t.Context(), dir, 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Empty(t, events[0].Lease, "a missing join is the designed outcome, never an invented one")
}

func (s *CallsSuite) TestWritePayload_RoundTripAndDedup() {
	t := s.T()
	dir := t.TempDir()
	p := WritePayload(t.Context(), dir, "mcp", []byte("payload one"))
	ref, size := p.Ref, p.Bytes
	if ref == "" || size != int64(len("payload one")) || ref[:3] != "mcp" {
		t.Fatalf("WritePayload = %q,%d", ref, size)
	}
	body, err := readBlob(dir, ref)
	if err != nil || string(body) != "payload one" {
		t.Fatalf("readBlob = %q, %v", body, err)
	}
	if again := WritePayload(t.Context(), dir, "mcp", []byte("payload one")).Ref; again != ref { // content-addressed dedup
		t.Errorf("dedup failed: %q != %q", again, ref)
	}
}

func (s *CallsSuite) TestWritePayload_RejectsBadPrefixOrEmpty() {
	t := s.T()
	dir := t.TempDir()
	for _, bad := range []string{"", "m", "MCP", "toolong99", "a1"} {
		if ref := WritePayload(t.Context(), dir, bad, []byte("x")).Ref; ref != "" {
			t.Errorf("WritePayload(prefix=%q) = %q, want empty", bad, ref)
		}
	}
	if p := WritePayload(t.Context(), dir, "mcp", nil); p != (journal.Payload{}) {
		t.Errorf("empty data = %+v; want the zero payload", p)
	}
	if ref := WritePayload(t.Context(), "", "mcp", []byte("x")).Ref; ref != "" {
		t.Errorf("empty base = %q, want empty", ref)
	}
}

// backdateBlob rewinds a stored blob's mtime by age, so a test can put it past
// blobGraceWindow deterministically instead of sleeping.
func backdateBlob(t *testing.T, base, ref string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filepath.Join(blobsPath(base), ref), old, old))
}

func (s *CallsSuite) TestRotate_CapsEventsAndGCsOrphanBlobs() {
	t := s.T()
	dir := t.TempDir()
	refs := make([]string, 0, 5)
	for i := 1; i <= 5; i++ {
		response := WritePayload(t.Context(), dir, "mcp", []byte(string(rune('a'+i-1))+"-body"))
		ref := response.Ref
		refs = append(refs, ref)
		AppendCall(t.Context(), dir, journal.Call{Time: atMs(int64(i)), Kind: journal.CallKindMCPTool, Name: string(rune('a' + i - 1)), Response: response})
		// Past blobGraceWindow, so this test exercises real orphan collection rather
		// than the grace-window protection covered by TestGCBlobs_ProtectsPendingBlob.
		backdateBlob(t, dir, ref, blobGraceWindow+time.Second)
	}

	require.NoError(t, rotate(t.Context(), dir, 2)) // keep the last 2 events

	events, _ := RecentCalls(t.Context(), dir, 10)
	if len(events) != 2 {
		t.Fatalf("after rotate got %d events, want 2", len(events))
	}
	if _, err := readBlob(dir, refs[4]); err != nil { // newest kept event's blob survives
		t.Errorf("kept event's blob was GC'd: %v", err)
	}
	if _, err := readBlob(dir, refs[0]); err == nil { // oldest, now orphaned, is GC'd
		t.Errorf("orphaned blob not garbage-collected")
	}

	// An in-flight WritePayload's temp file fails ValidPayloadRef and survives.
	tmp := filepath.Join(blobsPath(dir), "mcp0123456789abcd.tmp999")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	require.NoError(t, gcBlobs(dir, nil)) // nothing referenced: valid-ref blobs go, the temp file stays
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("gcBlobs deleted a non-ref temp file: %v", err)
	}
}

// TestGCBlobs_ProtectsPendingBlob reproduces the window between WritePayload
// and the AppendCall naming its blob: a rotate there must not collect it.
func (s *CallsSuite) TestGCBlobs_ProtectsPendingBlob() {
	t := s.T()
	dir := t.TempDir()
	ref := writeBlobRef(t.Context(), dir, "mcp", []byte("pending payload"))

	require.NoError(t, gcBlobs(dir, nil)) // simulate rotate's events snapshot: taken before Append landed

	if _, err := readBlob(dir, ref); err != nil {
		t.Fatalf("gcBlobs collected a blob still within its grace window: %v", err)
	}
}

// TestWritePayload_DedupHitReopensTheGraceWindow: a second write of a stored
// payload is a fresh reference, so it must reopen the grace window.
func (s *CallsSuite) TestWritePayload_DedupHitReopensTheGraceWindow() {
	t := s.T()
	dir := t.TempDir()
	ref := writeBlobRef(t.Context(), dir, "mcp", []byte("shared payload"))
	backdateBlob(t, dir, ref, blobGraceWindow+time.Second)

	again := writeBlobRef(t.Context(), dir, "mcp", []byte("shared payload"))
	require.Equal(t, ref, again, "identical content must dedup onto the stored blob")

	require.NoError(t, gcBlobs(dir, nil)) // rotate's snapshot, taken before the pending Append lands

	_, err := readBlob(dir, ref)
	require.NoError(t, err, "a deduped blob must survive the rotate its fresh reference races")
}

// TestGCBlobs_CollectsOrphanPastGraceWindow pins the other half of the T-1 fix: the grace
// window only delays collection, it must not disable it, or gcBlobs stops doing its job.
func (s *CallsSuite) TestGCBlobs_CollectsOrphanPastGraceWindow() {
	t := s.T()
	dir := t.TempDir()
	ref := writeBlobRef(t.Context(), dir, "mcp", []byte("truly orphaned"))
	backdateBlob(t, dir, ref, blobGraceWindow+time.Second)

	require.NoError(t, gcBlobs(dir, nil))

	if _, err := readBlob(dir, ref); err == nil {
		t.Fatalf("gcBlobs kept an orphaned blob past its grace window")
	}
}

// TestGCBlobs_KeepsTheVerdictARepeatDenyCites pins the third ref an event can name: a
// repeated guard deny prints a grd ref, and collecting it would leave that line pointing at
// nothing while the event that printed it is still kept.
func (s *CallsSuite) TestGCBlobs_KeepsTheVerdictARepeatDenyCites() {
	t := s.T()
	dir := t.TempDir()
	ref := writeBlobRef(t.Context(), dir, "grd", []byte("the full verdict"))
	backdateBlob(t, dir, ref, blobGraceWindow+time.Second)
	line, err := journal.Call{Kind: journal.CallKindAgentCommand, Name: "shell.command", Guard: &journal.Guard{Verdict: journal.Payload{Ref: ref}}}.MarshalJSON()
	require.NoError(t, err)

	require.NoError(t, gcBlobs(dir, []string{string(line)}))

	got, err := readBlob(dir, ref)
	require.NoError(t, err)
	assert.Equal(t, "the full verdict", string(got))
	assert.True(t, journal.ValidPayloadRef(ref))
}

func (s *CallsSuite) TestRotate_UnderCapAndEmptyBaseAreNoops() {
	t := s.T()
	dir := t.TempDir()
	AppendCall(t.Context(), dir, journal.Call{Time: atMs(1), Name: "a"})
	require.NoError(t, rotate(t.Context(), dir, 10)) // under cap: no rewrite
	if evs, _ := RecentCalls(t.Context(), dir, 10); len(evs) != 1 {
		t.Errorf("under-cap rotate changed the trail: %d events", len(evs))
	}
	require.NoError(t, rotate(t.Context(), "", 2))
}

func (s *CallsSuite) TestRotateCalls_UnderCapKeepsAll() {
	t := s.T()
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		AppendCall(t.Context(), dir, journal.Call{Time: atMs(int64(i)), Kind: journal.CallKindJob, Name: "x"})
	}
	require.NoError(t, RotateCalls(t.Context(), dir)) // maxEvents is 10000, so this is a no-op that must not lose events
	if evs, _ := RecentCalls(t.Context(), dir, 10); len(evs) != 3 {
		t.Errorf("RotateCalls under cap changed the log: got %d events, want 3", len(evs))
	}
}

// TestRotateCalls_TrimsAnOverCapTrail: a rotate trims to the right window.
func (s *CallsSuite) TestRotateCalls_TrimsAnOverCapTrail() {
	t := s.T()
	dir := t.TempDir()
	seedEvents(t, dir, maxEvents+5) // over the cap so a rotate is observable

	require.NoError(t, RotateCalls(t.Context(), dir))
	got, err := RecentCalls(t.Context(), dir, maxEvents+100)
	require.NoError(t, err)
	require.Len(t, got, maxEvents)
	// Whole-struct assertions on the window edges, newest first.
	require.Equal(t, journal.Call{Time: atMs(int64(maxEvents + 5)), Kind: journal.CallKindMCPTool, Name: "t", Outcome: journal.OutcomeOK}, got[0])
	require.Equal(t, journal.Call{Time: atMs(6), Kind: journal.CallKindMCPTool, Name: "t", Outcome: journal.OutcomeOK}, got[len(got)-1])
}

// A blob is a whole tool payload, so it stays private to its owner.
func (s *CallsSuite) TestWritePayload_IsOwnerOnly() {
	t := s.T()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	dir := t.TempDir()
	ref := writeBlobRef(t.Context(), dir, "mcp", []byte("payload"))
	require.NotEmpty(t, ref)

	fi, err := os.Stat(filepath.Join(blobsPath(dir), ref))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// A rotate writes the trail with the mode Append creates it with, so a trail
// an older magus left world-readable becomes private.
func (s *CallsSuite) TestRotateCalls_WritesTheTrailPrivate() {
	t := s.T()
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	dir := t.TempDir()
	seedEvents(t, dir, maxEvents+5)
	require.NoError(t, os.Chmod(eventsPath(dir), 0o644))

	require.NoError(t, RotateCalls(t.Context(), dir))
	got, err := RecentCalls(t.Context(), dir, maxEvents+100)
	require.NoError(t, err)
	require.Len(t, got, maxEvents, "the rotate ran")
	fi, err := os.Stat(eventsPath(dir))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// TestRotateCalls_SkipsTheReadWhenTheFileIsTooSmall pins the stat fast path,
// and that its bound stays sound: the smallest serializable line is at least
// minEventBytes, so no log over the cap is ever skipped.
func (s *CallsSuite) TestRotateCalls_SkipsTheReadWhenTheFileIsTooSmall() {
	t := s.T()
	dir := t.TempDir()
	AppendCall(t.Context(), dir, journal.Call{Time: atMs(1), Name: "a"})

	info, err := os.Stat(eventsPath(dir))
	require.NoError(t, err)
	require.Less(t, info.Size(), int64(maxEvents)*minEventBytes, "a one-event trail is below the skip threshold")

	require.NoError(t, RotateCalls(t.Context(), dir))
	got, err := RecentCalls(t.Context(), dir, 10)
	require.NoError(t, err)
	require.Len(t, got, 1, "the fast path must leave a small trail exactly as it found it")

	// The floor is not a guess: the smallest event that can reach the file (every
	// no-omitempty field empty) must still be at least minEventBytes, or the skip could
	// fire on a trail that genuinely needed trimming.
	line, err := journal.Call{}.MarshalJSON()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(line)+1, minEventBytes, "minEventBytes must not exceed the smallest serializable event line")
}

func (s *CallsSuite) TestRotateCalls_EmptyBaseIsNoop() {
	t := s.T()
	require.NoError(t, RotateCalls(t.Context(), ""))
}

// TestSelectKept_ReservesAFloorForEveryKind is the governance guarantee: a loud kind must not be
// able to evict a quiet one. Before the floor, rotation kept the newest N lines blind to kind, so
// a burst of agent reads pushed every sandbox_denial out of the window, and gcBlobs then deleted
// their payloads, which is not recoverable.
func (s *CallsSuite) TestSelectKept_ReservesAFloorForEveryKind() {
	t := s.T()
	var lines []string
	line := func(kind journal.CallKind, ts int) string {
		b, err := journal.Call{Time: atMs(int64(ts)), Kind: kind, Name: "x", Outcome: journal.OutcomeOK}.MarshalJSON()
		require.NoError(t, err)
		return string(b)
	}
	// Three rare governance events, then a flood that would bury them under plain recency.
	for i := 1; i <= 3; i++ {
		lines = append(lines, line(journal.CallKindSandboxDenial, i))
	}
	for i := 4; i <= 103; i++ {
		lines = append(lines, line(journal.CallKindAgentCommand, i))
	}

	kept := selectKept(lines, 50)
	require.Len(t, kept, 50)

	var denials int
	for _, l := range kept {
		if strings.Contains(l, string(journal.CallKindSandboxDenial)) {
			denials++
		}
	}
	assert.Equal(t, 3, denials, "every sandbox_denial survives: there are fewer of them than the floor")

	// Order is preserved: the log's readers depend on it.
	assert.True(t, strings.Contains(kept[0], string(journal.CallKindSandboxDenial)), "kept output stays oldest-first")
}

// TestSelectKept_SingleKindIsPlainTruncation pins that the floor changes nothing for a trail with
// one producer: the common case must keep behaving exactly as it did before the policy existed.
func (s *CallsSuite) TestSelectKept_SingleKindIsPlainTruncation() {
	t := s.T()
	var lines []string
	for i := 1; i <= 100; i++ {
		b, err := journal.Call{Time: atMs(int64(i)), Kind: journal.CallKindMCPTool, Name: "t", Outcome: journal.OutcomeOK}.MarshalJSON()
		require.NoError(t, err)
		lines = append(lines, string(b))
	}
	kept := selectKept(lines, 10)
	require.Len(t, kept, 10)
	assert.Contains(t, kept[0], `"ts":91`)
	assert.Contains(t, kept[9], `"ts":100`)
}

// TestTrailRedactsThroughContext is the reason these functions grew a ctx parameter.
// The trail is DURABLE and append-only, so a credential landing here sits on disk until
// someone deletes the file, strictly worse than the same value reaching a terminal.
// Blobs are the sharpest edge: an MCP request/response pair is a whole tool payload,
// persisted verbatim, and nothing else on that path scrubs it.
//
// Delete secrets.Redact in WritePayload, or redactCall in AppendCall, and
// this fails.
func (s *CallsSuite) TestTrailRedactsThroughContext() {
	t := s.T()
	const credential = "sk-live-must-not-be-persisted"
	base := t.TempDir()

	res := secrets.NewResolver()
	t.Setenv("TRAIL_TEST_TOKEN", credential)
	ctx := secrets.WithResolver(t.Context(), res)
	// Reading is what marks the value as a secret: provenance, not shape.
	got, err := res.Read(ctx, "TRAIL_TEST_TOKEN")
	require.NoError(t, err)
	require.Equal(t, credential, got.Reveal())

	ref := writeBlobRef(ctx, base, "mcp", []byte(`{"headers":{"Authorization":"Bearer `+credential+`"}}`))
	require.NotEmpty(t, ref)

	AppendCall(ctx, base, journal.Call{
		Time:    time.Now(),
		Kind:    journal.CallKindAgentCommand,
		Name:    "curl -H 'Authorization: Bearer " + credential + "'",
		Outcome: journal.OutcomeOK,
		Preview: "sent " + credential,
		Error:   "failed with " + credential,
	})

	AppendAgentCommand(ctx, base, AgentCommand{
		Event:   "pre-tool",
		Tool:    "Bash",
		Command: "deploy --token " + credential,
	})

	// Nothing anywhere under the trail directory may contain the value.
	var found []string
	require.NoError(t, filepath.WalkDir(filepath.Join(base, dir), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), credential) {
			found = append(found, path)
		}
		return nil
	}))
	assert.Empty(t, found, "the credential was persisted to the trail in these files")
}

// TestTrailKeepsStructuralFields: redaction must not shred the fields the activity view
// filters on by exact match. Those are enumerated values, identities and content
// addresses; a credential cannot occupy them, and masking them would break the reader
// to protect nothing.
func (s *CallsSuite) TestTrailKeepsStructuralFields() {
	t := s.T()
	base := t.TempDir()
	res := secrets.NewResolver()
	t.Setenv("TRAIL_TEST_TOKEN", "sk-live-must-not-be-persisted")
	ctx := secrets.WithResolver(t.Context(), res)
	_, err := res.Read(ctx, "TRAIL_TEST_TOKEN")
	require.NoError(t, err)

	AppendCall(ctx, base, journal.Call{
		Time:      time.Now(),
		Kind:      journal.CallKindAgentCommand,
		Origin:    job.Origin{Host: "agent-7"},
		Workspace: "/repos/magus",
		Name:      "Bash",
		Outcome:   journal.OutcomeOK,
	})

	b, err := os.ReadFile(filepath.Join(base, dir, eventsFile))
	require.NoError(t, err)
	line := string(b)
	assert.Contains(t, line, "agent-7")
	assert.Contains(t, line, "/repos/magus")
	assert.Contains(t, line, string(journal.CallKindAgentCommand))
}

func (s *CallsSuite) TestObservedCountsReturnsAnUndecodableBlobBesideTheCounts() {
	t := s.T()
	base := t.TempDir()
	AppendAgentCommand(t.Context(), base, AgentCommand{Session: "s1", Tool: ToolFileRead, Path: "a.go"})
	request := WritePayload(t.Context(), base, "agent", []byte("not json"))
	AppendCall(t.Context(), base, journal.Call{Time: atMs(1), Kind: journal.CallKindAgentCommand, Origin: job.Origin{Session: "s1"}, Name: ToolShellCommand, Request: request})

	reads, writes, shell, err := ObservedCounts(t.Context(), base, 100)

	var undecodable *blobUndecodableError
	require.ErrorAs(t, err, &undecodable)
	assert.Equal(t, request.Ref, undecodable.Ref)
	assert.Equal(t, [3]int{1, 0, 0}, [3]int{reads, writes, shell})
}

// recordUndecodable writes an agent command whose request blob is not JSON.
func recordUndecodable(t *testing.T, base string) string {
	t.Helper()
	request := WritePayload(t.Context(), base, "agent", []byte("not json"))
	AppendCall(t.Context(), base, journal.Call{Time: atMs(1), Kind: journal.CallKindAgentCommand, Origin: job.Origin{Session: "s1"}, Lease: "l1", Name: ToolShellCommand, Request: request})
	return request.Ref
}

func (s *CallsSuite) TestCommandProgramReducesToTheProgram() {
	s.Equal("go", CommandProgram("go test ./internal/cache/"))
	s.Equal("magus", CommandProgram("./magus run build ."))
	s.Equal("psql", CommandProgram("/usr/local/bin/psql --password=hunter2"))
	s.Equal("curl", CommandProgram(`SECRET=abc TOKEN=def curl -H "Authorization: Bearer $TOKEN"`))
	s.Equal("sh", CommandProgram(`sh -c "curl -H 'Authorization: Bearer x'"`))
	// A path that merely contains "=" is a program, not an assignment.
	s.Equal("tool", CommandProgram("/opt/we=ird/tool --flag"))
}

// Nothing parseable reduces to empty and is dropped, not guessed at.
func (s *CallsSuite) TestCommandProgramOfNothingParseableIsEmpty() {
	s.Empty(CommandProgram(""))
	s.Empty(CommandProgram("ONLY=assignments HERE=too"))
}

// A log that does not read is not a job that never ran.
func (s *CallsSuite) TestLastJobReturnsALogThatDoesNotRead() {
	t := s.T()
	base := t.TempDir()
	require.NoError(t, os.MkdirAll(eventsPath(base), 0o700))
	_, _, err := LastJob(t.Context(), base, "graph build")
	require.Error(t, err)
	_, _, err = StatCalls(t.Context(), base)
	require.Error(t, err)
}

func (s *CallsSuite) TestRelativizeCutsTheRootAtASeparator() {
	t := s.T()
	assert.Equal(t, "x/y.go", Relativize("/repo", "/repo/x/y.go"))
	assert.Equal(t, "x/y.go", Relativize("/repo/", "/repo/x/y.go"))
	assert.Equal(t, "/repo2/x", Relativize("/repo", "/repo2/x"), "a sibling sharing the prefix is outside the root")
	assert.Equal(t, "x.go", Relativize("/repo", "x.go"))
}

func (s *CallsSuite) TestLastAgentActivityReadsTheNewestObservation() {
	t := s.T()
	base := t.TempDir()
	_, ok := LastAgentActivity(t.Context(),base)
	assert.False(t, ok, "an empty log has no activity to report")

	AppendAgentCommand(t.Context(), base, AgentCommand{Session: "s1", Host: "opencode", Tool: ToolShellCommand, Command: "ls"})
	AppendAgentCommand(t.Context(), base, AgentCommand{Session: "s2", Host: "opencode", Tool: ToolFileRead, Path: "magus.go"})

	got, ok := LastAgentActivity(t.Context(),base)
	require.True(t, ok)
	assert.Equal(t, AgentActivity{Session: "s2", Host: "opencode", At: got.At}, got)
	assert.WithinDuration(t, time.Now(), got.At, time.Minute)

	_, ok = LastAgentActivity(t.Context(),t.TempDir())
	assert.False(t, ok, "an absent log is no activity, not an error")
}

// An observation is found however many calls sit on top of it, and one with
// no time is not one: it would date the answer to 1970.
func (s *CallsSuite) TestLastAgentActivityIsNotBoundedByAWindow() {
	t := s.T()
	base := t.TempDir()
	AppendAgentCommand(t.Context(), base, AgentCommand{Session: "s1", Host: "opencode", Tool: ToolShellCommand, Command: "ls"})
	for range 200 {
		AppendCall(t.Context(), base, journal.Call{Kind: journal.CallKindJob, Time: time.Now()})
	}

	got, ok := LastAgentActivity(t.Context(),base)
	require.True(t, ok, "an observation does not age out because other calls were recorded after it")
	assert.Equal(t, "s1", got.Session)

	stamped := t.TempDir()
	AppendCall(t.Context(), stamped, journal.Call{Kind: journal.CallKindAgentCommand, Origin: job.Origin{Session: "s1"}})
	_, ok = LastAgentActivity(t.Context(),stamped)
	assert.False(t, ok, "an observation with no time has no age to report")
}
