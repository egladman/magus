package opencode

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type fakeClient struct {
	mu       sync.Mutex
	messages []sessionMessage
	files    map[string]bool
	vars     map[string]string
	prompts  []string
	warnings []string
	failSend error
}

func (c *fakeClient) sessionParent(context.Context, string) (string, error) { return "", nil }
func (c *fakeClient) sessionMessages(context.Context, string) ([]sessionMessage, error) {
	return c.messages, nil
}
func (c *fakeClient) prompt(_ context.Context, id, message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.prompts = append(c.prompts, id+": "+message)
	return c.failSend
}
func (c *fakeClient) notify(context.Context, string) {}
func (c *fakeClient) warn(message string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings = append(c.warnings, message)
}
func (c *fakeClient) exists(_ context.Context, file string) bool { return c.files[file] }
func (c *fakeClient) env(name string) string                     { return c.vars[name] }

// recorder answers every magus call with stdout and keeps the calls.
type recorder struct {
	mu     sync.Mutex
	stdout string
	calls  [][]string
}

func (r *recorder) invoke(_ context.Context, in Invocation) (Capture, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, in.Args)
	return Capture{Stdout: r.stdout}, nil
}

func (r *recorder) ran(args ...string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.calls, func(call []string) bool { return slices.Equal(call[:min(len(call), len(args))], args) })
}

type pluginSuite struct {
	suite.Suite
	client *fakeClient
	magus  *recorder
	plugin *plugin
}

func TestPlugin(t *testing.T) { suite.Run(t, new(pluginSuite)) }

func (s *pluginSuite) SetupTest() {
	s.client = &fakeClient{files: map[string]bool{}, vars: map[string]string{}}
	s.magus = &recorder{stdout: `{"schema_version":1,"decision":"pass"}`}
	runtime, err := Open(context.Background(), Plugin, s.magus.invoke)
	s.Require().NoError(err)
	s.T().Cleanup(func() { s.NoError(runtime.Close()) })
	s.plugin = newPlugin(s.client, s.magus.invoke, "/repo", runtime)
}

func (s *pluginSuite) TestPromptKeyNamesAPushVerb() {
	s.Equal("git push", promptKey("git push origin main"))
}

func (s *pluginSuite) TestPromptKeyNamesAMagusRun() {
	s.Equal("./magus run release", promptKey("./magus  run release ."))
}

func (s *pluginSuite) TestPromptKeyRefusesShellSyntax() {
	s.Empty(promptKey("git push; rm -rf ."))
}

func (s *pluginSuite) TestPromptKeyRefusesAFlagForTarget() {
	s.Empty(promptKey("magus run --list"))
}

func (s *pluginSuite) TestMagusToolNameStripsAListedServer() {
	servers := []mcpServer{{key: "tools.dev", listed: true, command: []string{"/bin/magus", "mcp"}}}
	s.Equal("query", magusToolName("tools_dev_query", servers))
}

func (s *pluginSuite) TestMagusToolNameTrustsAnUnlistedMagusKey() {
	s.Equal("refs", magusToolName("magus_refs", []mcpServer{{key: "magus"}}))
}

func (s *pluginSuite) TestMagusToolNameIgnoresAnotherServer() {
	servers := []mcpServer{{key: "other", listed: true, command: []string{"node", "server.js"}}}
	s.Empty(magusToolName("other_query", servers))
}

func (s *pluginSuite) TestLastReplyTextStopsAtTheUser() {
	messages := []sessionMessage{s.message("assistant", "old"), s.message("user", "ask")}
	s.Empty(lastReplyText(messages))
}

func (s *pluginSuite) TestLastReplyTextSkipsSyntheticParts() {
	reply := s.message("assistant", "said")
	reply.Parts = append(reply.Parts, messagePart{Type: "text", Text: "injected", Synthetic: true})
	s.Equal("said", lastReplyText([]sessionMessage{s.message("user", "ask"), reply}))
}

func (s *pluginSuite) message(role, text string) sessionMessage {
	var m sessionMessage
	m.Info.Role = role
	m.Parts = []messagePart{{Type: "text", Text: text}}
	return m
}

func (s *pluginSuite) TestWorkspaceBinaryPrefersTheWorkspaceCopy() {
	exists := func(_ context.Context, file string) bool { return file == "/w/magus.yaml" || file == "/w/magus" }
	s.Equal("/w/magus", workspaceBinary(context.Background(), "/w/sub/dir", exists))
}

func (s *pluginSuite) TestWorkspaceBinaryFallsBackToPath() {
	s.Equal("magus", workspaceBinary(context.Background(), "/w", func(context.Context, string) bool { return false }))
}

func (s *pluginSuite) TestParseTimeoutRefusesAFraction() {
	_, err := parseTimeout("/repo", "magus", 1.5)
	s.Error(err)
}

func (s *pluginSuite) TestParseTimeoutReadsMilliseconds() {
	timeout, err := parseTimeout("/repo", "magus", 250)
	s.Require().NoError(err)
	s.Equal(250*time.Millisecond, timeout)
}

func (s *pluginSuite) TestSpawnErrorKeepsCancellation() {
	s.ErrorIs(spawnError("magus", context.Canceled), context.Canceled)
}

