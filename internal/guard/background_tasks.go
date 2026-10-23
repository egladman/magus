package guard

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"mvdan.cc/sh/v3/syntax"
)

// denyRuleBackgroundLeft names the refusal of an agent's stop while shell commands it ran in
// the background are still running.
const denyRuleBackgroundLeft denyRuleName = "background-left"

// denyRuleBackgroundUnbounded names the refusal of a backgrounded shell command nothing ends.
const denyRuleBackgroundUnbounded denyRuleName = "background-unbounded"

// backgroundCeiling is the bound an unbounded background command's refusal names. It
// equals backgroundSilentAdvise, so a command run at the ceiling ends before the health
// check would speak of it.
const backgroundCeiling = 30 * time.Minute

// backgroundSilentAdvise is how long a background task may go without new output before
// each later call is advised of it: past half an hour, a quiet task is more likely stuck
// than slow.
const backgroundSilentAdvise = 30 * time.Minute

// backgroundSilentDeny is how long it may go before each later call is refused until it
// is stopped: twice the advisory, so the advice had a chance to be read and acted on.
const backgroundSilentDeny = 60 * time.Minute

// denyRuleBackgroundSilent names the advice, then the refusal, a session gets while one
// of its background tasks has gone silent.
const denyRuleBackgroundSilent denyRuleName = "background-silent"

// rootBackgroundAge is how old a root session's background task must be before its stop
// names it. A root wakes on a task's completion notice, so a young task is one it waits on.
const rootBackgroundAge = 30 * time.Minute

// stopEnvelope is a host's stop event: an agent ending its turn. stop_hook_active is the
// field that marks one, on a Stop or SubagentStop event.
type stopEnvelope struct {
	StopHookActive      *bool  `json:"stop_hook_active"`
	ToolName            string `json:"tool_name"`
	TranscriptPath      string `json:"transcript_path"`
	AgentID             string `json:"agent_id"`
	AgentTranscriptPath string `json:"agent_transcript_path"`
}

// decodeStop reads raw as a stop event, false for anything else.
func decodeStop(raw string) (stopEnvelope, bool) {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return stopEnvelope{}, false
	}
	var env stopEnvelope
	if json.Unmarshal([]byte(raw), &env) != nil || env.StopHookActive == nil || env.ToolName != "" {
		return stopEnvelope{}, false
	}
	return env, true
}

// advisoryBackgroundLeft holds a root's background-left advice to one firing per task per
// session; see backgroundTaskKey.
const advisoryBackgroundLeft hint.MarkerKind = "background-left"

// judgeStop answers an agent's stop event from the background tasks its transcript leaves
// running, false when it has nothing to say. A subagent with any task still running is
// refused, since no notice of the task reaches anyone once it ends. A root session is
// advised of tasks older than rootBackgroundAge, since the person is there to decide.
//
// A root hears of each task once per session, held on said: a task whose end never
// reached the transcript would otherwise be named after every turn.
//
// The transcript is the host's record, read by shape; a host whose transcript records no
// background task in that shape gets no answer, never a refusal.
func judgeStop(deps Dependencies, said hint.Gate, raw string, now time.Time) (types.GuardJudgment, bool) {
	env, ok := decodeStop(raw)
	if !ok {
		return types.GuardJudgment{}, false
	}
	setting, ok := deps.setting(string(denyRuleBackgroundLeft))
	if !ok || setting.Decision == builtin.Off {
		return types.GuardJudgment{}, false
	}
	subagent := env.AgentID != ""
	transcript := env.TranscriptPath
	if subagent {
		transcript = env.AgentTranscriptPath
	}
	if transcript == "" {
		return types.GuardJudgment{}, false
	}
	tasks, err := liveBackgroundTasks(transcript, now)
	if err != nil {
		return types.GuardJudgment{}, false
	}
	if !subagent {
		// Age is checked first, so a young task never spends its one firing.
		tasks = slices.DeleteFunc(tasks, func(t backgroundTask) bool {
			return now.Sub(t.Started) < rootBackgroundAge || said.MarkFired(backgroundTaskKey(advisoryBackgroundLeft, t))
		})
	}
	if len(tasks) == 0 {
		return types.GuardJudgment{}, false
	}
	listing := stoppableTasks(tasks, now)
	// A subagent's notice arrives only after a stop this refuses, so stopping is its remedy.
	if subagent && setting.Decision == builtin.Deny {
		return types.GuardJudgment{
			SchemaVersion: agent.GuardSchemaVersion,
			Decision:      decisionDeny,
			Rule:          string(denyRuleBackgroundLeft),
			Reason: fmt.Sprintf("magus workspace: %s you ran in the background %s still running; stop each before you finish:\n%s",
				plural(len(tasks), "a shell command", "shell commands"), plural(len(tasks), "is", "are"), listing),
		}, true
	}
	return types.GuardJudgment{
		SchemaVersion: agent.GuardSchemaVersion,
		Decision:      decisionAdvise,
		Rule:          string(denyRuleBackgroundLeft),
		Context: fmt.Sprintf("magus workspace: %s this session ran in the background %s still running; stop any nothing waits on:\n%s",
			plural(len(tasks), "a shell command", "shell commands"), plural(len(tasks), "is", "are"), listing),
	}, true
}

