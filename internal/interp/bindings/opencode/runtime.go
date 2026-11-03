package opencode

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/json"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// The harness spell embeds ./wasm built as js/wasm, so the outputs are
// committed, unstamped so an unchanged source keeps its bytes.
//
//go:generate env GOEXPERIMENT=jsonv2 GOOS=js GOARCH=wasm go build -buildvcs=false -trimpath "-ldflags=-s -w" -o ../../../../spells/harness/opencode/gen/magus-opencode.wasm ./wasm
//go:generate cp $GOROOT/lib/wasm/wasm_exec.js ../../../../spells/harness/opencode/gen/wasm_exec.js

// moduleName is the Buzz module plugin.buzz imports.
const moduleName = "opencode"

// declarations is the Buzz module the runtime session provides.
const declarations = `export extern fun judge(event: {str: str}, mode: str, payload: str) > {str: str};`

// pluginSource is the Buzz program that routes harness events.
//
//go:embed plugin.buzz
var pluginSource string

type magusCall struct {
	Args  []string `json:"args"`
	Input string   `json:"input"`
}
type processOutput struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}
type invoker func(context.Context, magusCall) (processOutput, error)

type guardVerdict struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason,omitempty"`
	Context       string `json:"context,omitempty"`
	Why           string `json:"why,omitempty"`
}

type hookEvent struct {
	Kind          string        `json:"kind"`
	Tool          string        `json:"tool,omitempty"`
	SessionID     string        `json:"sessionID,omitempty"`
	CallID        string        `json:"callID,omitempty"`
	RootSessionID string        `json:"rootSessionID,omitempty"`
	AgentID       string        `json:"agentID,omitempty"`
	Cwd           string        `json:"cwd,omitempty"`
	Command       string        `json:"command,omitempty"`
	Path          string        `json:"path,omitempty"`
	ArgsJSON      string        `json:"argsJSON,omitempty"`
	PatchText     string        `json:"patchText,omitempty"`
	Promptable    bool          `json:"promptable,omitempty"`
	MCP           bool          `json:"mcp,omitempty"`
	Context       string        `json:"context,omitempty"`
	Verdict       *guardVerdict `json:"verdict,omitempty"`
}
type dispatchResult struct {
	Block   bool   `json:"block"`
	Reason  string `json:"reason"`
	Context string `json:"context"`
	Status  string `json:"status"`
	MCP     bool   `json:"mcp"`
}
type request struct {
	ctx   context.Context
	event hookEvent
	reply chan response
}
type response struct {
	result dispatchResult
	err    error
}

type buzzRuntime struct {
	requests chan request
	done     chan struct{}
	cancel   context.CancelFunc
}

// openRuntime gives the session one owning goroutine, including
// initialization and teardown. Calls waiting on native JavaScript promises
// never reenter the VM.
func openRuntime(ctx context.Context, source string, invoke invoker) (*buzzRuntime, error) {
	if invoke == nil {
		return nil, errors.New("harness runtime: missing native invocation capability")
	}
	lifetime, cancel := context.WithCancel(ctx)
	r := &buzzRuntime{requests: make(chan request), done: make(chan struct{}), cancel: cancel}
	ready := make(chan error, 1)
	go r.serve(lifetime, source, invoke, ready)
	select {
	case err := <-ready:
		if err != nil {
			cancel()
			<-r.done
			return nil, err
		}
		return r, nil
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
}

// module binds the Buzz module plugin.buzz imports.
func module(invoke invoker) buzz.Module {
	return buzz.Module{Name: moduleName, Bind: func(s *buzz.Session, _ buzz.ModuleEnv) error {
		s.DeclareModuleTypes(moduleName, declarations)
		s.SetModuleDecls(moduleName, declarations)
		mod := vm.NewMap()
		mod.MapSet("judge", vm.DirectValue(moduleName+".judge", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) != 3 {
				return vm.Null, errors.New("harness runtime: judge requires event, mode and payload")
			}
			return judge(callCtx, invoke, args[0], args[1].String(), args[2].String()), nil
		}))
		s.SetNativeModule(moduleName, mod)
		return nil
	}}
}

// newSession is the session plugin.buzz runs in; the caller owns Close.
func newSession(ctx context.Context, invoke invoker) (*buzz.Session, error) {
	sess := buzz.NewSession(ctx, buzz.WithoutFileImports(), buzz.WithoutFFI())
	if err := sess.Provide(buzz.ModuleEnv{Ctx: ctx}, module(invoke)); err != nil {
		_ = sess.Close()
		return nil, err
	}
	return sess, nil
}

