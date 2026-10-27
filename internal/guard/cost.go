package guard

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/syntax"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/cache"
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
		if row, ok := at.modelTable().Lookup(c.Model); ok {
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
	// cacheTTL is agents.prompt_cache_ttl, zero when unset; loadErr is why magus.yaml did
	// not load, which a reader of cacheTTL must say rather than read zero as unset.
	cacheTTL time.Duration
	loadErr  error
	read     time.Time
}

// accountTTL bounds how long a process trusts a read of magus.yaml; a daemon judging for
// hours must see an edit.
const accountTTL = 10 * time.Second

var accounts sync.Map

// accountAt is the billing mode and agents.models rows in effect for the workspace at
// root. A magus.yaml that does not load leaves billing unknown and no rows, and a declared
// mode the environment contradicts yields the detected one.
func accountAt(root string) account {
	if got, ok := accounts.Load(root); ok {
		if cached, ok := got.(account); ok && time.Since(cached.read) < accountTTL {
			return cached
		}
	}
	a := account{read: time.Now()}
	if agents, err := config.LoadAgents(root); err == nil {
		a.billing = agents.ResolveBilling(os.Getenv)
		a.overrides = agents.ModelOverrides()
		a.cacheTTL = agents.PromptCacheTTL
	} else {
		a.billing = config.DetectBilling(os.Getenv)
		a.loadErr = err
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
	// Orchestrates is whether the thread hands its work to workers as of the last call:
	// proven while a row in Forked is live, and "" once every one has ended or been taken
	// back. sessionCostState derives it from the job store on every read.
	Orchestrates types.Orchestration `json:"orchestrates,omitempty"`
	// Forks maps each job id the thread ran a fork of to when that call was judged, in
	// unix nanoseconds, until the store shows the row or forkConfirmWindow passes.
	Forks map[string]int64 `json:"forks,omitempty"`
	// Forked are the rows the thread handed out: forks the store confirmed and rows a
	// titled spawn bound. A row the thread then takes itself with `magus job exec` leaves.
	Forked []string `json:"forked,omitempty"`
	// Refs are the output, invocation and verdict refs the thread's tool results named,
	// oldest first and at most refsCap, so reading one back is checking its own work.
	Refs []string `json:"refs,omitempty"`
}

// refsCap bounds Refs, dropping the oldest first.
const refsCap = 256

// forget drops id from the rows the thread handed out, reporting whether it was there.
func (st *costState) forget(id string) bool {
	_, pending := st.Forks[id]
	delete(st.Forks, id)
	if len(st.Forks) == 0 {
		st.Forks = nil
	}
	n := len(st.Forked)
	st.Forked = slices.DeleteFunc(st.Forked, func(f string) bool { return f == id })
	return pending || len(st.Forked) != n
}

type pendingCall struct {
	Tool  string               `json:"tool"`
	Class types.DelegableClass `json:"class,omitempty"`
}

// pendingCap bounds Pending: a call whose result never lands must not grow it forever.
const pendingCap = 64

func readCostState(facts hint.Gate) costState {
	st, _ := readMarker[costState](facts, costStateKind)
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
		if rec.Type == "response_item" && rec.Payload.Type == "function_call_output" {
			st.noteRefs(rec.Payload.Output)
			continue
		}
		var blocks []contentBlock
		if len(rec.Message.Content) > 0 && rec.Message.Content[0] == '[' && json.Unmarshal(rec.Message.Content, &blocks) == nil {
			for _, b := range blocks {
				if b.Type == "tool_result" {
					st.noteRefs(b.Content)
				}
			}
		}
	}
	return results
}

// noteRefs adds to Refs every minted output ref, invocation id and guard verdict ref a
// tool result's text names.
func (st *costState) noteRefs(raw json.RawMessage) {
	var text string
	if json.Unmarshal(raw, &text) != nil {
		var blocks []contentBlock
		_ = json.Unmarshal(raw, &blocks)
		for _, b := range blocks {
			text += b.Text + "\n"
		}
	}
	words := strings.FieldsFunc(text, func(r rune) bool { return (r < 'a' || r > 'z') && (r < '0' || r > '9') })
	for _, w := range words {
		if !namesRef(w) || slices.Contains(st.Refs, w) {
			continue
		}
		st.Refs = append(st.Refs, w)
	}
	if over := len(st.Refs) - refsCap; over > 0 {
		st.Refs = slices.Delete(st.Refs, 0, over)
	}
}