func (s *pluginSuite) TestSpawnErrorNamesTheBinary() {
	s.EqualError(spawnError("/bin/magus", errors.New("ENOENT")), "/bin/magus could not be run")
}

func (s *pluginSuite) TestDispatchJSONAnswersTheResult() {
	encoded, err := dispatchJSON(context.Background(), s.plugin.runtime, `{"kind":"take","sessionID":"s","callID":"c"}`)
	s.Require().NoError(err)
	s.JSONEq(`{"block":false,"reason":"","context":"","status":"","mcp":false}`, encoded)
}

func (s *pluginSuite) TestToolBeforeBlocksADeniedCommand() {
	s.magus.stdout = `{"schema_version":1,"decision":"deny","reason":"blocked"}`
	err := s.plugin.toolBefore(context.Background(), toolCall{Session: "s", Tool: "bash", CallID: "c", Args: []byte(`{"command":"rm -rf ."}`)})
	s.EqualError(err, "[magus guard] blocked")
}

func (s *pluginSuite) TestToolBeforeSkipsAnEditWithoutAPath() {
	s.Require().NoError(s.plugin.toolBefore(context.Background(), toolCall{Session: "s", Tool: "edit", Args: []byte(`{}`)}))
	s.Empty(s.magus.calls)
}

func (s *pluginSuite) TestToolBeforeAddsTheFilePathToAnotherTool() {
	call := toolCall{Session: "s", Tool: "read", Args: []byte(`{"filePath":"a.go"}`)}
	event := Event{}
	ready, err := s.plugin.describeCall(context.Background(), &event, call, who{session: "s"})
	s.Require().NoError(err)
	s.True(ready)
	s.JSONEq(`{"filePath":"a.go","file_path":"a.go"}`, event.ArgsJSON)
	s.True(s.magus.ran("buzz", "-C", "/repo", "-s", observeScript))
}

func (s *pluginSuite) TestPermissionAskAllowsAPassedPush() {
	s.plugin.configure(pluginConfig{bash: map[string]string{"git push": "ask", "git push *": "ask"}})
	status, err := s.plugin.permissionAsk(context.Background(), permissionRequest{Type: "bash", Command: "git push", Session: "s"})
	s.Require().NoError(err)
	s.Equal("allow", status)
}

func (s *pluginSuite) TestPermissionAskLeavesAnUnconfiguredCommand() {
	status, err := s.plugin.permissionAsk(context.Background(), permissionRequest{Type: "bash", Command: "git push", Session: "s"})
	s.Require().NoError(err)
	s.Empty(status)
	s.Empty(s.magus.calls)
}

func (s *pluginSuite) TestToolAfterTakesHeldNotesOnlyWhenTheOutputCarriesThem() {
	s.plugin.briefNote = "brief"
	edits, err := s.plugin.toolAfter(context.Background(), toolCall{Session: "s", Tool: "read"}, toolResult{})
	s.Require().NoError(err)
	s.Empty(edits)
	edits, err = s.plugin.toolAfter(context.Background(), toolCall{Session: "s", Tool: "read"}, toolResult{Notable: true})
	s.Require().NoError(err)
	s.Equal([]noteEdit{{Text: "brief"}}, edits)
}

func (s *pluginSuite) TestGuardStopSendsTheRefusalAndSkipsItsEcho() {
	s.magus.stdout = `{"schema_version":1,"decision":"deny","reason":"finish the job"}`
	s.True(s.plugin.guardStop(context.Background(), "root", false))
	s.Equal([]string{"root: [magus guard] finish the job"}, s.client.prompts)
	s.plugin.chatMessage(context.Background(), "root", []messagePart{{Type: "text", Text: "[magus guard] finish the job"}})
	s.False(s.magus.ran("shell", "--message"))
}

func (s *pluginSuite) TestGuardStopForgetsAFollowUpItCouldNotSend() {
	s.magus.stdout = `{"schema_version":1,"decision":"deny","reason":"finish the job"}`
	s.client.failSend = errors.New("offline")
	s.False(s.plugin.guardStop(context.Background(), "root", false))
	s.False(s.plugin.hasFollowUp("root"))
	s.True(strings.HasSuffix(s.client.warnings[0], "offline"))
}

func (s *pluginSuite) TestChatMessageRecordsAPersonsMessage() {
	s.plugin.chatMessage(context.Background(), "root", []messagePart{{Type: "text", Text: "ship it"}})
	s.True(s.magus.ran("shell", "--message", "--agent-name", agentName, "--session", "root"))
}

func (s *pluginSuite) TestWhoIsWalksToTheRootSession() {
	s.plugin.setParent("child", "parent")
	s.plugin.setParent("parent", "root")
	s.Equal(who{session: "root", agent: "child"}, s.plugin.whoIs(context.Background(), "child"))
}

func (s *pluginSuite) TestIdleBackgroundChildIsPromptedWhenRefused() {
	s.magus.stdout = `{"schema_version":1,"decision":"deny","reason":"report back"}`
	s.plugin.background["child"] = false
	s.plugin.event(context.Background(), sessionEvent{Kind: "session.idle", Session: "child"})
	s.Equal([]string{"child: [magus guard] report back"}, s.client.prompts)
	s.True(s.plugin.background["child"])
}
