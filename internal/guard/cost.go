package guard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// The facts the cost rules and the trail read about a caller: which thread of its session
// it is, how large its context is, and how its account pays. None is inferred from a
// host's name.

// hostAgentsKind marks a host that has reported a subagent id on a judged call, so a call
// from it that reports none comes from a main thread.
const hostAgentsKind hint.MarkerKind = "reports-agent-ids"

// costStateKind holds a session main thread's costState.
const costStateKind hint.MarkerKind = "cost-main"

// hostGate keys what magus has learned about one host, across its sessions. A caller that
// names no host learns nothing.
func hostGate(cacheDir, host string) hint.Gate {
	if strings.TrimSpace(host) == "" {
		return hint.NewGate("", "")
	}
	return hint.NewGate(cacheDir, "guard/host/"+callerKeyEscaper.Replace(host))
}

// callerThread is which thread of its session who is, and what that rests on. A call
// naming a subagent is one. A host that has named a subagent on some call does so on
// every call, so its calls naming none are the main thread's. Otherwise an unleased call
// from a host is taken for the main thread, inferred: the subagents of a host that names
// none look the same, so a rule reading an inferred thread only advises. A call no host
// made, or a leased one from a host magus has not learned, is unknown.
func callerThread(cacheDir string, who hookAttribution, lease string) (types.AgentThread, types.ThreadSource) {
	switch {
	case who.Agent != "":
		return types.AgentThreadSub, types.ThreadReported
	case strings.TrimSpace(who.Host) == "":
		return types.AgentThreadUnknown, types.ThreadInferred
	case hostGate(cacheDir, who.Host).AlreadyFired(hostAgentsKind):
		return types.AgentThreadMain, types.ThreadReported
	case lease == "":
		return types.AgentThreadMain, types.ThreadInferred
	}
	return types.AgentThreadUnknown, types.ThreadInferred
}

// callerCost is the thread, its source and the context window a rule request carries for
// who.
func callerCost(at location, who hookAttribution, lease string, facts hint.Gate) (types.AgentThread, types.ThreadSource, *types.ContextWindow) {
	thread, from := callerThread(at.cacheDir, who, lease)
	return thread, from, callerContext(at, who, thread, facts)
}

// callerContext is the caller's context as magus last recorded it: the main thread's from
// its transcript, a subagent's from the usage its host reported for it. Nil when nothing
// was recorded.
func callerContext(at location, who hookAttribution, thread types.AgentThread, facts hint.Gate) *types.ContextWindow {
	var c types.ContextWindow
	switch thread {
	case types.AgentThreadMain:
		st := readCostState(facts)
		c = types.ContextWindow{Tokens: st.Tokens, Window: st.Window, Model: st.Model, Source: types.ContextFromTranscript}
	case types.AgentThreadSub:
		rec, _ := readSpawnedAgent(facts, who.Agent)
		if rec.ContextTokens != nil {
			c = types.ContextWindow{Tokens: *rec.ContextTokens, Window: rec.ContextWindow, Model: rec.Model, Source: types.ContextFromTranscript}
		}
	}
	if c.Tokens == 0 {
		return nil
	}
	if c.Window == 0 {
		if row, ok := resolveModel(c.Model, at.modelTable()); ok {
			c.Window = row.Window
		}
	}
	return &c
}

// account is what the person's magus.yaml and the host's environment say about the
// account agents run under.
type account struct {
	billing types.AgentBilling
	// overrides are the rows agents.models declares. The harness rows are not kept here:
	// they belong to the request judged, and location.modelTable layers these over them.
	overrides []types.ModelPricing
	read      time.Time
}

// accountTTL bounds how long a process trusts a read of magus.yaml; a daemon judging for
// hours must see an edit.
const accountTTL = 10 * time.Second

var accounts sync.Map

// accountAt is the billing mode and agents.models rows in effect for the workspace at
// root. A magus.yaml that does not load leaves billing unknown and no rows, and a declared
// mode the environment contradicts yields the detected one.
func accountAt(root string) account {
	if got, ok := accounts.Load(root); ok && time.Since(got.(account).read) < accountTTL {
		return got.(account)
	}
	a := account{read: time.Now()}
	if agents, err := config.LoadAgents(root); err == nil {
		a.billing = agents.ResolveBilling(os.Getenv)
		a.overrides = agents.ModelOverrides()
	} else {
		a.billing = config.DetectBilling(os.Getenv)
	}
	accounts.Store(root, a)
	return a
}