// namesRef reports a word `magus query output` or `query invocation` reads back: a minted
// output ref, an invocation id, or a guard verdict ref. An invocation id carries a digit,
// which keeps words like "invalid" out.
func namesRef(w string) bool {
	if cache.IsMintedRef(w) {
		return true
	}
	if tail, ok := strings.CutPrefix(w, verdictRefPrefix); ok && len(tail) == 16 {
		_, err := hex.DecodeString(tail)
		return err == nil
	}
	return cache.LooksLikeInvocationID(w) && strings.ContainsAny(w, "0123456789")
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
func observeCall(ctx context.Context, at location, input string, who hookAttribution, tool, lease string, verdict types.GuardJudgment) types.AgentFacts {
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
		now := time.Now()
		st := sessionCostState(at, who, liveRowLookup(ctx, at), now)
		out.Orchestration = orchestration(st, from)
		call := mainCall{from: from, billing: out.Billing, window: contextWindow(st, at), class: out.DelegableClass, free: free, line: input, orchestration: out.Orchestration}
		switch {
		case out.Orchestration == "":
		case tool == hookToolCommand:
			call.work = mainWork(ctx, at, st, input)
		case tool == hookToolWrite && !notesPath(input, at):
			call.work = types.DelegableEdit
		}
		out.Shadow = shadowVerdicts(st, call)
		results = st.readTranscript(who.Transcript)
		if ran && tool == hookToolCommand {
			noteFork(&st, input, now)
		}
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

// noteMainSpawn restarts the main thread's streak of delegable calls, since it handed work
// out, and files row, the live job row the spawn's title bound, among the rows the thread
// handed out. A spawn bound to no row hands out nothing the store can show.
func noteMainSpawn(at location, who hookAttribution, lease, row string) {
	if lease != "" || at.cacheDir == "" {
		return
	}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	if thread, _ := callerThread(at.cacheDir, who, lease); thread != types.AgentThreadMain {
		return
	}
	st := readCostState(facts)
	streak := st.Streak
	st.Streak = 0
	added := row != "" && !slices.Contains(st.Forked, row)
	if added {
		st.Forked = append(st.Forked, row)
	}
	if streak == 0 && !added {
		return
	}
	writeCostState(facts, st)
}

// orchestration is how surely the thread st records hands its work to workers. Proof
// counts only on a main thread its host reported: where root and worker look alike, the
// same record is inferred, and a rule reading it only advises.
func orchestration(st costState, from types.ThreadSource) types.Orchestration {
	switch {
	case st.Orchestrates == types.OrchestrationProven && from == types.ThreadReported:
		return types.OrchestrationProven
	case st.Orchestrates != "":
		return types.OrchestrationInferred
	}
	return ""
}

// sessionCostState is who's main-thread record as of now: forks the store has shown since
// confirmed, and Orchestrates proven while a row the thread handed out is live (an exited
// row waits on its `job wait`). The role is the live lease, never history: once every row
// ends it orchestrates nothing. It writes nothing; observeCall files what it returns.
func sessionCostState(at location, who hookAttribution, live func(id string) (types.Job, bool), now time.Time) costState {
	st := readCostState(hint.NewGate(at.cacheDir, who.factsKey()))
	if len(st.Forks) > 0 {
		confirmForks(&st, live, now)
	}
	st.Orchestrates = ""
	if slices.ContainsFunc(st.Forked, func(id string) bool { _, ok := live(id); return ok }) {
		st.Orchestrates = types.OrchestrationProven
	}
	return st
}

// callerOrchestration is orchestration for a rule request's caller, "" for any caller but
// an unleased main thread. Its callers carry no pinned job store, so the store is read
// here, and only when the thread handed a row out.
func callerOrchestration(at location, who hookAttribution, thread types.AgentThread, from types.ThreadSource, lease string) types.Orchestration {
	if thread != types.AgentThreadMain || lease != "" || at.cacheDir == "" {
		return ""
	}
	return orchestration(sessionCostState(at, who, liveRowLookup(context.Background(), at), time.Now()), from)
}

// forkConfirmWindow is how long a fork the thread ran waits for the store to show its row.
// A fork the store never shows, refused or failed, proves nothing.
const forkConfirmWindow = 10 * time.Minute

// noteFork files in st.Forks each job id line forks, at now: a `magus job fork`, a
// magus\job.put rendered from an MCP call, and either one inside a `magus buzz` script the
// line carries.
func noteFork(st *costState, line string, now time.Time) {
	for _, id := range forkedIDs(line) {
		if id == "" || id == unreadValue {
			continue
		}
		if st.Forks == nil {
			st.Forks = map[string]int64{}
		}
		st.Forks[id] = now.UnixNano()
	}
}

func forkedIDs(line string) []string {
	f, err := parseFile(line, DialectBash)
	if err != nil {
		return nil
	}
	var ids []string
	syntax.Walk(f, func(n syntax.Node) bool {
		s, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		for _, c := range stmtCommands(s, DialectBash) {
			switch {
			case c.Name == hint.ToolClient.String():
				if p := mcpParams(c.Args); p["op"] == jobOpPut {
					ids = append(ids, strings.TrimSpace(p["id"]))
				}
			case !isMagusInvocation(c):
			case hint.JobFork.MatchedBy(magusSubcommandWords(c.Args)):
				ids = append(ids, cliFork(c.Args, stdinRecord(s)).id)
			default:
				if script, ok := inlineBuzz(c.Args, stdinRecord(s)); ok {
					if lines, ok := clientScriptLines(script); ok {
						ids = append(ids, forkedIDs(strings.Join(lines, "\n"))...)
					}
				}
			}
		}
		return true
	})
	return ids
}

// confirmForks moves to st.Forked each fork the store now shows live, created no earlier
// than the call that ran it. A fork older than forkConfirmWindow that the store never
// showed is dropped.
func confirmForks(st *costState, live func(id string) (types.Job, bool), now time.Time) {
	for id, judged := range st.Forks {
		at := time.Unix(0, judged)
		if row, ok := live(id); ok && row.Created >= at.Unix() {
			if !slices.Contains(st.Forked, id) {
				st.Forked = append(st.Forked, id)
			}
			delete(st.Forks, id)
			continue
		}
		if now.Sub(at) > forkConfirmWindow {
			delete(st.Forks, id)
		}
	}
	if len(st.Forks) == 0 {
		st.Forks = nil
	}
}

// liveRowLookup finds a live row in the job store, read once however many ids it is asked
// for. An unreadable store finds none.
func liveRowLookup(ctx context.Context, at location) func(id string) (types.Job, bool) {
	rows := sync.OnceValue(func() []types.Job {
		rows, err := leaseRows(ctx, at)
		if err != nil {
			return nil
		}
		return rows
	})
	return func(id string) (types.Job, bool) {
		for _, row := range rows() {
			if row.ID == id && row.State.Live() {
				return row, true
			}
		}
		return types.Job{}, false
	}
}

// rootWorkClass is the groundwork a shell line does, judged for a session that hands its
// work out, "" for orchestration work: moving jobs, planning from the graph, reading a
// guard verdict, the notes, and rebuilding a stale or absent ./magus (replacesStaleMagus).
// Other integration is the checkout's integrator's. Unlike delegableClass, a silent run is still a run,
// and Markdown inside a checkout is still a read; only the instruction files, and a read or a
// search of nothing but paths outside every checkout, are notes.
// The checks of the rows a thread forked are mainWork's to clear.
func rootWorkClass(line string, at location) types.DelegableClass {
	f, err := parseFile(line, DialectBash)
	if err != nil || replacesStaleMagus(line, at.workspace) {
		return ""
	}
	// A relative path is read from where the line's cds have moved the call, so a root that
	// cds into its plan folder reads its plan, not the checkout's file of that name.
	dirs := stmtDirs(f, line, cmp.Or(at.dir, at.workspace), userHome())
	var class types.DelegableClass
	syntax.Walk(f, func(n syntax.Node) bool {
		if class != "" {
			return false
		}
		s, ok := n.(*syntax.Stmt)
		if !ok {
			return true
		}
		here := at
		if dir := dirs[s]; dir != "" {
			here.dir = dir
		}
		for _, c := range stmtCommands(s, DialectBash) {
			if class = invocationWork(c, stdinRecord(s), here); class != "" {
				return false
			}
		}
		return true
	})
	return class
}

func invocationWork(c hint.Invocation, stdin string, at location) types.DelegableClass {
	if isCdInvocation(c) {
		return ""
	}
	if reads := parseReadCalls(c); len(reads) > 0 {
		for _, rc := range reads {
			if !notesPath(rc.path, at) {
				return types.DelegableRead
			}
		}
		return ""
	}
	if !isMagusInvocation(c) {
		if v := vcsInvocation(c); v != nil && integratesVCS(v.Subcommand, v.Args) {
			return delegableIntegrate
		}
		class := invocationClass(c)
		// A search with no path searches the directory it runs in.
		paths := invocationPaths(c)
		if len(paths) == 0 {
			paths = []string{"."}
		}
		if class == types.DelegableSearch && !slices.ContainsFunc(paths, func(p string) bool { return !notesPath(p, at) }) {
			return ""
		}
		return class
	}
	words := magusSubcommandWords(c.Args)
	if len(words) == 0 {
		return ""
	}
	switch words[0] {
	case "run", "affected":
		// The workspace's own install, with no charm, is the gate's precondition: a fresh
		// checkout runs no gate until its dependencies are there.
		if r, ok := parseTargetRun(c); ok && !isGateCommand(c.Args) && !r.installs(Dependencies{}) {
			if integrationTarget(r.target) {
				return delegableIntegrate
			}
			return types.DelegableRun
		}
	case "vcs":
		if hint.VCSLsFiles.MatchedBy(words) {
			return types.DelegableRead
		}
		if len(words) > 1 && integratesVCS(words[1], words[2:]) {
			return delegableIntegrate
		}
	case "buzz":
		if script, ok := inlineBuzz(c.Args, stdin); ok && !drivesOnlyJobs(script) {
			return types.DelegableRun
		}
	case "query":
		if hint.QueryOutput.MatchedBy(words) && !magusFlag(c.Args, "identity") &&
			(len(words) < 3 || !strings.HasPrefix(words[2], verdictRefPrefix)) {
			return types.DelegableRead
		}
	case "diff":
		if !magusFlag(c.Args, "impact") {
			return types.DelegableDiff
		}
	}
	return ""
}

// delegableIntegrate is integration a checkout's integrator owns: a cherry-pick, a build of
// ./magus, a regeneration, staging or a commit. Only orchestrator-groundwork names it, so it
// is declared beside that rule rather than in types.
const delegableIntegrate types.DelegableClass = "integrate"

// integrationVerbs are the VCS subcommands that land work in a checkout or move its tree,
// which its integrator runs and an orchestrator never does. They are one class: a root
// allowed to merge but not commit integrated half a branch and left the rest.
var integrationVerbs = map[string]bool{
	"add": true, "am": true, "checkout": true, "cherry-pick": true, "commit": true, "graft": true,
	"merge": true, "pull": true, "rebase": true, "reset": true, "restore": true, "stash": true,
}

// integratesVCS reports a VCS subcommand that integrates: one of integrationVerbs, or a
// switch that creates the branch it moves to. A plain switch, a branch, a worktree add and
// a push are orchestration.
func integratesVCS(sub string, args []string) bool {
	if sub == "switch" {
		return slices.ContainsFunc(args, func(a string) bool {
			return strings.HasPrefix(a, "-c") || strings.HasPrefix(a, "-C") ||
				strings.HasPrefix(a, "--create") || strings.HasPrefix(a, "--force-create")
		})
	}
	return integrationVerbs[sub]
}

// integrationTarget reports a target a checkout's integrator runs to integrate work: the
// build of ./magus, and regeneration.
func integrationTarget(target string) bool {
	if i := strings.LastIndex(target, "::"); i >= 0 {
		target = target[i+2:]
	}
	t, err := types.ParseTarget(target)
	if err != nil {
		return false
	}
	return t.Name == "go-build" || t.Name == "generate" || strings.HasSuffix(t.Name, "-generate")
}

// drivesOnlyJobs reports a Buzz script whose every reach outside itself is magus\job: it
// imports magus alone and names no other member of it. A script that does not parse
// reaches anything.
func drivesOnlyJobs(script string) bool {
	prog, err := buzz.ParseEmbedded(script)
	if err != nil || prog == nil {
		return false
	}
	bound := map[string]bool{}
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		switch {
		case imp.Path != "magus" || imp.Alias == "_":
			return false
		case len(imp.Only) > 0:
			if slices.ContainsFunc(imp.Only, func(n string) bool { return n != "job" }) {
				return false
			}
		default:
			bound[cmp.Or(imp.Alias, "magus")] = true
		}
	}
	only := true
	for _, stmt := range prog.Stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			m, ok := n.(*ast.MemberExpr)
			if !ok {
				return only
			}
			if id, ok := m.Object.(*ast.IdentExpr); ok && m.Namespaced && bound[id.Name] && m.Name != "job" {
				only = false
			}
			return only
		})
	}
	return only
}

