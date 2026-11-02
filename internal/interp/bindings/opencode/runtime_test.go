package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type runtimeSuite struct {
	suite.Suite
	source string
}

func TestOpenCodeRuntime(t *testing.T) { suite.Run(t, new(runtimeSuite)) }
func (s *runtimeSuite) SetupSuite() {
	s.source = Plugin
}
func (s *runtimeSuite) open(invoke Invoke) *Runtime {
	runtime, err := Open(context.Background(), s.source, invoke)
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.NoError(runtime.Close()) })
	return runtime
}
func (s *runtimeSuite) nativeVerdict(body string, expected Result, permission string) {
	runtime := s.open(func(context.Context, Invocation) (Capture, error) { return Capture{Stdout: body}, nil })
	event := Event{Kind: "before", Tool: "bash", Command: "git status", SessionID: "s", CallID: "c"}
	result, err := runtime.Dispatch(context.Background(), event)
	s.Require().NoError(err)
	s.Equal(expected, result)
	event.Kind = "permission"
	result, err = runtime.Dispatch(context.Background(), event)
	s.Require().NoError(err)
	s.Equal(Result{Status: permission}, result)
}
func (s *runtimeSuite) TestPassAllowsToolAndPermission() {
	s.nativeVerdict(`{"schema_version":1,"decision":"pass"}`, Result{}, "allow")
}
func (s *runtimeSuite) TestAdviseAllowsToolAndPermission() {
	s.nativeVerdict(`{"schema_version":1,"decision":"advise","context":"use magus"}`, Result{Context: "use magus"}, "allow")
}
func (s *runtimeSuite) TestAskBlocksToolAndPreservesPermissionAsk() {
	s.nativeVerdict(`{"schema_version":1,"decision":"ask","reason":"approval required"}`, Result{Block: true, Reason: "[magus guard] approval required\n\nThis call needs the approval of the person you work for, and OpenCode will not ask them: only a plain git push, hg push, sl push or jj git push, or a plain magus run of a human-only target, reaches the prompt its own \"permission.bash\" entries configure, which magus describe harness opencode prints. Ask them to run it from their own terminal. If they saw no such message, relay this one to them verbatim."}, "ask")
}
func (s *runtimeSuite) TestDenyBlocksToolAndPermission() {
	s.nativeVerdict(`{"schema_version":1,"decision":"deny","reason":"blocked"}`, Result{Block: true, Reason: "[magus guard] blocked"}, "deny")
}
func (s *runtimeSuite) TestMalformedAdviceBlocksToolAndPermission() {
	s.nativeVerdict(`{"schema_version":1,"decision":"advise"}`, Result{Block: true, Reason: `[magus guard] magus guard returned the decision "advise", which this plugin does not know, so it refuses the call rather than allow it. Update the plugin from the magus docs.`}, "deny")
}
func (s *runtimeSuite) TestFutureSchemaBlocksToolAndPreservesPrompt() {
	s.nativeVerdict(`{"schema_version":2,"decision":"pass"}`, Result{Block: true, Reason: "[magus guard] magus guard could not judge this call: magus answered verdict schema 2, not 1; update this plugin from the magus docs; refused.\nInstall magus (https://eli.gladman.cc/magus/guides/setup/), build it from source (https://eli.gladman.cc/magus/guides/setup/build-from-source/), or set __MAGUS_BIN to its path."}, "")
}
func (s *runtimeSuite) TestUnknownDecisionBlocksToolAndPermission() {
	s.nativeVerdict(`{"schema_version":1,"decision":"future"}`, Result{Block: true, Reason: `[magus guard] magus guard returned the decision "future", which this plugin does not know, so it refuses the call rather than allow it. Update the plugin from the magus docs.`}, "deny")
}
func (s *runtimeSuite) TestNonJSONBlocksToolAndPreservesPrompt() {
	s.nativeVerdict(`not json`, Result{Block: true, Reason: "[magus guard] magus guard could not judge this call: magus answered something that is not JSON; refused.\nInstall magus (https://eli.gladman.cc/magus/guides/setup/), build it from source (https://eli.gladman.cc/magus/guides/setup/build-from-source/), or set __MAGUS_BIN to its path."}, "")
}
func (s *runtimeSuite) TestPromptableAskUsesHostApproval() {
	runtime := s.open(func(context.Context, Invocation) (Capture, error) {
		s.Fail("direct mapping must not invoke native magus")
		return Capture{}, nil
	})
	result, err := runtime.Dispatch(context.Background(), Event{Kind: "decide", Promptable: true, Verdict: &Verdict{SchemaVersion: 1, Decision: "ask", Reason: "human required"}})
	s.Require().NoError(err)
	s.Equal(Result{}, result)
}
func (s *runtimeSuite) TestUnpromptableAskRefusesTool() {
	runtime := s.open(func(context.Context, Invocation) (Capture, error) {
		s.Fail("direct mapping must not invoke native magus")
		return Capture{}, nil
	})
	result, err := runtime.Dispatch(context.Background(), Event{Kind: "decide", Verdict: &Verdict{SchemaVersion: 1, Decision: "ask", Reason: "human required"}})
	s.Require().NoError(err)
	s.True(result.Block)
	s.Contains(result.Reason, "human required")
	s.Contains(result.Reason, "OpenCode will not ask")
	s.Empty(result.Context)
	s.Empty(result.Status)
	s.False(result.MCP)
}
func (s *runtimeSuite) TestUnjudgedAdapterMappingPreservesHostPrompt() {
	runtime := s.open(func(context.Context, Invocation) (Capture, error) {
		s.Fail("direct mapping must not invoke native magus")
		return Capture{}, nil
	})
	result, err := runtime.Dispatch(context.Background(), Event{Kind: "permission-map", Verdict: &Verdict{Decision: "unjudged", Why: "unavailable"}})
	s.Require().NoError(err)
	s.Equal(Result{}, result)
}
func (s *runtimeSuite) TestNativeArgumentsAndLegacyRetry() {
	var calls []Invocation
	runtime := s.open(func(_ context.Context, inv Invocation) (Capture, error) {
		calls = append(calls, inv)
		if len(calls) == 1 {
			return Capture{}, nil
		}
		return Capture{Stdout: `{"schema_version":1,"decision":"pass"}`}, nil
	})
	result, err := runtime.Dispatch(context.Background(), Event{Kind: "before", Tool: "edit", Path: "a;$(secret).go", RootSessionID: "root", AgentID: "worker"})
	s.Require().NoError(err)
	s.Equal(Result{}, result)
	s.Equal([]Invocation{
		{Args: []string{"shell", "--path", "--agent-name", "opencode", "--renders-ask", "-o", "json", "--session", "root", "--agent", "worker"}, Input: "a;$(secret).go"},
		{Args: []string{"shell", "--path", "-o", "json"}, Input: "a;$(secret).go"},
	}, calls, "untrusted payload remains stdin across compatibility retry")
}
func (s *runtimeSuite) TestEnvelopesAndSessionScopedAdvisories() {
	var captured Invocation
	runtime := s.open(func(_ context.Context, inv Invocation) (Capture, error) {
		captured = inv
		return Capture{Stdout: `{"schema_version":1,"decision":"advise","context":"first advice"}`}, nil
	})
	event := Event{Kind: "before", Tool: "mcp__magus__magus_query", SessionID: "a", CallID: "same", RootSessionID: "root", AgentID: "child", Cwd: "/workspace", ArgsJSON: `{"query":"hello"}`, MCP: true}
	result, err := runtime.Dispatch(context.Background(), event)
	s.Require().NoError(err)
	s.Equal(Result{Context: "first advice"}, result)
	var body map[string]any
	s.Require().NoError(json.Unmarshal([]byte(captured.Input), &body))
	s.Equal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": "mcp__magus__magus_query", "session_id": "root", "agent_id": "child", "cwd": "/workspace", "tool_input": map[string]any{"query": "hello"}}, body)
	_, err = runtime.Dispatch(context.Background(), Event{Kind: "remember", SessionID: "b", CallID: "same", Context: "other advice"})
	s.Require().NoError(err)
	result, err = runtime.Dispatch(context.Background(), Event{Kind: "take", SessionID: "b", CallID: "same"})
	s.Require().NoError(err)
	s.Equal(Result{Context: "other advice"}, result)
	result, err = runtime.Dispatch(context.Background(), Event{Kind: "take", SessionID: "a", CallID: "same"})
	s.Require().NoError(err)
	s.Equal(Result{Context: "first advice", MCP: true}, result)
	result, err = runtime.Dispatch(context.Background(), Event{Kind: "take", SessionID: "a", CallID: "same"})
	s.Require().NoError(err)
	s.Equal(Result{}, result)
}
func (s *runtimeSuite) TestOverlappingCallsAreSerialized() {
	var active, maxActive atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	runtime := s.open(func(ctx context.Context, inv Invocation) (Capture, error) {
		count := active.Add(1)
		if count > maxActive.Load() {
			maxActive.Store(count)
		}
		defer active.Add(-1)
		if inv.Input == "first" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return Capture{}, ctx.Err()
			}
		}
		encoded, _ := json.Marshal(map[string]any{"schema_version": 1, "decision": "advise", "context": inv.Input})
		return Capture{Stdout: string(encoded)}, nil
	})
	var wg sync.WaitGroup
	wg.Add(2)
	results := make(chan response, 2)
	go func() {
		defer wg.Done()
		v, err := runtime.Dispatch(context.Background(), Event{Kind: "before", Tool: "bash", Command: "first", SessionID: "s", CallID: "1"})
		results <- response{v, err}
	}()
	<-entered
	go func() {
		defer wg.Done()
		v, err := runtime.Dispatch(context.Background(), Event{Kind: "before", Tool: "bash", Command: "second", SessionID: "s", CallID: "2"})
		results <- response{v, err}
	}()
	close(release)
	wg.Wait()
	s.Equal(int32(1), maxActive.Load())
	s.NoError((<-results).err)
	s.NoError((<-results).err)
	first, err := runtime.Dispatch(context.Background(), Event{Kind: "take", SessionID: "s", CallID: "1"})
	s.Require().NoError(err)
	s.Equal(Result{Context: "first"}, first)
	second, err := runtime.Dispatch(context.Background(), Event{Kind: "take", SessionID: "s", CallID: "2"})
	s.Require().NoError(err)
	s.Equal(Result{Context: "second"}, second)
}
func (s *runtimeSuite) TestCancellationAndShutdown() {
	entered := make(chan struct{})
	runtime := s.open(func(ctx context.Context, _ Invocation) (Capture, error) {
		close(entered)
		<-ctx.Done()
		return Capture{}, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := runtime.Dispatch(ctx, Event{Kind: "before", Tool: "bash", Command: "waiting"})
		finished <- err
	}()
	<-entered
	cancel()
	select {
	case err := <-finished:
		s.ErrorIs(err, context.Canceled)
	case <-time.After(time.Second):
		s.FailNow("dispatch did not cancel")
	}
	s.NoError(runtime.Close())
	_, err := runtime.Dispatch(context.Background(), Event{Kind: "take"})
	s.ErrorContains(err, "runtime closed")
}
func (s *runtimeSuite) TestInitializationFailsVisibly() {
	_, err := Open(context.Background(), "this is not buzz", func(context.Context, Invocation) (Capture, error) { return Capture{}, errors.New("unused") })
	s.ErrorContains(err, "load plugin.buzz")
}
func (s *runtimeSuite) TestMissingNativeCapabilityFailsVisibly() {
	_, err := Open(context.Background(), "", nil)
	s.ErrorContains(err, "missing native invocation")
}