func loadDispatch(ctx context.Context, sess *buzz.Session, source string) (vm.Value, error) {
	if err := sess.Exec(ctx, source); err != nil {
		return vm.Null, fmt.Errorf("harness runtime: load plugin.buzz: %w", err)
	}
	dispatch, ok := sess.Exports()["dispatch"]
	if !ok || !dispatch.IsFun() {
		return vm.Null, errors.New("harness runtime: plugin.buzz must export dispatch")
	}
	return dispatch, nil
}

func (r *buzzRuntime) serve(ctx context.Context, source string, invoke invoker, ready chan<- error) {
	defer close(r.done)
	sess, err := newSession(ctx, invoke)
	if err != nil {
		ready <- err
		return
	}
	defer sess.Close()
	dispatch, err := loadDispatch(ctx, sess, source)
	ready <- err
	if err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-r.requests:
			req.reply <- call(ctx, sess, dispatch, req)
		}
	}
}

// call runs one dispatch until the request or the runtime ends.
func call(lifetime context.Context, sess *buzz.Session, dispatch vm.Value, req request) response {
	callCtx, cancel := context.WithCancel(req.ctx)
	defer cancel()
	defer context.AfterFunc(lifetime, cancel)()
	if err := callCtx.Err(); err != nil {
		return response{err: err}
	}
	val, err := sess.CallValue(callCtx, dispatch, []vm.Value{eventValue(req.event)})
	if callCtx.Err() != nil {
		return response{err: callCtx.Err()}
	}
	if err != nil {
		return response{err: err}
	}
	result, err := decodeResult(val)
	return response{result, err}
}

func (r *buzzRuntime) Dispatch(ctx context.Context, event hookEvent) (dispatchResult, error) {
	req := request{ctx: ctx, event: event, reply: make(chan response, 1)}
	select {
	case <-ctx.Done():
		return dispatchResult{}, ctx.Err()
	case <-r.done:
		return dispatchResult{}, errors.New("harness runtime: runtime closed")
	case r.requests <- req:
	}
	select {
	case <-ctx.Done():
		return dispatchResult{}, ctx.Err()
	case <-r.done:
		return dispatchResult{}, errors.New("harness runtime: runtime closed")
	case res := <-req.reply:
		return res.result, res.err
	}
}
func (r *buzzRuntime) Close() error { r.cancel(); <-r.done; return nil }

func record(fields map[string]string) vm.Value {
	value := vm.NewMap()
	for key, field := range fields {
		value.MapSet(key, vm.StringValue(field))
	}
	return value
}
func field(value vm.Value, key string) string {
	v, _ := value.MapGet(key)
	if v.IsString() {
		return v.String()
	}
	return ""
}
func eventValue(e hookEvent) vm.Value {
	key, _ := json.Marshal([]string{e.SessionID, e.CallID})
	fields := map[string]string{"kind": e.Kind, "tool": e.Tool, "key": string(key), "session": e.RootSessionID,
		"agent": e.AgentID, "cwd": e.Cwd, "command": e.Command, "path": e.Path, "args": e.ArgsJSON,
		"patch": e.PatchText, "context": e.Context, "promptable": fmt.Sprint(e.Promptable), "mcp": fmt.Sprint(e.MCP)}
	if e.Verdict != nil {
		fields["decision"] = e.Verdict.Decision
		fields["reason"] = e.Verdict.Reason
		fields["context"] = e.Verdict.Context
		fields["why"] = e.Verdict.Why
		if e.Verdict.Decision != "unjudged" && e.Verdict.SchemaVersion != 1 {
			fields["decision"] = "unjudged"
			fields["why"] = "unsupported guard verdict schema"
		}
	}
	return record(fields)
}
func decodeResult(value vm.Value) (dispatchResult, error) {
	for _, key := range []string{"block", "reason", "context", "status", "mcp"} {
		v, ok := value.MapGet(key)
		if !ok || !v.IsString() {
			return dispatchResult{}, fmt.Errorf("harness runtime: malformed Buzz result field %q", key)
		}
	}
	block, mcp, status := field(value, "block"), field(value, "mcp"), field(value, "status")
	if (block != "true" && block != "false") || (mcp != "true" && mcp != "false") ||
		(status != "" && status != "ask" && status != "allow" && status != "deny") {
		return dispatchResult{}, errors.New("harness runtime: malformed Buzz decision")
	}
	return dispatchResult{Block: block == "true", Reason: field(value, "reason"), Context: field(value, "context"), Status: status, MCP: mcp == "true"}, nil
}

