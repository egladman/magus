package opencode

import (
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/json"
)

const (
	reclaimDiskScript = ".opencode/magus/runtime/magus-reclaim-disk.buzz"
	buzzCheckScript   = ".opencode/magus/runtime/magus-buzz-check.buzz"
	observeScript     = ".opencode/magus/runtime/magus-observe.buzz"
	magusFix          = "Install magus (https://eli.gladman.cc/magus/guides/setup/), build it from source (https://eli.gladman.cc/magus/guides/setup/build-from-source/), or set __MAGUS_BIN to its path."
	agentName         = "opencode"
	hookTimeout       = 30 * time.Second
	maxBrief          = 16384
)

var (
	unsafePrompt = regexp.MustCompile("[;&|`$()<>\\\\\n]")
	unsafeName   = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
	pushVerbs    = []string{"git push", "hg push", "sl push", "jj git push"}
	editTools    = []string{"edit", "write", "apply_patch"}
)

// pluginClient is what the plugin asks of the harness's JavaScript SDK.
type pluginClient interface {
	sessionParent(ctx context.Context, id string) (string, error)
	sessionMessages(ctx context.Context, id string) ([]sessionMessage, error)
	prompt(ctx context.Context, id, message string) error
	notify(ctx context.Context, message string)
	warn(message string)
	exists(ctx context.Context, file string) bool
	env(name string) string
}

// sessionMessage is one row of the harness's session.messages answer.
type sessionMessage struct {
	Info struct {
		Role any `json:"role"`
	} `json:"info"`
	Parts []messagePart `json:"parts"`
}

// messagePart keeps each field as JSON gave it: a non-string text reads as
// empty, and synthetic and ignored follow JavaScript truthiness.
type messagePart struct {
	Type      any `json:"type"`
	Text      any `json:"text"`
	Synthetic any `json:"synthetic"`
	Ignored   any `json:"ignored"`
}

type toolCall struct {
	Session string
	Tool    string
	CallID  string
	Args    []byte // the args as JSON.stringify wrote them; nil when undefined
}

type toolResult struct {
	Notable    bool // the output takes a note: a content array or an output string
	Child      string
	Background bool
}

// noteEdit is one note the JavaScript side adds to a tool's output, in order.
type noteEdit struct {
	Text     string
	Advisory bool
	MCP      bool
}

type permissionRequest struct {
	Type    string
	Command string
	Session string
}

type sessionEvent struct {
	Kind    string
	ID      string
	Parent  string
	Session string
}

type mcpServer struct {
	key     string
	listed  bool // command is an array
	command []string
}

type pluginConfig struct {
	bash    map[string]string
	servers []mcpServer
}

type who struct{ session, agent string }

// plugin routes one harness instance's hooks. Its methods are safe for
// concurrent use.
type plugin struct {
	client    pluginClient
	invoke    Invoke
	directory string
	runtime   *Runtime

	mu                  sync.Mutex
	parents             map[string]string
	followUps           map[string]string
	background          map[string]bool
	config              pluginConfig
	diskNote, briefNote string
}

func newPlugin(client pluginClient, invoke Invoke, directory string, runtime *Runtime) *plugin {
	return &plugin{client: client, invoke: invoke, directory: directory, runtime: runtime,
		parents: map[string]string{}, followUps: map[string]string{}, background: map[string]bool{}}
}

func str(value any) string {
	text, _ := value.(string)
	return text
}

func truthy(value any) bool {
	switch v := value.(type) {
	case nil:
		return false
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v != ""
	}
	return true
}

func jsonBody(value any) string { data, _ := json.Marshal(value); return string(data) }

func shellArgs() []string {
	return []string{"shell", "--agent-name", agentName, "--renders-ask", "-o", "json"}
}

// fields decodes the call's args as an object, or nil when they are not one.
func (c toolCall) fields() map[string]any {
	var fields map[string]any
	if json.Unmarshal(c.Args, &fields) != nil {
		return nil
	}
	return fields
}

func (c toolCall) argsJSON() (string, error) {
	if len(c.Args) == 0 {
		return "", errors.New("harness plugin: value cannot be encoded as JSON")
	}
	return string(c.Args), nil
}