// mainWork is rootWorkClass for line, less what an orchestrator owes the rows it forked:
// running the check of a live row this thread forked, reading any row's recorded check
// output, and reading back a ref its own tool results named. The store is read only for a
// line that could be one of those.
func mainWork(ctx context.Context, at location, st costState, line string) types.DelegableClass {
	class := rootWorkClass(line, at)
	if class != types.DelegableRun && class != types.DelegableRead {
		return class
	}
	cmds, ok := ParseCommands(line)
	if !ok {
		return class
	}
	cmds = slices.DeleteFunc(cmds, isCdInvocation)
	if len(cmds) != 1 || !isMagusInvocation(cmds[0]) {
		return class
	}
	words := magusSubcommandWords(cmds[0].Args)
	readsBack := (hint.QueryOutput.MatchedBy(words) || hint.QueryInvocation.MatchedBy(words)) && len(words) > 2
	if readsBack && slices.Contains(st.Refs, words[2]) {
		return ""
	}
	rows, err := leaseRows(ctx, at)
	if err != nil {
		return class
	}
	if r, ok := parseTargetRun(cmds[0]); ok {
		for _, row := range rows {
			if row.State.Live() && slices.Contains(st.Forked, row.ID) && runsCheck(row.Check, r) {
				return ""
			}
		}
		return class
	}
	if readsBack {
		for _, row := range rows {
			if row.Result != nil && row.Result.Validation.OutputRef == words[2] {
				return ""
			}
		}
	}
	return class
}