// modelTable is every model row in effect for the request judged at at: the rows its
// harnesses publish, then the workspace's agents.models over them. The harness rows can
// take a workspace load, so a call that needs only billing reads accountAt instead.
func (at location) modelTable() model.Table {
	var harness []types.ModelPricing
	if at.models != nil {
		harness = at.models()
	}
	return model.InEffect(harness, accountAt(at.workspace).overrides)
}

// resolveModel finds id's row: by Table.Lookup, else as an alias a host takes in place of
// an id, one dash-separated word of a row's id with no version in it, which names the
// first such row.
func resolveModel(id string, t model.Table) (types.ModelPricing, bool) {
	if id == "" {
		return types.ModelPricing{}, false
	}
	if row, ok := t.Lookup(id); ok {
		return row, true
	}
	alias := strings.ToLower(strings.TrimSpace(id))
	if strings.ContainsAny(alias, "0123456789-.") {
		return types.ModelPricing{}, false
	}
	for _, row := range t.Rows() {
		for _, word := range strings.Split(row.ID, "-") {
			if word == alias {
				return row, true
			}
		}
	}
	return types.ModelPricing{}, false
}

// costState is what magus keeps about a session's main thread for the cost rules. It is
// written after a call is judged and read by the rules on the next one, so a rule judging
// a call never opens the transcript.
type costState struct {
	// Tokens, Window and Model are the thread's context as its transcript last reported
	// it; Window is 0 when the host reported none.
	Tokens int64  `json:"tokens,omitzero"`
	Window int64  `json:"window,omitzero"`
	Model  string `json:"model,omitempty"`
	// Transcript and Offset are the file last read and the end of its last whole line
	// read, so the next read starts where this one stopped.
	Transcript string `json:"transcript,omitempty"`
	Offset     int64  `json:"offset,omitzero"`
	// Streak counts delegable calls since the thread last spawned or continued an agent.
	Streak int `json:"streak,omitzero"`
	// Repeats counts each delegable command line, by digest, since the thread last wrote
	// a file.
	Repeats map[string]int `json:"repeats,omitempty"`
	// Results is the largest result each host tool returned this session, in characters.
	Results map[string]int `json:"results,omitempty"`
	// Pending maps a tool call the transcript shows started to its tool and delegable
	// class, until its result arrives.
	Pending map[string]pendingCall `json:"pending,omitempty"`
}

type pendingCall struct {
	Tool  string               `json:"tool"`
	Class types.DelegableClass `json:"class,omitempty"`
}

// pendingCap bounds Pending: a call whose result never lands must not grow it forever.
const pendingCap = 64

func readCostState(facts hint.Gate) costState {
	var st costState
	if facts.CacheDir() == "" {
		return st
	}
	body, err := os.ReadFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), costStateKind))
	if err == nil {
		_ = json.Unmarshal(body, &st)
	}
	return st
}

// writeCostState replaces the record whole, best effort, since a racing hook may be
// reading it.
func writeCostState(facts hint.Gate, st costState) {
	if facts.CacheDir() == "" {
		return
	}
	body, err := json.Marshal(st)
	if err != nil {
		return
	}
	_ = file.ReplaceFile(hint.MarkerPath(facts.CacheDir(), facts.Session(), costStateKind), body, 0o644)
}

// lineDigest names a command line in Repeats.
func lineDigest(line string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(line)))
	return hex.EncodeToString(sum[:8])
}

// toolResult is one finished tool call the transcript reported.
type toolResult struct {
	tool  string
	class types.DelegableClass
	chars int
}