func judge(ctx context.Context, invoke invoker, event vm.Value, mode, payload string) vm.Value {
	unjudged := func(why string) vm.Value { return record(map[string]string{"decision": "unjudged", "why": why}) }
	args := shellArgs()
	switch mode {
	case "path":
		args = append([]string{"shell", "--path"}, args[1:]...)
	case "envelope":
		var input any
		if err := json.Unmarshal([]byte(payload), &input); err != nil {
			return unjudged("invalid tool arguments JSON")
		}
		envelope := map[string]any{"hook_event_name": "PreToolUse", "tool_name": field(event, "tool"), "tool_input": input, "cwd": field(event, "cwd")}
		if session := field(event, "session"); session != "" {
			envelope["session_id"] = session
		}
		if agent := field(event, "agent"); agent != "" {
			envelope["agent_id"] = agent
		}
		body, err := json.Marshal(envelope)
		if err != nil {
			return unjudged("cannot encode tool envelope")
		}
		payload = string(body)
	case "command":
	default:
		return unjudged("unknown native judgment mode")
	}
	if mode != "envelope" {
		if session := field(event, "session"); session != "" {
			args = append(args, "--session", session)
		}
		if agent := field(event, "agent"); agent != "" {
			args = append(args, "--agent", agent)
		}
	}
	verdict := askGuard(ctx, invoke, magusCall{Args: args, Input: payload})
	return record(map[string]string{"decision": verdict.Decision, "reason": verdict.Reason, "context": verdict.Context, "why": verdict.Why})
}

// askGuard preserves the native guard's verdict for lifecycle events whose
// result is delivered through an SDK prompt rather than a tool rejection.
func askGuard(ctx context.Context, invoke invoker, invocation magusCall) guardVerdict {
	capture, err := invokeWithLegacyRetry(ctx, invoke, invocation)
	if err != nil {
		return unjudgedVerdict(err.Error())
	}
	envelope, why := verdictEnvelope(capture.Stdout)
	if why != "" {
		return unjudgedVerdict(why)
	}
	return decideVerdict(envelope)
}

func unjudgedVerdict(why string) guardVerdict { return guardVerdict{Decision: "unjudged", Why: why} }

// invokeWithLegacyRetry drops the newer flags when magus prints nothing.
func invokeWithLegacyRetry(ctx context.Context, invoke invoker, invocation magusCall) (processOutput, error) {
	capture, err := invoke(ctx, invocation)
	if err != nil || strings.TrimSpace(capture.Stdout) != "" {
		return capture, err
	}
	return invoke(ctx, magusCall{Args: legacyArgs(invocation.Args), Input: invocation.Input})
}

func legacyArgs(args []string) []string {
	legacy := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent-name", "--session", "--agent":
			i++
		case "--renders-ask":
		default:
			legacy = append(legacy, args[i])
		}
	}
	return legacy
}

// verdictEnvelope decodes stdout, or says why it is not a verdict.
func verdictEnvelope(stdout string) (map[string]json.RawMessage, string) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		if json.Valid([]byte(stdout)) {
			return nil, "magus answered an unrecognized verdict shape"
		}
		return nil, "magus answered something that is not JSON"
	}
	return envelope, schemaMismatch(envelope)
}

func schemaMismatch(envelope map[string]json.RawMessage) string {
	var schema float64
	raw := envelope["schema_version"]
	if envelope == nil || json.Unmarshal(raw, &schema) != nil || string(raw) == "null" {
		return "magus answered an unrecognized verdict shape"
	}
	if schema != 1 {
		return fmt.Sprintf("magus answered verdict schema %v, not 1; update this plugin from the magus docs", schema)
	}
	return ""
}

func decideVerdict(envelope map[string]json.RawMessage) guardVerdict {
	var decision string
	_ = json.Unmarshal(envelope["decision"], &decision)
	verdict := guardVerdict{SchemaVersion: 1, Decision: decision}
	ok := false
	switch decision {
	case "pass":
		return verdict
	case "deny", "ask":
		verdict.Reason, ok = stringMember(envelope, "reason")
	case "advise":
		verdict.Context, ok = stringMember(envelope, "context")
	}
	if ok {
		return verdict
	}
	return unknownDecision(envelope)
}

func unknownDecision(envelope map[string]json.RawMessage) guardVerdict {
	label := "undefined"
	if raw, ok := envelope["decision"]; ok {
		var decoded any
		if json.Unmarshal(raw, &decoded) == nil {
			encoded, _ := json.Marshal(decoded)
			label = string(encoded)
		}
	}
	reason := fmt.Sprintf("magus guard returned the decision %s, which this plugin does not know, so it refuses the call rather than allow it. Update the plugin from the magus docs.", label)
	return guardVerdict{SchemaVersion: 1, Decision: "deny", Reason: reason}
}

// stringMember is false unless key holds a JSON string, so a verdict
// missing its reason or context is refused rather than read as empty.
func stringMember(envelope map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := envelope[key]
	if !ok || string(raw) == "null" {
		return "", false
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	return value, true
}
