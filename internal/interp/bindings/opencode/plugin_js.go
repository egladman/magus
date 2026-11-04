//go:build js && wasm

package opencode //nolint:hostagnostic // the package is named for the product it binds

import (
	"context"
	"errors"
	"syscall/js"
	"time"

	"github.com/egladman/magus/internal/json"
)

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

func array(value js.Value) bool { return js.Global().Get("Array").Call("isArray", value).Bool() }

func jsonString(value js.Value) (string, error) {
	encoded := js.Global().Get("JSON").Call("stringify", value)
	if encoded.IsUndefined() {
		return "", errors.New("harness plugin: value cannot be encoded as JSON")
	}
	return encoded.String(), nil
}

const hookTimeout = 30 * time.Second

func consoleWarn(message string) { js.Global().Get("console").Call("warn", message) }

func objectKeys(value js.Value) []string {
	if value.Type() != js.TypeObject || value.IsNull() {
		return nil
	}
	keys := js.Global().Get("Object").Call("keys", value)
	out := make([]string, keys.Length())
	for i := range out {
		out[i] = keys.Index(i).String()
	}
	return out
}

// jsClient is the harness SDK client the plugin context hands over.
type jsClient struct{ client js.Value }

func (c jsClient) call(ctx context.Context, pending func() js.Value) (js.Value, error) {
	var value js.Value
	_, err := javascript(func() (any, error) {
		var err error
		value, err = await(ctx, pending(), func() {})
		return nil, err
	})
	return value, err
}

func pathID(id string) map[string]any { return map[string]any{"path": map[string]any{"id": id}} }

func (c jsClient) sessionParent(ctx context.Context, id string) (string, error) {
	found, err := c.call(ctx, func() js.Value { return c.client.Get("session").Call("get", pathID(id)) })
	if err != nil {
		return "", err
	}
	return text(property(property(found, "data"), "parentID")), nil
}

func (c jsClient) sessionMessages(ctx context.Context, id string) ([]sessionMessage, error) {
	listed, err := c.call(ctx, func() js.Value { return c.client.Get("session").Call("messages", pathID(id)) })
	if err != nil {
		return nil, err
	}
	encoded, err := jsonString(property(listed, "data"))
	if err != nil {
		return nil, err
	}
	var messages []sessionMessage
	if err := json.Unmarshal([]byte(encoded), &messages); err != nil {
		return nil, err
	}
	return messages, nil
}

func (c jsClient) prompt(ctx context.Context, id, message string) error {
	body := map[string]any{"parts": []any{map[string]any{"type": "text", "text": message}}}
	_, err := c.call(ctx, func() js.Value {
		return c.client.Get("session").Call("promptAsync", map[string]any{"path": map[string]any{"id": id}, "body": body})
	})
	return err
}

// delivered reports whether an SDK call settled without an error member.
func (c jsClient) delivered(ctx context.Context, pending func() js.Value) bool {
	value, err := c.call(ctx, pending)
	return err == nil && property(value, "error").IsUndefined()
}

func (c jsClient) notify(ctx context.Context, message string) {
	toast := c.delivered(ctx, func() js.Value {
		return c.client.Get("tui").Call("showToast", map[string]any{"body": map[string]any{"title": "magus", "message": message, "variant": "warning"}})
	})
	logged := c.delivered(ctx, func() js.Value {
		return c.client.Get("app").Call("log", map[string]any{"body": map[string]any{"service": "magus", "level": "warn", "message": message}})
	})
	if !toast && !logged {
		consoleWarn("[magus] " + message)
	}
}

func (c jsClient) warn(message string) { consoleWarn(message) }

func (c jsClient) exists(ctx context.Context, file string) bool {
	value, err := c.call(ctx, func() js.Value { return js.Global().Get("Bun").Call("file", file).Call("exists") })
	return err == nil && value.Bool()
}

func (c jsClient) env(name string) string {
	return text(property(property(js.Global().Get("process"), "env"), name))
}

func decodeToolCall(input, args js.Value) toolCall {
	call := toolCall{Session: text(property(input, "sessionID")), Tool: text(property(input, "tool")), CallID: text(property(input, "callID"))}
	if encoded, err := jsonString(args); err == nil {
		call.Args = []byte(encoded)
	}
	return call
}

func decodeToolResult(output js.Value) toolResult {
	metadata := property(output, "metadata")
	background := property(metadata, "background")
	return toolResult{
		Notable:    array(property(output, "content")) || property(output, "output").Type() == js.TypeString,
		Child:      text(property(metadata, "sessionId")),
		Background: background.Type() == js.TypeBoolean && background.Bool(),
	}
}

func decodePermission(input js.Value) permissionRequest {
	command := property(property(input, "metadata"), "command")
	if command.Type() != js.TypeString {
		command = property(input, "title")
	}
	return permissionRequest{Type: text(property(input, "type")), Command: text(command), Session: text(property(input, "sessionID"))}
}

func decodeSessionEvent(input js.Value) sessionEvent {
	event := property(input, "event")
	properties := property(event, "properties")
	info := property(properties, "info")
	return sessionEvent{Kind: text(property(event, "type")), ID: text(property(info, "id")),
		Parent: text(property(info, "parentID")), Session: text(property(properties, "sessionID"))}
}

