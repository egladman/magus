//go:build js && wasm

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"syscall/js"
	"time"

	"github.com/egladman/magus/internal/interp/bindings/opencode"
)

const (
	reclaimDiskScript = ".opencode/magus/runtime/magus-reclaim-disk.buzz"
	buzzCheckScript   = ".opencode/magus/runtime/magus-buzz-check.buzz"
	observeScript     = ".opencode/magus/runtime/magus-observe.buzz"
	magusFix          = "Install magus (https://eli.gladman.cc/magus/guides/setup/), build it from source (https://eli.gladman.cc/magus/guides/setup/build-from-source/), or set __MAGUS_BIN to its path."
)

var unsafePrompt = regexp.MustCompile("[;&|`$()<>\\\\\n]")
var unsafeName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

type who struct{ session, agent string }
type plugin struct {
	client              js.Value
	directory, binary   string
	runtime             *opencode.Runtime
	mu                  sync.Mutex
	parents             map[string]string
	followUps           map[string]string
	background          map[string]bool
	config              js.Value
	diskNote, briefNote string
	callbacks           []js.Func
}

func property(value js.Value, key string) js.Value {
	if value.Type() != js.TypeObject && value.Type() != js.TypeFunction || value.IsNull() {
		return js.Undefined()
	}
	return value.Get(key)
}
func text(value js.Value) string {
	if value.Type() == js.TypeString {
		return value.String()
	}
	return ""
}
func argString(value js.Value, keys ...string) string {
	for _, key := range keys {
		if found := text(property(value, key)); found != "" {
			return found
		}
	}
	return ""
}
func array(value js.Value) bool { return js.Global().Get("Array").Call("isArray", value).Bool() }
func jsonString(value js.Value) (string, error) {
	encoded := js.Global().Get("JSON").Call("stringify", value)
	if encoded.IsUndefined() {
		return "", errors.New("opencode: value cannot be encoded as JSON")
	}
	return encoded.String(), nil
}
func jsonBody(value any) string { data, _ := json.Marshal(value); return string(data) }
func warn(message string)       { js.Global().Get("console").Call("warn", message) }
func env(name string) string {
	return text(property(property(js.Global().Get("process"), "env"), name))
}