// readTranscript advances st over the lines transcript gained since it was last read, at
// most agentUsageTail of them: the latest context size and model, and every tool result. A
// file other than the one st last read starts over. Lines a subagent wrote into the main
// transcript are skipped.
func (st *costState) readTranscript(transcript string) []toolResult {
	if !filepath.IsAbs(transcript) {
		return nil
	}
	if transcript != st.Transcript {
		st.Transcript, st.Offset, st.Pending = transcript, 0, nil
	}
	f, err := os.Open(transcript)
	if err != nil {
		return nil
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	if info.Size() < st.Offset {
		st.Offset = 0
	}
	start := max(st.Offset, info.Size()-agentUsageTail)
	// The tail cut into a line, and half a record is not one.
	cut := start > st.Offset
	buf := make([]byte, info.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil
	}
	end := bytes.LastIndexByte(buf[:n], '\n')
	if end < 0 {
		return nil
	}
	st.Offset = start + int64(end) + 1
	lines := bytes.Split(buf[:end], []byte("\n"))
	if cut {
		lines = lines[1:]
	}
	var results []toolResult
	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var rec transcriptUsage
		if json.Unmarshal(line, &rec) != nil || rec.IsSidechain {
			continue
		}
		if tokens, window, ok := rec.contextSize(); ok {
			st.Tokens, st.Window = tokens, window
			if rec.Message.Model != "" {
				st.Model = rec.Message.Model
			}
		}
		results = append(results, st.toolCalls(rec)...)
	}
	return results
}

// contentBlock is one entry of a message's content: a tool call the model made, or the
// result handed back for one.
type contentBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	Text      string          `json:"text"`
}

// toolCalls files the tool calls rec starts in Pending and returns the results it hands
// back, read by shape: content blocks of a message, or a rollout's function call items.
func (st *costState) toolCalls(rec transcriptUsage) []toolResult {
	var out []toolResult
	start := func(id, tool string, class types.DelegableClass) {
		if id == "" {
			return
		}
		if st.Pending == nil {
			st.Pending = map[string]pendingCall{}
		}
		if len(st.Pending) >= pendingCap {
			clear(st.Pending)
		}
		st.Pending[id] = pendingCall{Tool: tool, Class: class}
	}
	finish := func(id string, chars int) {
		call, ok := st.Pending[id]
		if !ok {
			call = pendingCall{Tool: "tool"}
		}
		delete(st.Pending, id)
		out = append(out, toolResult{tool: call.Tool, class: call.Class, chars: chars})
	}
	if rec.Type == "response_item" {
		switch rec.Payload.Type {
		case "function_call":
			var args map[string]any
			_ = json.Unmarshal([]byte(rec.Payload.Arguments), &args)
			start(rec.Payload.CallID, rec.Payload.Name, inputClass(args))
		case "function_call_output":
			finish(rec.Payload.CallID, resultChars(rec.Payload.Output))
		}
		return out
	}
	if len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' {
		return nil
	}
	var blocks []contentBlock
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return nil
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			start(b.ID, b.Name, inputClass(b.Input))
		case "tool_result":
			finish(b.ToolUseID, resultChars(b.Content))
		}
	}
	return out
}

// resultChars is the size of a tool result: a string's length, the text of a list of
// blocks, or the encoded size of anything else.
func resultChars(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return len(s)
	}
	var blocks []contentBlock
	if json.Unmarshal(raw, &blocks) == nil {
		n := 0
		for _, b := range blocks {
			n += len(b.Text)
		}
		return n
	}
	return len(raw)
}

// inputClass is the delegable class of a host tool call, read from its input by shape: a
// shell command by its line, a whole file read, a search pattern, a URL.
func inputClass(input map[string]any) types.DelegableClass {
	if line := inputCommand(input); line != "" {
		return delegableClass(line)
	}
	_, writes := input["content"]
	_, edits := input["old_string"]
	switch p, _ := input["file_path"].(string); {
	case p != "" && !writes && !edits && !orientationFile(p):
		return types.DelegableRead
	case input["pattern"] != nil && !writes && !edits:
		return types.DelegableSearch
	case input["url"] != nil:
		return types.DelegableFetch
	}
	return ""
}

// inputCommand is the shell line a tool input carries, as a string or an argv.
func inputCommand(input map[string]any) string {
	for _, key := range []string{"command", "cmd"} {
		switch v := input[key].(type) {
		case string:
			return v
		case []any:
			words := make([]string, 0, len(v))
			for _, w := range v {
				s, _ := w.(string)
				words = append(words, shellWord(s))
			}
			return strings.Join(words, " ")
		}
	}
	return ""
}

// orientationFile is a file whose read orients a reader rather than does the work: an
// instruction file, a plan, a note. Markdown, by extension.
func orientationFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".mdx", ".markdown":
		return true
	}
	return false
}