func argString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if found := str(fields[key]); found != "" {
			return found
		}
	}
	return ""
}

func promptKey(command string) string {
	if unsafePrompt.MatchString(command) {
		return ""
	}
	if verb := pushVerb(command); verb != "" {
		return verb
	}
	return magusRunKey(command)
}

func pushVerb(command string) string {
	for _, verb := range pushVerbs {
		if command == verb || strings.HasPrefix(command, verb+" ") {
			return verb
		}
	}
	return ""
}

// magusRunKey splits on literal spaces only, as the harness's plain-prefix
// permission syntax does.
func magusRunKey(command string) string {
	words := strings.FieldsFunc(command, func(r rune) bool { return r == ' ' })
	if len(words) >= 3 && isMagusWord(words[0]) && isRunVerb(words[1]) && !strings.HasPrefix(words[2], "-") {
		return strings.Join(words[:3], " ")
	}
	return ""
}

func isMagusWord(word string) bool { return word == "magus" || word == "./magus" }
func isRunVerb(word string) bool   { return word == "run" || word == "affected" }

func (s mcpServer) isMagus() bool {
	if !s.listed {
		return s.key == "magus"
	}
	binary := slices.ContainsFunc(s.command, func(word string) bool { return path.Base(word) == "magus" })
	return binary && slices.Contains(s.command, "mcp")
}

// magusToolName is tool without the prefix the harness gives a magus MCP
// server's tools, or "" when no magus server names it.
func magusToolName(tool string, servers []mcpServer) string {
	for _, s := range servers {
		prefix := unsafeName.ReplaceAllString(s.key, "_") + "_"
		if s.isMagus() && strings.HasPrefix(tool, prefix) && len(tool) > len(prefix) {
			return tool[len(prefix):]
		}
	}
	return ""
}

func lastReplyText(messages []sessionMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		switch str(messages[i].Info.Role) {
		case "user":
			return ""
		case "assistant":
			if reply := replyText(messages[i].Parts); reply != "" {
				return reply
			}
		}
	}
	return ""
}

func replyText(parts []messagePart) string {
	var texts []string
	for _, part := range parts {
		if spoken(part) {
			texts = append(texts, str(part.Text))
		}
	}
	return strings.Join(texts, "\n\n")
}

func spoken(part messagePart) bool {
	return str(part.Type) == "text" && !truthy(part.Synthetic) && !truthy(part.Ignored) && strings.TrimSpace(str(part.Text)) != ""
}

func typedTexts(parts []messagePart) []string {
	var texts []string
	for _, part := range parts {
		if str(part.Type) == "text" && !truthy(part.Synthetic) {
			texts = append(texts, str(part.Text))
		}
	}
	return texts
}

func nearest(ctx context.Context, exists func(context.Context, string) bool, name, directory string) string {
	for at := directory; ; at = path.Dir(at) {
		if exists(ctx, path.Join(at, name)) {
			return at
		}
		if path.Dir(at) == at {
			return ""
		}
	}
}

// workspaceBinary is the workspace's ./magus, else magus on PATH.
func workspaceBinary(ctx context.Context, cwd string, exists func(context.Context, string) bool) string {
	root := nearest(ctx, exists, "magus.yaml", cwd)
	if root == "" {
		root = nearest(ctx, exists, "magusfile.buzz", cwd)
	}
	if root != "" && exists(ctx, path.Join(root, "magus")) {
		return path.Join(root, "magus")
	}
	return "magus"
}

// spawnError keeps cancellation as it is and names the binary otherwise.
func spawnError(binary string, err error) error {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("%s could not be run", binary)
}