func promptKey(command string) string {
	if unsafePrompt.MatchString(command) {
		return ""
	}
	for _, verb := range []string{"git push", "hg push", "sl push", "jj git push"} {
		if command == verb || strings.HasPrefix(command, verb+" ") {
			return verb
		}
	}
	// OpenCode's plain-prefix permission syntax separates words on literal spaces.
	var words []string
	for _, word := range strings.Split(command, " ") {
		if word != "" {
			words = append(words, word)
		}
	}
	if len(words) >= 3 && (words[0] == "magus" || words[0] == "./magus") && (words[1] == "run" || words[1] == "affected") && !strings.HasPrefix(words[2], "-") {
		return strings.Join(words[:3], " ")
	}
	return ""
}
func (p *plugin) asksBefore(command string) bool {
	key := promptKey(command)
	if key == "" {
		return false
	}
	p.mu.Lock()
	config := p.config
	p.mu.Unlock()
	patterns := property(property(config, "permission"), "bash")
	return text(property(patterns, key)) == "ask" && text(property(patterns, key+" *")) == "ask"
}
func (p *plugin) magusTool(tool string) string {
	p.mu.Lock()
	config := p.config
	p.mu.Unlock()
	mcp := property(config, "mcp")
	if mcp.IsUndefined() || mcp.IsNull() || mcp.Type() != js.TypeObject {
		return ""
	}
	keys := js.Global().Get("Object").Call("keys", mcp)
	for i := 0; i < keys.Length(); i++ {
		key := keys.Index(i).String()
		command := property(property(mcp, key), "command")
		match := false
		if array(command) {
			binary, server := false, false
			for j := 0; j < command.Length(); j++ {
				word := command.Index(j).String()
				binary = binary || path.Base(word) == "magus"
				server = server || word == "mcp"
			}
			match = binary && server
		} else {
			match = key == "magus"
		}
		prefix := unsafeName.ReplaceAllString(key, "_") + "_"
		if match && strings.HasPrefix(tool, prefix) && len(tool) > len(prefix) {
			return tool[len(prefix):]
		}
	}
	return ""
}
func exists(ctx context.Context, file string) bool {
	var found bool
	_, err := javascript(func() (any, error) {
		pending := js.Global().Get("Bun").Call("file", file).Call("exists")
		value, err := await(ctx, pending, func() {})
		if err == nil {
			found = value.Bool()
		}
		return nil, err
	})
	return err == nil && found
}
func nearest(ctx context.Context, name, directory string) string {
	for at := directory; ; at = path.Dir(at) {
		if exists(ctx, path.Join(at, name)) {
			return at
		}
		if path.Dir(at) == at {
			return ""
		}
	}
}
func workspaceBinary(ctx context.Context) string {
	directory := js.Global().Get("process").Call("cwd").String()
	root := nearest(ctx, "magus.yaml", directory)
	if root == "" {
		root = nearest(ctx, "magusfile.buzz", directory)
	}
	if root != "" && exists(ctx, path.Join(root, "magus")) {
		return path.Join(root, "magus")
	}
	return "magus"
}
func (p *plugin) sdk(ctx context.Context, pending js.Value) (js.Value, error) {
	return await(ctx, pending, func() {})
}
func (p *plugin) parentOf(ctx context.Context, id string) string {
	p.mu.Lock()
	parent, known := p.parents[id]
	p.mu.Unlock()
	if known {
		return parent
	}
	_, _ = javascript(func() (any, error) {
		found, err := p.sdk(ctx, p.client.Get("session").Call("get", map[string]any{"path": map[string]any{"id": id}}))
		if err == nil {
			parent = text(property(property(found, "data"), "parentID"))
		}
		return nil, err
	})
	p.mu.Lock()
	// An SDK request may overlap session.created; prefer the actual event.
	if current, ok := p.parents[id]; ok {
		parent = current
	} else {
		p.parents[id] = parent
	}
	p.mu.Unlock()
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
func inThread(args []string, identity who) []string {
	result := append([]string(nil), args...)
	if identity.session != "" {
		result = append(result, "--session", identity.session)
	}
	if identity.agent != "" {
		result = append(result, "--agent", identity.agent)
	}
	return result
}
func shellArgs() []string {
	return []string{"shell", "--agent-name", "opencode", "--renders-ask", "-o", "json"}
}
func (p *plugin) run(ctx context.Context, args []string, input string) (opencode.Capture, error) {
	return invoke(ctx, p.directory, p.binary, opencode.Invocation{Args: args, Input: input})
}
func (p *plugin) runOnce(ctx context.Context, args []string, input string) string {
	capture, err := p.run(ctx, args, input)
	if err != nil {
		return ""
	}
	return capture.Stdout
}
func (p *plugin) judge(ctx context.Context, args []string, input string) opencode.Verdict {
	return opencode.Judge(ctx, func(ctx context.Context, inv opencode.Invocation) (opencode.Capture, error) {
		return invoke(ctx, p.directory, p.binary, inv)
	}, opencode.Invocation{Args: args, Input: input})
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
func (p *plugin) prompt(ctx context.Context, id, message string) error {
	_, err := javascript(func() (any, error) {
		return p.sdk(ctx, p.client.Get("session").Call("promptAsync", map[string]any{"path": map[string]any{"id": id}, "body": map[string]any{"parts": []any{map[string]any{"type": "text", "text": message}}}}))
	})
	return err
}
func (p *plugin) notify(ctx context.Context, message string) {
	toastFailed, logFailed := false, false
	_, toastErr := javascript(func() (any, error) {
		value, err := p.sdk(ctx, p.client.Get("tui").Call("showToast", map[string]any{"body": map[string]any{"title": "magus", "message": message, "variant": "warning"}}))
		toastFailed = err != nil || !property(value, "error").IsUndefined()
		return nil, err
	})
	_, logErr := javascript(func() (any, error) {
		value, err := p.sdk(ctx, p.client.Get("app").Call("log", map[string]any{"body": map[string]any{"service": "magus", "level": "warn", "message": message}}))
		logFailed = err != nil || !property(value, "error").IsUndefined()
		return nil, err
	})
	if (toastFailed || toastErr != nil) && (logFailed || logErr != nil) {
		warn("[magus] " + message)
	}
}
func (p *plugin) runGlue(ctx context.Context, script string, event any) string {
	return p.runOnce(ctx, []string{"buzz", "-C", p.directory, "-s", script, "--", "--agent-name", "opencode"}, jsonBody(event))
}
func (p *plugin) observe(ctx context.Context, identity who, file string) {
	p.runGlue(ctx, observeScript, map[string]any{"session_id": identity.session, "tool_input": map[string]any{"file_path": file}, "cwd": p.directory})
}
func (p *plugin) buzzCheck(ctx context.Context, tool string, args js.Value) string {
	file, patch := argString(args, "filePath", "file_path", "path"), argString(args, "patchText")
	if file != "" && !strings.HasSuffix(file, ".buzz") || file == "" && !strings.Contains(patch, ".buzz") {
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
	if len(brief) > 16384 {
		warn(fmt.Sprintf("[magus guard] session --brief was %d bytes, over 16384; dropping it", len(brief)))
		return
	}
	p.mu.Lock()
	p.briefNote = brief
	p.mu.Unlock()
}
func appendNote(result js.Value, note string) bool {
	content := property(result, "content")
	if array(content) {
		content.Call("push", map[string]any{"type": "text", "text": note})
		return true
	}
	if output := property(result, "output"); output.Type() == js.TypeString {
		result.Set("output", output.String()+"\n\n"+note)
		return true
	}
	return false
}
func (p *plugin) lastReply(ctx context.Context, id string) string {
	var reply string
	_, _ = javascript(func() (any, error) {
		listed, err := p.sdk(ctx, p.client.Get("session").Call("messages", map[string]any{"path": map[string]any{"id": id}}))
		if err != nil {
			return nil, err
		}
		rows := property(listed, "data")
		if !array(rows) {
			return nil, nil
		}
		for i := rows.Length() - 1; i >= 0; i-- {
			row := rows.Index(i)
			role := text(property(property(row, "info"), "role"))
			if role == "user" {
				return nil, nil
			}
			if role != "assistant" {
				continue
			}
			parts := property(row, "parts")
			var texts []string
			if array(parts) {
				for j := 0; j < parts.Length(); j++ {
					part := parts.Index(j)
					if text(property(part, "type")) != "text" || property(part, "synthetic").Truthy() || property(part, "ignored").Truthy() {
						continue
					}
					value := text(property(part, "text"))
					if strings.TrimSpace(value) != "" {
						texts = append(texts, value)
					}
				}
			}
			if len(texts) > 0 {
				reply = strings.Join(texts, "\n\n")
				return nil, nil
			}
		}
		return nil, nil
	})
	return reply
}
func (p *plugin) checkReply(ctx context.Context, id string) {
	p.mu.Lock()
	_, followed := p.followUps[id]
	delete(p.followUps, id)
	p.mu.Unlock()
	if followed {
		return
	}
	script := env("__PROOFREAD_REPLY_BUZZ")
	if script == "" || !exists(ctx, script) {
		return
	}
	reply := p.lastReply(ctx, id)
	if reply == "" {
		return
	}
	ran, err := p.run(ctx, []string{"buzz", "-s", script, "--", "--agent-name", "opencode"}, jsonBody(map[string]any{"last_assistant_message": reply, "stop_hook_active": false}))
	if err != nil {
		return
	}
	if notice := strings.TrimSpace(ran.Stderr); notice != "" {
		p.notify(ctx, notice)
	}
	var response struct {
		Prompt string `json:"prompt"`
	}
	if json.Unmarshal([]byte(ran.Stdout), &response) != nil || response.Prompt == "" {
		return
	}
	p.mu.Lock()
	p.followUps[id] = response.Prompt
	p.mu.Unlock()
	if err := p.prompt(ctx, id, response.Prompt); err != nil {
		p.mu.Lock()
		delete(p.followUps, id)
		p.mu.Unlock()
		warn("[magus guard] could not send the reply check back: " + err.Error())
	}
}
func (p *plugin) sampleDisk(ctx context.Context) {
	said := p.runGlue(ctx, reclaimDiskScript, map[string]any{"cwd": p.directory})
	if note := strings.TrimSpace(said); note != "" {
		p.mu.Lock()
		p.diskNote = note
		p.mu.Unlock()
	}
}
func (p *plugin) guardStop(ctx context.Context, id string, followed bool) bool {
	body := jsonBody(map[string]any{"hook_event_name": "Stop", "session_id": id, "stop_hook_active": followed, "cwd": p.directory})
	verdict := p.judge(ctx, shellArgs(), body)
	if verdict.Decision == "unjudged" {
		warn("[magus guard] " + verdict.Why + "\n" + magusFix)
		return false
	}
	if verdict.Decision == "advise" {
		warn("[magus guard] " + verdict.Context)
		return false
	}
	if verdict.Decision != "deny" {
		return false
	}
	message := "[magus guard] " + verdict.Reason
	p.mu.Lock()
	p.followUps[id] = message
	p.mu.Unlock()
	if err := p.prompt(ctx, id, message); err != nil {
		p.mu.Lock()
		delete(p.followUps, id)
		p.mu.Unlock()
		warn("[magus guard] could not send the stop refusal back: " + err.Error())
		return false
	}
	return true
}
func (p *plugin) guardSubagentStop(ctx context.Context, identity who, child string, followed bool) string {
	body := jsonBody(map[string]any{"hook_event_name": "SubagentStop", "session_id": identity.session, "agent_id": child, "stop_hook_active": followed, "cwd": p.directory})
	verdict := p.judge(ctx, shellArgs(), body)
	if verdict.Decision == "unjudged" {
		warn("[magus guard] " + verdict.Why + "\n" + magusFix)
	}
	if verdict.Decision == "advise" {
		warn("[magus guard] " + verdict.Context)
	}
	if verdict.Decision == "deny" {
		return verdict.Reason
	}
	return ""
}
func (p *plugin) backgroundChildIdle(ctx context.Context, child string) {
	p.mu.Lock()
	followed := p.background[child]
	p.mu.Unlock()
	reason := p.guardSubagentStop(ctx, p.whoIs(ctx, child), child, followed)
	p.mu.Lock()
	if reason == "" {
		delete(p.background, child)
		p.mu.Unlock()
		return
	}
	p.background[child] = true
	p.mu.Unlock()
	if err := p.prompt(ctx, child, "[magus guard] "+reason); err != nil {
		warn("[magus guard] could not send the subagent stop refusal back: " + err.Error())
	}
}
func decoded(value js.Value) (any, error) {
	encoded, err := jsonString(value)
	if err != nil {
		return nil, err
	}
	var data any
	err = json.Unmarshal([]byte(encoded), &data)
	return data, err
}
func (p *plugin) toolBefore(ctx context.Context, input, output js.Value) error {
	session, tool := text(property(input, "sessionID")), text(property(input, "tool"))
	identity := p.whoIs(ctx, session)
	args := property(output, "args")
	event := opencode.Event{Kind: "before", Tool: tool, SessionID: session, CallID: text(property(input, "callID")), RootSessionID: identity.session, AgentID: identity.agent, Cwd: p.directory}
	var err error
	switch tool {
	case "bash":
		event.Command = argString(args, "command")
		if event.Command == "" {
			return nil
		}
		event.Promptable = p.asksBefore(event.Command)
	case "edit", "write":
		event.Path = argString(args, "filePath", "file_path", "path")
		if event.Path == "" {
			return nil
		}
	case "apply_patch":
		patch := argString(args, "patchText")
		if patch == "" {
			return nil
		}
		event.ArgsJSON = jsonBody(map[string]any{"patchText": patch})
	case "task":
		if argString(args, "prompt") == "" {
			return nil
		}
		event.ArgsJSON, err = jsonString(args)
	default:
		if name := p.magusTool(tool); name != "" {
			event.Tool = "mcp__magus__" + name
			event.MCP = true
			event.ArgsJSON, err = jsonString(args)
		} else {
			file := argString(args, "filePath")
			if tool == "read" && file != "" {
				p.observe(ctx, identity, file)
			}
			if file != "" {
				data, decodeErr := decoded(args)
				if decodeErr != nil {
					return decodeErr
				}
				fields, ok := data.(map[string]any)
				if !ok {
					return errors.New("opencode: tool args are not an object")
				}
				fields["file_path"] = file
				event.ArgsJSON = jsonBody(fields)
			} else {
				event.ArgsJSON, err = jsonString(args)
			}
		}
	}
	if err != nil {
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
func (p *plugin) permissionAsk(ctx context.Context, input, output js.Value) error {
	if text(property(input, "type")) != "bash" {
		return nil
	}
	command := property(property(input, "metadata"), "command")
	if command.Type() != js.TypeString {
		command = property(input, "title")
	}
	if !p.asksBefore(text(command)) {
		return nil
	}
	identity := p.whoIs(ctx, text(property(input, "sessionID")))
	result, err := p.runtime.Dispatch(ctx, opencode.Event{Kind: "permission", Tool: "bash", Command: text(command), RootSessionID: identity.session, AgentID: identity.agent, Cwd: p.directory})
	if err != nil {
		return err
	}
	if result.Status != "" {
		output.Set("status", result.Status)
	}
	return nil
}
func (p *plugin) toolAfter(ctx context.Context, input, output js.Value) error {
	func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.briefNote != "" && appendNote(output, p.briefNote) {
			p.briefNote = ""
		}
		if p.diskNote != "" && appendNote(output, "[magus disk] "+p.diskNote) {
			p.diskNote = ""
		}
	}()
	tool := text(property(input, "tool"))
	if tool == "edit" || tool == "write" || tool == "apply_patch" {
		if failed := p.buzzCheck(ctx, tool, property(input, "args")); failed != "" {
			appendNote(output, "[magus buzz check] "+failed)
		}
	}
	advisory, err := p.runtime.Dispatch(ctx, opencode.Event{Kind: "take", SessionID: text(property(input, "sessionID")), CallID: text(property(input, "callID"))})
	if err != nil {
		return err
	}
	if advisory.Context != "" {
		note := "[magus guard] " + advisory.Context
		content := property(output, "content")
		if advisory.MCP && array(content) {
			content.Call("push", map[string]any{"type": "text", "text": note})
		} else if result := property(output, "output"); result.Type() == js.TypeString {
			output.Set("output", result.String()+"\n\n"+note)
		} else {
			output.Set("content", []any{map[string]any{"type": "text", "text": note}})
		}
	}
	if tool != "task" {
		return nil
	}
	metadata := property(output, "metadata")
	child := text(property(metadata, "sessionId"))
	if child == "" {
		return nil
	}
	session := text(property(input, "sessionID"))
	p.mu.Lock()
	p.parents[child] = session
	p.mu.Unlock()
	identity := p.whoIs(ctx, session)
	data, err := decoded(property(input, "args"))
	if err != nil {
		return err
	}
	p.judge(ctx, shellArgs(), p.envelope("PostToolUse", identity, tool, data, map[string]any{"agent_id": child}))
	if background := property(metadata, "background"); background.Type() == js.TypeBoolean && background.Bool() {
		p.mu.Lock()
		p.background[child] = false
		p.mu.Unlock()
		return nil
	}
	if reason := p.guardSubagentStop(ctx, identity, child, false); reason != "" {
		appendNote(output, "[magus guard] "+reason+"\n\nResume the subagent with task_id "+child+".")
	}
	return nil
}
func (p *plugin) chatMessage(ctx context.Context, input, output js.Value) error {
	id := text(property(input, "sessionID"))
	if p.parentOf(ctx, id) != "" {
		return nil
	}
	parts := property(output, "parts")
	var texts []string
	if array(parts) {
		for i := 0; i < parts.Length(); i++ {
			part := parts.Index(i)
			if text(property(part, "type")) == "text" && !property(part, "synthetic").Truthy() {
				texts = append(texts, text(property(part, "text")))
			}
		}
	}
	message := strings.Join(texts, "\n\n")
	if strings.TrimSpace(message) == "" {
		return nil
	}
	p.mu.Lock()
	if p.followUps[id] == message {
		p.mu.Unlock()
		return nil
	}
	delete(p.followUps, id)
	p.mu.Unlock()
	p.runOnce(ctx, []string{"shell", "--message", "--agent-name", "opencode", "--session", id}, message)
	return nil
}
func (p *plugin) event(ctx context.Context, input js.Value) error {
	event := property(input, "event")
	kind := text(property(event, "type"))
	properties := property(event, "properties")
	if kind == "session.created" || kind == "session.updated" {
		info := property(properties, "info")
		parent, id := text(property(info, "parentID")), text(property(info, "id"))
		p.mu.Lock()
		p.parents[id] = parent
		p.mu.Unlock()
		if kind == "session.created" && parent == "" {
			p.holdBrief(ctx)
		}
		return nil
	}
	id := text(property(properties, "sessionID"))
	if kind == "session.compacted" {
		if p.parentOf(ctx, id) == "" {
			p.holdBrief(ctx)
		}
		return nil
	}
	if kind != "session.idle" {
		return nil
	}
	p.mu.Lock()
	_, background := p.background[id]
	p.mu.Unlock()
	if background {
		p.backgroundChildIdle(ctx, id)
		return nil
	}
	if p.parentOf(ctx, id) != "" {
		return nil
	}
	p.mu.Lock()
	_, followed := p.followUps[id]
	p.mu.Unlock()
	p.runOnce(ctx, []string{"session", "checkpoint", "--agent-name", "opencode"}, "")
	p.sampleDisk(ctx)
	if !p.guardStop(ctx, id, followed) {
		p.checkReply(ctx, id)
	}
	return nil
}

func createPlugin(input js.Value) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	directory := text(property(input, "directory"))
	client := property(input, "client")
	if directory == "" || client.IsUndefined() || client.IsNull() {
		return nil, errors.New("opencode: plugin requires directory and client")
	}
	binary := env("__MAGUS_BIN")
	if binary == "" {
		binary = workspaceBinary(ctx)
	}
	p := &plugin{directory: directory, binary: binary, client: client, parents: make(map[string]string), followUps: make(map[string]string), background: make(map[string]bool), config: js.Null()}
	runtime, err := opencode.Open(context.Background(), pluginSource, func(ctx context.Context, input opencode.Invocation) (opencode.Capture, error) {
		return invoke(ctx, directory, binary, input)
	})
	if err != nil {
		return nil, err
	}
	p.runtime = runtime
	hook := func(handler func(context.Context, js.Value, js.Value) error) js.Func {
		callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
			input, output := js.Undefined(), js.Undefined()
			if len(args) > 0 {
				input = args[0]
			}
			if len(args) > 1 {
				output = args[1]
			}
			return promise(func() (any, error) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				return nil, handler(ctx, input, output)
			})
		})
		p.callbacks = append(p.callbacks, callback)
		return callback
	}
	hooks := map[string]any{
		"tool.execute.before": hook(p.toolBefore),
		"tool.execute.after":  hook(p.toolAfter),
		"permission.ask":      hook(p.permissionAsk),
		"chat.message":        hook(p.chatMessage),
		"config": hook(func(_ context.Context, input, _ js.Value) error {
			p.mu.Lock()
			p.config = input
			p.mu.Unlock()
			return nil
		}),
		"shell.env": hook(func(_ context.Context, _, output js.Value) error {
			environment := property(output, "env")
			if property(property(js.Global().Get("process"), "env"), "MAGUS_LOG_SILENT").IsUndefined() && property(environment, "MAGUS_LOG_SILENT").IsUndefined() {
				environment.Set("MAGUS_LOG_SILENT", "true")
			}
			return nil
		}),
		"event": hook(func(ctx context.Context, input, _ js.Value) error { return p.event(ctx, input) }),
	}
	return hooks, nil
}