// delegableClass names the work a shell line does that a subagent could have done in the
// caller's place, "" for any other line. The final gate is never delegable: the main
// thread runs it, and neither is a command that costFree reports.
func delegableClass(line string) types.DelegableClass {
	if commandRunsGate(line) {
		return ""
	}
	cmds, ok := ParseCommands(line)
	if !ok {
		return ""
	}
	for _, c := range cmds {
		if freeInvocation(c) {
			continue
		}
		if class := invocationClass(c); class != "" {
			return class
		}
	}
	return ""
}

// costFree reports a shell line whose output is a ref or one line, so running it in the
// main thread adds nothing to the context every later call re-sends, and no subagent would
// do it cheaper. The cost rules that keep large output out of that context leave it alone,
// which matters most for work a subagent may not do: only the main session builds ./magus,
// so a main thread past its budget must still be able to. A line that changes directory
// first is judged by the rest of it.
func costFree(line string) bool {
	if strings.TrimSpace(line) == ward.BootstrapCommand {
		return true
	}
	cmds, ok := ParseCommands(line)
	if !ok {
		return false
	}
	free := false
	for _, c := range cmds {
		switch {
		case isCdInvocation(c):
		case freeInvocation(c):
			free = true
		default:
			return false
		}
	}
	return free
}

func freeInvocation(c hint.Invocation) bool {
	if isMagusInvocation(c) {
		return freeMagus(c.Args)
	}
	v := vcsInvocation(c)
	if v == nil || v.Tool != "git" {
		return false
	}
	switch v.Subcommand {
	case "commit", "cherry-pick", "branch":
		return true
	case "status":
		return hasFlag(v.Args, 's', "short")
	case "log":
		return hasFlag(v.Args, 0, "oneline") && !hasFlag(v.Args, 'p', "patch") && !hasFlag(v.Args, 'u', "") && logLimited(v.Args)
	}
	return false
}

// logLimited reports a git log argv that bounds its commit count: -n N, -nN, --max-count
// or -N.
func logLimited(args []string) bool {
	if hasFlag(args, 'n', "max-count") {
		return true
	}
	for _, a := range args {
		if a == "--" {
			return false
		}
		if len(a) > 1 && a[0] == '-' && strings.Trim(a[1:], "0123456789") == "" {
			return true
		}
	}
	return false
}

// freeMagus reports a magus argv that prints a ref or one line: a run or an affected set
// asked for silence, the build of ./magus itself, a job verb that files or waits, a
// projection down to names or one template field, or the identity of a run's output.
func freeMagus(args []string) bool {
	words := magusSubcommandWords(args)
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "run", "affected":
		if magusFlag(args, "s", "silent") {
			return true
		}
		if words[0] == "run" && len(words) > 1 && (words[1] == "go-build" || words[1] == "go::go-build") {
			return true
		}
	case "job":
		if len(words) > 1 {
			switch words[1] {
			case "fork", "exit", "apply", "wait":
				return true
			}
		}
	case "query":
		if len(words) > 1 && words[1] == "output" && magusFlag(args, "identity") {
			return true
		}
	}
	return magusProjection(args)
}

// magusProjection reports an -o that asks for ids (name) or one template field: output
// whose size does not grow with the workspace.
func magusProjection(args []string) bool {
	format := magusOutputFormat(args)
	return format == "name" || strings.HasPrefix(format, "template=")
}

func invocationClass(c hint.Invocation) types.DelegableClass {
	name := path.Base(c.Name)
	switch name {
	case "rg", "ag", "ack", "find", "fd":
		return types.DelegableSearch
	case "grep", "egrep", "fgrep":
		if hasFlag(c.Args, 'r', "recursive") || hasFlag(c.Args, 'R', "dereference-recursive") {
			return types.DelegableSearch
		}
		return ""
	case "curl", "wget":
		return types.DelegableFetch
	}
	for _, rc := range parseReadCalls(c) {
		if !orientationFile(rc.path) {
			return types.DelegableRead
		}
	}
	if v := vcsInvocation(c); v != nil && v.Tool == "git" {
		summary := func(flags ...string) bool {
			for _, a := range v.Args {
				for _, f := range flags {
					if a == f || strings.HasPrefix(a, f+"=") {
						return true
					}
				}
			}
			return false
		}
		switch v.Subcommand {
		case "diff", "show":
			if !summary("--stat", "--shortstat", "--numstat", "--name-only", "--name-status") {
				return types.DelegableDiff
			}
		case "log":
			if summary("-p", "--patch", "-u") && !summary("--stat", "--name-only", "--name-status") {
				return types.DelegableDiff
			}
		}
	}
	if isMagusInvocation(c) {
		switch words := magusSubcommandWords(c.Args); {
		case len(words) > 0 && words[0] == "run":
			return types.DelegableRun
		case len(words) > 1 && words[0] == "query" && words[1] == "output":
			// A full log; costFree took the --identity form before it got here.
			return types.DelegableRead
		}
	}
	return ""
}