// runsCheck reports a run of check's target in check's project, "." when it names none.
func runsCheck(check *types.LeaseCheck, r targetRun) bool {
	if check == nil || check.Target == "" || r.verb != "run" || r.target != check.Target {
		return false
	}
	project := cmp.Or(check.Project, ".")
	return slices.Equal(r.projects, []string{project}) || (len(r.projects) == 0 && project == ".")
}

// instructionFiles are the files an orchestrator reads to orient, wherever they sit. Each is
// written as the path a host discovers it at under a directory, so a host's own file is named
// in a path and a lookup prefixes the base name with "/".
var instructionFiles = []string{"/AGENTS.md", "/MAGUS.md", "/CLAUDE.md", "/SKILL.md"}

// notesPath reports a path an orchestrator reads or writes as notes rather than as work: an
// instruction file, or anything outside every checkout of the workspace's repository (a
// plan, a memory, a scratch file). A relative path is the call's, and a home-relative one is
// the home directory's, so it is a checkout's only when the home holds one; with the home
// unknown it is in no checkout. A workspace in no git repository has one checkout, itself.
func notesPath(p string, at location) bool {
	if slices.Contains(instructionFiles, "/"+filepath.Base(p)) {
		return true
	}
	if p = expandHome(p, userHome()); strings.HasPrefix(p, "~") {
		return true
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(cmp.Or(at.dir, at.workspace), p)
	}
	if at.workspace == "" {
		return true
	}
	_, common, ok := gitCheckout(at.workspace)
	if !ok {
		return !within(p, at.workspace)
	}
	// realPath, so a file in a directory not created yet resolves its symlinks through the
	// nearest one that exists, as the workspace's did. A directory a search names is its
	// own checkout's, not its parent's.
	dir := filepath.Dir(p)
	if info, err := os.Stat(p); err == nil && info.IsDir() {
		dir = p
	}
	_, pathCommon, ok := gitCheckout(realPath(dir))
	return !ok || pathCommon != common
}

// integrating reports a checkout, the one holding p, that is midway through a merge or a
// cherry-pick: an edit there may be the resolution of a conflict.
func integrating(p string, at location) bool {
	if !filepath.IsAbs(p) {
		p = filepath.Join(cmp.Or(at.dir, at.workspace), p)
	}
	root, _, ok := gitCheckout(realPath(filepath.Dir(p)))
	if !ok {
		return false
	}
	gitDir := filepath.Join(root, ".git")
	if info, err := os.Stat(gitDir); err == nil && !info.IsDir() {
		data, err := os.ReadFile(gitDir)
		if err != nil {
			return false
		}
		dir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
		if !ok {
			return false
		}
		gitDir = resolvePath(root, strings.TrimSpace(dir))
	}
	for _, head := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD"} {
		if _, err := os.Stat(filepath.Join(gitDir, head)); err == nil {
			return true
		}
	}
	return false
}

// contextWindow is the main thread's window: the host's, else the model table's, else 0.
func contextWindow(st costState, at location) int64 {
	if st.Window > 0 {
		return st.Window
	}
	if row, ok := at.modelTable().Lookup(st.Model); ok {
		return row.Window
	}
	return 0
}