// decodeMessageParts decodes a chat message's parts, or none when they are not
// an array of objects.
func decodeMessageParts(parts js.Value) []messagePart {
	encoded, err := jsonString(parts)
	if err != nil {
		return nil
	}
	var decoded []messagePart
	if json.Unmarshal([]byte(encoded), &decoded) != nil {
		return nil
	}
	return decoded
}

func decodeConfig(config js.Value) pluginConfig {
	bash := property(property(config, "permission"), "bash")
	patterns := map[string]string{}
	for _, key := range objectKeys(bash) {
		patterns[key] = text(bash.Get(key))
	}
	return pluginConfig{bash: patterns, servers: decodeMCPServers(property(config, "mcp"))}
}

func decodeMCPServers(mcp js.Value) []mcpServer {
	var servers []mcpServer
	for _, key := range objectKeys(mcp) {
		command := property(mcp.Get(key), "command")
		server := mcpServer{key: key, listed: array(command)}
		for j := 0; server.listed && j < command.Length(); j++ {
			server.command = append(server.command, command.Index(j).String())
		}
		servers = append(servers, server)
	}
	return servers
}

func appendNote(output js.Value, note string) {
	if content := property(output, "content"); array(content) {
		content.Call("push", map[string]any{"type": "text", "text": note})
		return
	}
	if result := property(output, "output"); result.Type() == js.TypeString {
		output.Set("output", result.String()+"\n\n"+note)
	}
}

// placeAdvisory puts an MCP advisory in the content array and any other in
// the output string, else starts a content array.
func placeAdvisory(output js.Value, edit noteEdit) {
	content, result := property(output, "content"), property(output, "output")
	switch {
	case edit.MCP && array(content):
		content.Call("push", map[string]any{"type": "text", "text": edit.Text})
	case result.Type() == js.TypeString:
		output.Set("output", result.String()+"\n\n"+edit.Text)
	default:
		output.Set("content", []any{map[string]any{"type": "text", "text": edit.Text}})
	}
}

func applyNotes(output js.Value, edits []noteEdit) {
	for _, edit := range edits {
		if edit.Advisory {
			placeAdvisory(output, edit)
			continue
		}
		appendNote(output, edit.Text)
	}
}

func shellEnv(_ context.Context, _, output js.Value) error {
	environment := property(output, "env")
	if property(property(js.Global().Get("process"), "env"), "MAGUS_LOG_SILENT").IsUndefined() && property(environment, "MAGUS_LOG_SILENT").IsUndefined() {
		environment.Set("MAGUS_LOG_SILENT", "true")
	}
	return nil
}

// hook adapts handler to a harness hook: a promise per call, each with its
// own deadline.
func hook(handler func(context.Context, js.Value, js.Value) error) js.Func {
	return js.FuncOf(func(_ js.Value, args []js.Value) any {
		input, output := js.Undefined(), js.Undefined()
		if len(args) > 0 {
			input = args[0]
		}
		if len(args) > 1 {
			output = args[1]
		}
		return promise(func() (any, error) {
			ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
			defer cancel()
			return nil, handler(ctx, input, output)
		})
	})
}

func hooks(p *plugin) map[string]any {
	return map[string]any{
		"tool.execute.before": hook(func(ctx context.Context, input, output js.Value) error {
			return p.toolBefore(ctx, decodeToolCall(input, property(output, "args")))
		}),
		"tool.execute.after": hook(func(ctx context.Context, input, output js.Value) error {
			edits, err := p.toolAfter(ctx, decodeToolCall(input, property(input, "args")), decodeToolResult(output))
			applyNotes(output, edits)
			return err
		}),
		"permission.ask": hook(func(ctx context.Context, input, output js.Value) error {
			status, err := p.permissionAsk(ctx, decodePermission(input))
			if status != "" {
				output.Set("status", status)
			}
			return err
		}),
		"chat.message": hook(func(ctx context.Context, input, output js.Value) error {
			p.chatMessage(ctx, text(property(input, "sessionID")), decodeMessageParts(property(output, "parts")))
			return nil
		}),
		"config":    hook(func(_ context.Context, input, _ js.Value) error { p.configure(decodeConfig(input)); return nil }),
		"shell.env": hook(shellEnv),
		"event": hook(func(ctx context.Context, input, _ js.Value) error {
			p.event(ctx, decodeSessionEvent(input))
			return nil
		}),
	}
}

func pluginBinary(ctx context.Context, client jsClient) string {
	if binary := client.env("__MAGUS_BIN"); binary != "" {
		return binary
	}
	return workspaceBinary(ctx, js.Global().Get("process").Call("cwd").String(), client.exists)
}

func createPlugin(input js.Value) (any, error) {
	directory, client := text(property(input, "directory")), property(input, "client")
	if directory == "" || client.IsUndefined() || client.IsNull() {
		return nil, errors.New("harness plugin: plugin requires directory and client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), hookTimeout)
	defer cancel()
	sdk := jsClient{client: client}
	invoke := spawner(directory, pluginBinary(ctx, sdk))
	runtime, err := openRuntime(context.Background(), pluginSource, invoke)
	if err != nil {
		return nil, err
	}
	return hooks(newPlugin(sdk, invoke, directory, runtime)), nil
}