// observeCall records what one call teaches about its caller and returns the facts its
// trail row carries. It runs after the verdict, never on a dry run, so the rules read on
// the next call what this one recorded.
//
// An observation is the same call another hook judges, so it records the thread alone and
// nothing a metric counts: counting both would count the call twice.
func observeCall(ctx context.Context, at location, input string, who hookAttribution, tool, lease string, verdict Verdict) types.AgentFacts {
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	if who.Agent != "" && verdict.Decision != "" {
		hostGate(at.cacheDir, who.Host).MarkFired(hostAgentsKind)
	}
	thread, from := callerThread(at.cacheDir, who, lease)
	out := types.AgentFacts{Thread: thread, ThreadFrom: from}
	if verdict.Decision == "" {
		return out
	}
	out.Billing = accountAt(at.workspace).billing
	free := false
	if tool == hookToolCommand {
		out.DelegableClass = delegableClass(input)
		free = costFree(input)
	}
	ran := verdict.Decision == decisionPass || verdict.Decision == decisionAdvise
	var results []toolResult
	if thread == types.AgentThreadMain && lease == "" {
		st := readCostState(facts)
		out.Shadow = shadowVerdicts(st, mainCall{from: from, billing: out.Billing, window: contextWindow(st, at), class: out.DelegableClass, free: free, line: input})
		results = st.readTranscript(who.Transcript)
		switch {
		case ran && tool == hookToolCommand && out.DelegableClass != "":
			st.Streak++
			if st.Repeats == nil {
				st.Repeats = map[string]int{}
			}
			st.Repeats[lineDigest(input)]++
		case ran && tool == hookToolWrite:
			st.Repeats = nil
		}
		for _, r := range results {
			if r.chars > st.Results[r.tool] {
				if st.Results == nil {
					st.Results = map[string]int{}
				}
				st.Results[r.tool] = r.chars
			}
		}
		writeCostState(facts, st)
	}
	setContext(&out, callerContext(at, who, thread, facts))
	if lease != "" {
		observeStall(ctx, at, who, lease, verdict)
	}
	recordAgentCalls(ctx, out, results)
	return out
}

// setContext files w's size and model on f; a nil w leaves both unset.
func setContext(f *types.AgentFacts, w *types.ContextWindow) {
	if w != nil {
		f.ContextTokens, f.ContextModel = w.Tokens, w.Model
	}
}

// recordAgentCalls counts one call in the magus.agent.* metrics, and each tool result the
// transcript handed back since the last call under the class of the call that made it.
func recordAgentCalls(ctx context.Context, f types.AgentFacts, results []toolResult) {
	p := observability.FromContext(ctx)
	if p == nil {
		return
	}
	p.RecordAgentCall(ctx, observability.AgentCall{AgentFacts: f})
	for _, r := range results {
		if r.class != "" && r.chars > 0 {
			p.RecordAgentCall(ctx, observability.AgentCall{AgentFacts: types.AgentFacts{Thread: f.Thread, DelegableClass: r.class, ResultChars: &r.chars}})
		}
	}
}

// noteMainSpawn restarts the main thread's streak of delegable calls: it handed work out.
func noteMainSpawn(at location, who hookAttribution, lease string) {
	if lease != "" || at.cacheDir == "" {
		return
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	if thread, _ := callerThread(at.cacheDir, who, lease); thread != types.AgentThreadMain {
		return
	}
	st := readCostState(facts)
	if st.Streak == 0 {
		return
	}
	st.Streak = 0
	writeCostState(facts, st)
}

// contextWindow is the main thread's window: the host's, else the model table's, else 0.
func contextWindow(st costState, at location) int64 {
	if st.Window > 0 {
		return st.Window
	}
	if row, ok := resolveModel(st.Model, at.modelTable()); ok {
		return row.Window
	}
	return 0
}