func parseTimeout(directory, binary string, milliseconds float64) (time.Duration, error) {
	if directory == "" || binary == "" || !validMilliseconds(milliseconds) {
		return 0, errors.New("harness plugin: invalid directory, binary or timeout")
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}

func validMilliseconds(ms float64) bool {
	return ms >= 1 && ms <= 2147483647 && ms == float64(int64(ms))
}

// dispatchJSON runs one event given as JSON and answers its result as JSON.
func dispatchJSON(ctx context.Context, r *Runtime, data string) (string, error) {
	var event Event
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return "", fmt.Errorf("harness plugin: event: %w", err)
	}
	result, err := r.Dispatch(ctx, event)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

func (p *plugin) configure(config pluginConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.config = config
}

func (p *plugin) asksBefore(command string) bool {
	key := promptKey(command)
	if key == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.config.bash[key] == "ask" && p.config.bash[key+" *"] == "ask"
}

func (p *plugin) magusTool(tool string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return magusToolName(tool, p.config.servers)
}

func (p *plugin) run(ctx context.Context, args []string, input string) (Capture, error) {
	return p.invoke(ctx, Invocation{Args: args, Input: input})
}

func (p *plugin) runOnce(ctx context.Context, args []string, input string) string {
	capture, err := p.run(ctx, args, input)
	if err != nil {
		return ""
	}
	return capture.Stdout
}

func (p *plugin) judge(ctx context.Context, args []string, input string) Verdict {
	return Judge(ctx, p.invoke, Invocation{Args: args, Input: input})
}

func (p *plugin) runGlue(ctx context.Context, script string, event any) string {
	return p.runOnce(ctx, []string{"buzz", "-C", p.directory, "-s", script, "--", "--agent-name", agentName}, jsonBody(event))
}

func (p *plugin) setParent(id, parent string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.parents[id] = parent
}

func (p *plugin) parentOf(ctx context.Context, id string) string {
	p.mu.Lock()
	parent, known := p.parents[id]
	p.mu.Unlock()
	if known {
		return parent
	}
	parent, _ = p.client.sessionParent(ctx, id)
	p.mu.Lock()
	defer p.mu.Unlock()
	// A lookup may overlap session.created; prefer the actual event.
	if current, ok := p.parents[id]; ok {
		return current
	}
	p.parents[id] = parent
	return parent
}

func (p *plugin) whoIs(ctx context.Context, id string) who {
	if id == "" {
		return who{}
	}
	root := id
	for range 16 {
		parent := p.parentOf(ctx, root)
		if parent == "" {
			break
		}
		root = parent
	}
	if root == id {
		return who{session: root}
	}
	return who{session: root, agent: id}
}

func (p *plugin) envelope(event string, identity who, tool string, input, response any) string {
	body := map[string]any{"hook_event_name": event, "tool_name": tool, "tool_input": input, "cwd": p.directory}
	if identity.session != "" {
		body["session_id"] = identity.session
	}
	if identity.agent != "" {
		body["agent_id"] = identity.agent
	}
	if response != nil {
		body["tool_response"] = response
	}
	return jsonBody(body)
}

func (p *plugin) observe(ctx context.Context, identity who, file string) {
	p.runGlue(ctx, observeScript, map[string]any{"session_id": identity.session, "tool_input": map[string]any{"file_path": file}, "cwd": p.directory})
}

// buzzEdit reports whether an edit of file, or a patch, touches Buzz.
func buzzEdit(file, patch string) bool {
	if file != "" {
		return strings.HasSuffix(file, ".buzz")
	}
	return strings.Contains(patch, ".buzz")
}

func (p *plugin) buzzCheck(ctx context.Context, tool string, fields map[string]any) string {
	file, patch := argString(fields, "filePath", "file_path", "path"), argString(fields, "patchText")
	if !buzzEdit(file, patch) {
		return ""
	}
	input := map[string]any{"patch": patch}
	if file != "" {
		input = map[string]any{"file_path": file}
	}
	said := p.runGlue(ctx, buzzCheckScript, map[string]any{"tool_name": tool, "tool_input": input, "cwd": p.directory})
	var reply struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if json.Unmarshal([]byte(said), &reply) == nil && reply.Decision == "block" {
		return reply.Reason
	}
	return ""
}

func (p *plugin) holdBrief(ctx context.Context) {
	brief := strings.TrimSpace(p.runOnce(ctx, []string{"session", "--brief"}, ""))
	if brief == "" {
		return
	}
	if len(brief) > maxBrief {
		p.client.warn(fmt.Sprintf("[magus guard] session --brief was %d bytes, over %d; dropping it", len(brief), maxBrief))
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.briefNote = brief
}

func (p *plugin) sampleDisk(ctx context.Context) {
	note := strings.TrimSpace(p.runGlue(ctx, reclaimDiskScript, map[string]any{"cwd": p.directory}))
	if note == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.diskNote = note
}

func (p *plugin) lastReply(ctx context.Context, id string) string {
	messages, err := p.client.sessionMessages(ctx, id)
	if err != nil {
		return ""
	}
	return lastReplyText(messages)
}

// takeFollowUp clears id's pending follow-up and reports whether it had one.
func (p *plugin) takeFollowUp(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, followed := p.followUps[id]
	delete(p.followUps, id)
	return followed
}

func (p *plugin) hasFollowUp(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, followed := p.followUps[id]
	return followed
}

// echoesFollowUp reports whether message is the follow-up the plugin sent to
// id, and otherwise forgets that follow-up.
func (p *plugin) echoesFollowUp(id, message string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.followUps[id] == message {
		return true
	}
	delete(p.followUps, id)
	return false
}

// sendFollowUp prompts id with message, remembered so its echo is skipped.
func (p *plugin) sendFollowUp(ctx context.Context, id, message, failure string) bool {
	p.mu.Lock()
	p.followUps[id] = message
	p.mu.Unlock()
	err := p.client.prompt(ctx, id, message)
	if err == nil {
		return true
	}
	p.mu.Lock()
	delete(p.followUps, id)
	p.mu.Unlock()
	p.client.warn("[magus guard] " + failure + err.Error())
	return false
}

func (p *plugin) checkReply(ctx context.Context, id string) {
	if p.takeFollowUp(id) {
		return
	}
	script := p.client.env("__PROOFREAD_REPLY_BUZZ")
	if script == "" || !p.client.exists(ctx, script) {
		return
	}
	if reply := p.lastReply(ctx, id); reply != "" {
		p.followUpReply(ctx, id, script, reply)
	}
}

func (p *plugin) followUpReply(ctx context.Context, id, script, reply string) {
	ran, err := p.run(ctx, []string{"buzz", "-s", script, "--", "--agent-name", agentName}, jsonBody(map[string]any{"last_assistant_message": reply, "stop_hook_active": false}))
	if err != nil {
		return
	}
	if notice := strings.TrimSpace(ran.Stderr); notice != "" {
		p.client.notify(ctx, notice)
	}
	if prompt := replyPrompt(ran.Stdout); prompt != "" {
		p.sendFollowUp(ctx, id, prompt, "could not send the reply check back: ")
	}
}

func replyPrompt(stdout string) string {
	var response struct {
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal([]byte(stdout), &response) != nil {
		return ""
	}
	return response.Prompt
}

func (p *plugin) warnVerdict(verdict Verdict) {
	switch verdict.Decision {
	case "unjudged":
		p.client.warn("[magus guard] " + verdict.Why + "\n" + magusFix)
	case "advise":
		p.client.warn("[magus guard] " + verdict.Context)
	}
}

func (p *plugin) guardStop(ctx context.Context, id string, followed bool) bool {
	body := jsonBody(map[string]any{"hook_event_name": "Stop", "session_id": id, "stop_hook_active": followed, "cwd": p.directory})
	verdict := p.judge(ctx, shellArgs(), body)
	if verdict.Decision != "deny" {
		p.warnVerdict(verdict)
		return false
	}
	return p.sendFollowUp(ctx, id, "[magus guard] "+verdict.Reason, "could not send the stop refusal back: ")
}

func (p *plugin) guardSubagentStop(ctx context.Context, identity who, child string, followed bool) string {
	body := jsonBody(map[string]any{"hook_event_name": "SubagentStop", "session_id": identity.session, "agent_id": child, "stop_hook_active": followed, "cwd": p.directory})
	verdict := p.judge(ctx, shellArgs(), body)
	p.warnVerdict(verdict)
	if verdict.Decision == "deny" {
		return verdict.Reason
	}
	return ""
}

func (p *plugin) isBackground(id string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, background := p.background[id]
	return background
}

// markBackground records whether child was refused, and forgets it when not.
func (p *plugin) markBackground(child, reason string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if reason == "" {
		delete(p.background, child)
		return false
	}
	p.background[child] = true
	return true
}

func (p *plugin) backgroundChildIdle(ctx context.Context, child string) {
	p.mu.Lock()
	followed := p.background[child]
	p.mu.Unlock()
	reason := p.guardSubagentStop(ctx, p.whoIs(ctx, child), child, followed)
	if !p.markBackground(child, reason) {
		return
	}
	if err := p.client.prompt(ctx, child, "[magus guard] "+reason); err != nil {
		p.client.warn("[magus guard] could not send the subagent stop refusal back: " + err.Error())
	}
}

func (p *plugin) toolBefore(ctx context.Context, call toolCall) error {
	identity := p.whoIs(ctx, call.Session)
	event := Event{Kind: "before", Tool: call.Tool, SessionID: call.Session, CallID: call.CallID, RootSessionID: identity.session, AgentID: identity.agent, Cwd: p.directory}
	ready, err := p.describeCall(ctx, &event, call, identity)
	if err != nil || !ready {
		return err
	}
	result, err := p.runtime.Dispatch(ctx, event)
	if err != nil {
		return err
	}
	if result.Block {
		return errors.New(result.Reason)
	}
	return nil
}

// describeCall fills event from the call's args, false when there is
// nothing to judge.
func (p *plugin) describeCall(ctx context.Context, event *Event, call toolCall, identity who) (bool, error) {
	fields := call.fields()
	switch call.Tool {
	case "bash":
		return p.describeCommand(event, fields), nil
	case "edit", "write":
		event.Path = argString(fields, "filePath", "file_path", "path")
		return event.Path != "", nil
	case "apply_patch":
		return describePatch(event, fields), nil
	case "task":
		return describeTask(event, call, fields)
	}
	return p.describeOther(ctx, event, call, fields, identity)
}

func (p *plugin) describeCommand(event *Event, fields map[string]any) bool {
	event.Command = argString(fields, "command")
	if event.Command == "" {
		return false
	}
	event.Promptable = p.asksBefore(event.Command)
	return true
}

func describePatch(event *Event, fields map[string]any) bool {
	patch := argString(fields, "patchText")
	if patch == "" {
		return false
	}
	event.ArgsJSON = jsonBody(map[string]any{"patchText": patch})
	return true
}

func describeTask(event *Event, call toolCall, fields map[string]any) (bool, error) {
	if argString(fields, "prompt") == "" {
		return false, nil
	}
	var err error
	event.ArgsJSON, err = call.argsJSON()
	return err == nil, err
}

func (p *plugin) describeOther(ctx context.Context, event *Event, call toolCall, fields map[string]any, identity who) (bool, error) {
	var err error
	if name := p.magusTool(call.Tool); name != "" {
		event.Tool, event.MCP = "mcp__magus__"+name, true
		event.ArgsJSON, err = call.argsJSON()
		return err == nil, err
	}
	file := argString(fields, "filePath")
	if call.Tool == "read" && file != "" {
		p.observe(ctx, identity, file)
	}
	event.ArgsJSON, err = fileArgs(call, fields, file)
	return err == nil, err
}

// fileArgs is the call's args, with file_path added when the tool names a file.
func fileArgs(call toolCall, fields map[string]any, file string) (string, error) {
	if file == "" {
		return call.argsJSON()
	}
	if fields == nil {
		return "", errors.New("harness plugin: tool args are not an object")
	}
	fields["file_path"] = file
	return jsonBody(fields), nil
}

func (p *plugin) permissionAsk(ctx context.Context, request permissionRequest) (string, error) {
	if request.Type != "bash" || !p.asksBefore(request.Command) {
		return "", nil
	}
	identity := p.whoIs(ctx, request.Session)
	result, err := p.runtime.Dispatch(ctx, Event{Kind: "permission", Tool: "bash", Command: request.Command, RootSessionID: identity.session, AgentID: identity.agent, Cwd: p.directory})
	return result.Status, err
}

// toolAfter is the notes a finished call adds to its output. The notes come
// back with an error too, so the output carries what was already taken.
func (p *plugin) toolAfter(ctx context.Context, call toolCall, out toolResult) ([]noteEdit, error) {
	edits := append(p.heldNotes(out.Notable), p.buzzCheckNotes(ctx, call)...)
	advisory, err := p.runtime.Dispatch(ctx, Event{Kind: "take", SessionID: call.Session, CallID: call.CallID})
	if err != nil {
		return edits, err
	}
	if advisory.Context != "" {
		edits = append(edits, noteEdit{Text: "[magus guard] " + advisory.Context, Advisory: true, MCP: advisory.MCP})
	}
	if call.Tool != "task" || out.Child == "" {
		return edits, nil
	}
	return p.afterTask(ctx, call, out, edits)
}

// heldNotes takes the brief and disk notes when the output can carry them.
func (p *plugin) heldNotes(notable bool) []noteEdit {
	p.mu.Lock()
	defer p.mu.Unlock()
	var edits []noteEdit
	if notable && p.briefNote != "" {
		edits, p.briefNote = append(edits, noteEdit{Text: p.briefNote}), ""
	}
	if notable && p.diskNote != "" {
		edits, p.diskNote = append(edits, noteEdit{Text: "[magus disk] " + p.diskNote}), ""
	}
	return edits
}

func (p *plugin) buzzCheckNotes(ctx context.Context, call toolCall) []noteEdit {
	if !slices.Contains(editTools, call.Tool) {
		return nil
	}
	if failed := p.buzzCheck(ctx, call.Tool, call.fields()); failed != "" {
		return []noteEdit{{Text: "[magus buzz check] " + failed}}
	}
	return nil
}

func (p *plugin) afterTask(ctx context.Context, call toolCall, out toolResult, edits []noteEdit) ([]noteEdit, error) {
	p.setParent(out.Child, call.Session)
	identity := p.whoIs(ctx, call.Session)
	var data any
	if err := json.Unmarshal(call.Args, &data); err != nil {
		return edits, err
	}
	p.judge(ctx, shellArgs(), p.envelope("PostToolUse", identity, call.Tool, data, map[string]any{"agent_id": out.Child}))
	if out.Background {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.background[out.Child] = false
		return edits, nil
	}
	if reason := p.guardSubagentStop(ctx, identity, out.Child, false); reason != "" {
		edits = append(edits, noteEdit{Text: "[magus guard] " + reason + "\n\nResume the subagent with task_id " + out.Child + "."})
	}
	return edits, nil
}

func (p *plugin) chatMessage(ctx context.Context, id string, parts []messagePart) {
	if p.parentOf(ctx, id) != "" {
		return
	}
	message := strings.Join(typedTexts(parts), "\n\n")
	if strings.TrimSpace(message) == "" || p.echoesFollowUp(id, message) {
		return
	}
	p.runOnce(ctx, []string{"shell", "--message", "--agent-name", agentName, "--session", id}, message)
}

func (p *plugin) event(ctx context.Context, event sessionEvent) {
	switch event.Kind {
	case "session.created", "session.updated":
		p.setParent(event.ID, event.Parent)
		if event.Kind == "session.created" && event.Parent == "" {
			p.holdBrief(ctx)
		}
	case "session.compacted":
		if p.parentOf(ctx, event.Session) == "" {
			p.holdBrief(ctx)
		}
	case "session.idle":
		p.sessionIdle(ctx, event.Session)
	}
}

func (p *plugin) sessionIdle(ctx context.Context, id string) {
	if p.isBackground(id) {
		p.backgroundChildIdle(ctx, id)
		return
	}
	if p.parentOf(ctx, id) != "" {
		return
	}
	followed := p.hasFollowUp(id)
	p.runOnce(ctx, []string{"session", "checkpoint", "--agent-name", agentName}, "")
	p.sampleDisk(ctx)
	if !p.guardStop(ctx, id, followed) {
		p.checkReply(ctx, id)
	}
}