// sessionEnded reports whether raw ends its caller's agent session: a subagent's stop
// (a SubagentStop) or a SessionEnd. A root session's Stop ends a turn, not the session. A
// host that fires neither in print mode, or whose idle event is a pause, ends a job with its
// checkout or its deadline.
func sessionEnded(raw string) bool {
	if !strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return false
	}
	var env struct {
		HookEventName string `json:"hook_event_name"`
	}
	if json.Unmarshal([]byte(raw), &env) == nil && env.HookEventName == "SessionEnd" {
		return true
	}
	stop, ok := decodeStop(raw)
	return ok && stop.AgentID != ""
}

// endStoppedSession ends the job caller c's session held when raw ends that session and
// the guard did not refuse the stop: a refused stop leaves the agent running.
func endStoppedSession(ctx context.Context, store *job.Store, c job.Caller, raw string, v types.GuardJudgment) (bool, error) {
	if v.Decision == decisionDeny || !sessionEnded(raw) {
		return false, nil
	}
	_, ended, err := store.EndSession(ctx, c)
	return ended, err
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// describeTasks lists tasks one per line: id, start, age and the command's first line.
func describeTasks(tasks []backgroundTask, now time.Time) string {
	const commandWidth = 100
	var b strings.Builder
	for _, t := range tasks {
		command := firstLine(t.Command)
		if len(command) > commandWidth {
			command = command[:commandWidth] + "..."
		}
		fmt.Fprintf(&b, "  %s  started %s, %s ago: %s\n",
			cmp.Or(t.ID, "(no id)"), t.Started.UTC().Format("15:04:05Z"), now.Sub(t.Started).Round(time.Second), command)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// stoppableTasks lists tasks as describeTasks does, each followed by the call that stops
// it, so one reply clears them all. The call is the stop tool's shape the transcript
// reader recognizes (see stoppedTask); a task with no id has none to name.
func stoppableTasks(tasks []backgroundTask, now time.Time) string {
	lines := make([]string, 0, len(tasks))
	for _, t := range tasks {
		line := describeTasks([]backgroundTask{t}, now)
		if t.ID != "" {
			line += fmt.Sprintf("\n    stop it: TaskStop {\"task_id\": %q}", t.ID)
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// backgroundTaskKey names t's marker under kind: its id, or its start and command
// when the transcript never named one, hashed so the file name holds no command text.
func backgroundTaskKey(kind hint.MarkerKind, t backgroundTask) hint.MarkerKind {
	subject := t.ID
	if subject == "" {
		subject = t.Started.UTC().Format(time.RFC3339Nano) + "\x00" + t.Command
	}
	sum := sha256.Sum256([]byte(subject))
	return kind + "-" + hint.MarkerKind(hex.EncodeToString(sum[:6]))
}

// judgeBackgroundSilent is a root session's per-call word on its background tasks that
// have gone silent: a refusal naming each silent past backgroundSilentDeny, else advice
// naming each silent past backgroundSilentAdvise, once per task on said. A watcher with an
// expiry is left out, since the host ends it.
func judgeBackgroundSilent(transcript string, said hint.Gate, now time.Time) (denial, advice) {
	if transcript == "" {
		return denial{}, advice{}
	}
	tasks, err := liveBackgroundTasks(transcript, now)
	if err != nil {
		return denial{}, advice{}
	}
	silentFor := func(t backgroundTask) time.Duration { return now.Sub(lastHeard(t)) }
	tasks = slices.DeleteFunc(tasks, func(t backgroundTask) bool {
		return !t.Expires.IsZero() || silentFor(t) < backgroundSilentAdvise
	})
	why := "A background task's output file and event notices are the only sign it is working. One that writes nothing for half an hour is more likely waiting on a stdin nobody feeds, a lock, or a prompt than doing slow work: a `python3 -` reading an unfed stdin sat that way for eight hours. " +
		fmt.Sprintf("Past %s of silence each call is advised, once per task; past %s each call is refused until the task is stopped. ", backgroundSilentAdvise, backgroundSilentDeny) +
		"Silence is measured from the task's start, its output file's last write, and its newest event notice, whichever is latest. A watcher with an expiry is not judged: the host ends it."
	if stuck := slices.DeleteFunc(slices.Clone(tasks), func(t backgroundTask) bool { return silentFor(t) < backgroundSilentDeny }); len(stuck) > 0 {
		return denial{
			Say: fmt.Sprintf("magus workspace: %s this session ran in the background %s written nothing for over %s; stop %s before anything else:\n%s",
				plural(len(stuck), "a shell command", "shell commands"), plural(len(stuck), "has", "have"), backgroundSilentDeny, plural(len(stuck), "it", "each"), silentTasks(stuck, now)),
			Why: why,
		}, advice{}
	}
	tasks = slices.DeleteFunc(tasks, func(t backgroundTask) bool { return said.MarkFired(backgroundTaskKey(hint.MarkerKind(denyRuleBackgroundSilent), t)) })
	if len(tasks) == 0 {
		return denial{}, advice{}
	}
	return denial{}, advice{
		Say: fmt.Sprintf("magus workspace: %s this session ran in the background %s written nothing for over %s; stop any that is stuck:\n%s",
			plural(len(tasks), "a shell command", "shell commands"), plural(len(tasks), "has", "have"), backgroundSilentAdvise, silentTasks(tasks, now)),
		Why: why,
	}
}

// silentTasks lists tasks as stoppableTasks does, each with how long it has been silent.
func silentTasks(tasks []backgroundTask, now time.Time) string {
	lines := make([]string, 0, len(tasks))
	for _, t := range tasks {
		line := stoppableTasks([]backgroundTask{t}, now)
		head, rest, _ := strings.Cut(line, "\n")
		line = fmt.Sprintf("%s (silent %s)", head, now.Sub(lastHeard(t)).Round(time.Second))
		if rest != "" {
			line += "\n" + rest
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// lastHeard is when t last showed it was working: its start, its output file's last
// write, or its newest event notice.
func lastHeard(t backgroundTask) time.Time {
	last := t.Started
	if t.Heard.After(last) {
		last = t.Heard
	}
	if t.Output != "" {
		if info, err := os.Stat(t.Output); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last
}

// backgroundTask is one shell command a host runs in the background for an agent: a
// backgrounded call, or a watcher's command.
type backgroundTask struct {
	// ID is the host's task id, "" when the transcript never named one.
	ID      string
	Command string
	Started time.Time
	// Expires is when the host ends the task on its own, zero when it never does.
	Expires time.Time
	// Output is the file the host writes the task's output to, "" when it named none.
	Output string
	// Heard is the newest event notice the task sent, zero for none.
	Heard time.Time
}

// The host's words for a shell command it runs in the background: asked for, timed out
// in the foreground, or a watcher started. Read from the call's result, the one record of
// the id. Anchored, so a result that merely prints one of these (a search over
// transcripts) starts nothing.
var backgroundIDPattern = regexp.MustCompile(`^(?:Command running in background with ID: |Command did not complete[^\n]*? moved to the background \(ID: |Monitor started \(task )([A-Za-z0-9_-]+)`)

// backgroundOutputPattern is the file the same result says the task writes to.
var backgroundOutputPattern = regexp.MustCompile(`Output is being written to: (\S+)\.(?:\s|$)`)

// A task's completion notice. Only one carrying a status reports the task ended: a
// watcher's event notices carry none.
var (
	taskNoticePattern = regexp.MustCompile(`(?s)<task-notification>(.*?)</task-notification>`)
	noticeTaskID      = regexp.MustCompile(`<task-id>([^<]+)</task-id>`)
	noticeToolUseID   = regexp.MustCompile(`<tool-use-id>([^<]+)</tool-use-id>`)
	noticeStatus      = regexp.MustCompile(`<status>([^<]+)</status>`)
)

// backgroundMarkers are the bytes a transcript line that starts or ends a task holds; a
// line without one is never decoded, which keeps a root transcript of hundreds of
// megabytes inside a hook's time.
var backgroundMarkers = [][]byte{[]byte(`"command"`), []byte(`background`), []byte(`started (task `), []byte(`task-notification`), []byte(`"task_id"`), []byte(`"shell_id"`)}

var errNotATranscript = errors.New("transcript path is not absolute")

// transcriptRecord is the part of one transcript line the task reader needs.
type transcriptRecord struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	// Content is a queued notice's text, on a queue line.
	Content    any `json:"content"`
	Attachment struct {
		Prompt string `json:"prompt"`
	} `json:"attachment"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type transcriptBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Input     map[string]any  `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Text      string          `json:"text"`
}

// liveBackgroundTasks reads a JSONL transcript for the shell commands its agent ran in the
// background that are still running at now, in start order.
//
// A task starts with a call carrying a command whose result names the background task it
// became: a run_in_background call, a foreground call that timed out, or a watcher. A
// call with no such result, or an errored one, started nothing. It ends with a completion
// notice carrying a status, with a call whose whole input names it by task_id or shell_id
// (the shape of a host's stop tool; one that also waits or reads, with block or timeout,
// is not a stop), or at the expiry a watcher declared.
func liveBackgroundTasks(transcript string, now time.Time) ([]backgroundTask, error) {
	if !filepath.IsAbs(transcript) {
		return nil, errNotATranscript
	}
	f, err := os.Open(transcript)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := &taskReader{
		commands: map[string]backgroundTask{},
		live:     map[string]*backgroundTask{},
		endedIDs: map[string]bool{},
		heard:    map[string]time.Time{},
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && slices.ContainsFunc(backgroundMarkers, func(m []byte) bool { return bytes.Contains(line, m) }) {
			r.read(line)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return r.result(now), nil
}

// taskReader folds transcript lines into the tasks still running.
type taskReader struct {
	// commands is every shell call by its call id, for a result that moves it to the background.
	commands map[string]backgroundTask
	// live is every backgrounded call by its call id, in order.
	live  map[string]*backgroundTask
	order []string
	// endedIDs holds the task ids a notice or a stop ended.
	endedIDs map[string]bool
	// heard is each task id's newest event notice.
	heard map[string]time.Time
}

func (r *taskReader) read(line []byte) {
	var rec transcriptRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, rec.Timestamp)
	r.notices(rec.Attachment.Prompt, at)
	if s, ok := rec.Content.(string); ok {
		r.notices(s, at)
	}
	var text string
	if json.Unmarshal(rec.Message.Content, &text) == nil {
		if rec.Type != "assistant" {
			r.notices(text, at)
		}
		return
	}
	var blocks []transcriptBlock
	if json.Unmarshal(rec.Message.Content, &blocks) != nil {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			r.call(b, at)
		case "tool_result":
			r.callResult(b)
		case "text":
			// An agent quoting a notice in its own reply ended nothing.
			if rec.Type != "assistant" {
				r.notices(b.Text, at)
			}
		}
	}
}

func (r *taskReader) call(b transcriptBlock, at time.Time) {
	if id, ok := stoppedTask(b.Input); ok {
		r.endedIDs[id] = true
		return
	}
	command, ok := b.Input["command"].(string)
	if !ok || command == "" {
		return
	}
	task := backgroundTask{Command: command, Started: at}
	// A watcher names how long it runs; one marked persistent runs until stopped.
	if ms, ok := b.Input["timeout_ms"].(float64); ok && ms > 0 && !truthy(b.Input["persistent"]) {
		task.Expires = at.Add(time.Duration(ms) * time.Millisecond)
	}
	r.commands[b.ID] = task
}

// truthy reads a boolean a host may send as a JSON boolean or as a string.
func truthy(v any) bool {
	return v == true || v == "true"
}

// stoppedTask is the task a stop call names: an input holding task_id or shell_id and
// nothing else.
func stoppedTask(input map[string]any) (string, bool) {
	var id string
	for key, value := range input {
		s, ok := value.(string)
		if !ok || (key != "task_id" && key != "shell_id") {
			return "", false
		}
		id = cmp.Or(id, s)
	}
	return id, id != ""
}

func (r *taskReader) start(callID string, task backgroundTask) {
	if _, ok := r.live[callID]; ok {
		return
	}
	r.live[callID] = &task
	r.order = append(r.order, callID)
}

// callResult reads a call's result for the id the host gave its background task.
// An errored result starts nothing, whatever id it quotes.
func (r *taskReader) callResult(b transcriptBlock) {
	if b.IsError {
		return
	}
	text := blockText(b.Content)
	m := backgroundIDPattern.FindStringSubmatch(text)
	if m == nil {
		return
	}
	if _, ok := r.live[b.ToolUseID]; !ok {
		task, known := r.commands[b.ToolUseID]
		if !known {
			return
		}
		r.start(b.ToolUseID, task)
	}
	r.live[b.ToolUseID].ID = m[1]
	if out := backgroundOutputPattern.FindStringSubmatch(text); out != nil {
		r.live[b.ToolUseID].Output = out[1]
	}
}

// blockText is a result's text, whether the host wrote it as a string or as text blocks.
func blockText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []transcriptBlock
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func (r *taskReader) notices(text string, at time.Time) {
	if !strings.Contains(text, "<task-notification>") {
		return
	}
	for _, m := range taskNoticePattern.FindAllStringSubmatch(text, -1) {
		if noticeStatus.FindStringSubmatch(m[1]) == nil {
			// A watcher's event: the task is alive and was heard from.
			if id := noticeTaskID.FindStringSubmatch(m[1]); id != nil {
				if key := strings.TrimSpace(id[1]); at.After(r.heard[key]) {
					r.heard[key] = at
				}
			}
			continue
		}
		if id := noticeTaskID.FindStringSubmatch(m[1]); id != nil {
			r.endedIDs[strings.TrimSpace(id[1])] = true
		}
		if use := noticeToolUseID.FindStringSubmatch(m[1]); use != nil {
			delete(r.live, strings.TrimSpace(use[1]))
		}
	}
}

func (r *taskReader) result(now time.Time) []backgroundTask {
	var out []backgroundTask
	for _, callID := range r.order {
		t, ok := r.live[callID]
		if !ok || (t.ID != "" && r.endedIDs[t.ID]) || (!t.Expires.IsZero() && !now.Before(t.Expires)) {
			continue
		}
		task := *t
		task.Heard = r.heard[t.ID]
		out = append(out, task)
	}
	return out
}

// denyUnboundedBackground refuses a shell command the host runs in the background when
// nothing in the line ends it: a backgrounded command outlives the agent that started it,
// and the host stops it only when asked. `magus job exec --timeout D -- cmd` bounds it, and
// so does a `timeout N` in front; a magus target run is bounded by its target's timeout. A
// line that does not parse is left to the host, which will not run it. The refusal names
// the bound; it never rewrites the command.
func denyUnboundedBackground(command string, background bool, d Dialect) denial {
	if !background {
		return denial{}
	}
	f, err := parseFile(command, effectiveDialect(d))
	if err != nil {
		return denial{}
	}
	program := unboundedStmts(f.Stmts, d)
	if program == "" {
		return denial{}
	}
	ceiling := fmt.Sprintf("%dm", int(backgroundCeiling.Minutes()))
	return denial{
		Say: fmt.Sprintf("magus workspace: `%s` runs in the background unbounded; bound it at the default ceiling: `timeout %s <command>` or `magus job exec --timeout %s -- <command>`.", program, ceiling, ceiling),
		Why: "A backgrounded command outlives the agent that started it: the host stops it only when asked, and an agent that finishes first leaves it running with nobody told. " +
			"`magus job exec --timeout D -- cmd` runs it in its own process group under the broker, which names its owner (your lease) in `magus status`, stops the whole group at the deadline or when the wrapper dies, and files one attention row when it does. " +
			"`timeout N` or `gtimeout N` in front of each command that can run long also bounds it; stock macOS ships neither, and Homebrew's coreutils installs `gtimeout`. " +
			"timeout signals its own process group, so a child that starts its own session (setsid, a daemon) escapes it, and `--foreground` narrows it to the direct child; `--kill-after` sends KILL to a command that ignores TERM. " +
			"A magus target run is bounded by its target's timeout and passes, and so do builtins that return at once (cd, export, echo). A pipeline is bounded by its first command, since the rest end with its output. Running the command in the foreground passes too, since the host times a foreground call out.",
	}
}

// unboundedStmts is the first program in stmts nothing bounds, "" when every one is bounded.
func unboundedStmts(stmts []*syntax.Stmt, d Dialect) string {
	for _, s := range stmts {
		if p := unboundedStmt(s, d); p != "" {
			return p
		}
	}
	return ""
}

func unboundedStmt(s *syntax.Stmt, d Dialect) string {
	if s == nil || s.Cmd == nil {
		return ""
	}
	switch c := s.Cmd.(type) {
	case *syntax.CallExpr:
		return unboundedCall(literalWords(c.Args), d)
	case *syntax.BinaryCmd:
		if c.Op == syntax.Pipe || c.Op == syntax.PipeAll {
			// The later stages read the first one's output and end with it.
			return unboundedStmt(c.X, d)
		}
		return cmp.Or(unboundedStmt(c.X, d), unboundedStmt(c.Y, d))
	case *syntax.Subshell:
		return unboundedStmts(c.Stmts, d)
	case *syntax.Block:
		return unboundedStmts(c.Stmts, d)
	case *syntax.IfClause:
		for clause := c; clause != nil; clause = clause.Else {
			if p := cmp.Or(unboundedStmts(clause.Cond, d), unboundedStmts(clause.Then, d)); p != "" {
				return p
			}
		}
		return ""
	case *syntax.ForClause:
		if _, ok := c.Loop.(*syntax.WordIter); ok {
			return unboundedStmts(c.Do, d)
		}
		return "for"
	case *syntax.WhileClause:
		if c.Until {
			return "until"
		}
		return "while"
	case *syntax.CaseClause:
		for _, item := range c.Items {
			if p := unboundedStmts(item.Stmts, d); p != "" {
				return p
			}
		}
		return ""
	case *syntax.TimeClause:
		return unboundedStmt(c.Stmt, d)
	case *syntax.DeclClause, *syntax.TestClause, *syntax.ArithmCmd, *syntax.LetClause, *syntax.FuncDecl:
		return ""
	default:
		return "the line"
	}
}

// instantBuiltins return at once, so they need no bound.
var instantBuiltins = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "export": true, "unset": true, "set": true,
	"shopt": true, "true": true, "false": true, ":": true, "echo": true, "printf": true,
	"pwd": true, "test": true, "[": true, "umask": true, "alias": true, "type": true,
	"hash": true,
}

// timeoutDuration is the duration operand timeout(1) takes: a number with an optional
// s, m, h or d suffix.
var timeoutDuration = regexp.MustCompile(`^(?:[0-9]+\.?[0-9]*|\.[0-9]+)[smhd]?$`)

// unboundedCall is the program words runs that nothing bounds, "" when it is bounded.
func unboundedCall(words []string, d Dialect) string {
	if len(words) == 0 {
		return ""
	}
	name := path.Base(words[0])
	switch {
	case words[0] == "":
		return "a command named by a parameter"
	case name == "timeout" || name == "gtimeout":
		if boundedByTimeout(words[1:]) {
			return ""
		}
		return name
	case shells[name]:
		if script, ok := shellDashC(words[1:]); ok {
			return unboundedLine(script, wrapperShellDialect(name, d))
		}
		return name
	case name == "eval":
		return unboundedLine(strings.Join(words[1:], " "), d)
	case name == "magus":
		inv := peelWrappers(words, d)[0]
		if _, ok := parseTargetRun(inv); ok || boundedByJobExec(inv.Args) {
			return ""
		}
		return name
	case wrappers[name]:
		rest := skipWrapperArgs(name, words[1:])
		if len(rest) == 0 {
			return ""
		}
		return unboundedCall(rest, d)
	case instantBuiltins[name]:
		return ""
	}
	return name
}

// boundedByTimeout reports whether timeout's arguments give it a nonzero duration and a
// program to run.
func boundedByTimeout(args []string) bool {
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if wrapperValueFlags[args[0]] && len(args) > 1 {
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	if len(args) < 2 || !timeoutDuration.MatchString(args[0]) {
		return false
	}
	return strings.Trim(strings.TrimRight(args[0], "smhd"), "0.") != ""
}

// boundedByJobExec reports whether magus's arguments are `job exec --timeout D -- cmd`
// with a positive D and a command after the separator. Global flags may come first.
func boundedByJobExec(args []string) bool {
	sep := slices.Index(args, "--")
	if sep < 0 || sep == len(args)-1 {
		return false
	}
	head := args[:sep]
	verb := slices.Index(head, "job")
	if verb < 0 || verb+1 >= len(head) || head[verb+1] != "exec" {
		return false
	}
	for i, a := range head {
		name, value, joined := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") || name != "timeout" {
			continue
		}
		if !joined && i+1 < len(head) {
			value = head[i+1]
		}
		d, err := time.ParseDuration(value)
		return err == nil && d > 0
	}
	return false
}

// unboundedLine parses script and reports its first unbounded program; a script that does
// not parse reports itself.
func unboundedLine(script string, d Dialect) string {
	f, err := parseFile(script, effectiveDialect(d))
	if err != nil {
		return "the script"
	}
	return unboundedStmts(f.Stmts, d)
}
