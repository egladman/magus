package bench

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/benchmarks/agent/internal/pycompat"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/pricing"
)

// Tool names whose call counts feed file_reads / re_read_rate.
var readTools = map[string]bool{"Read": true, "NotebookRead": true}

var testFileMarkers = []string{"_test.", ".test."}

var requiredMeta = []string{"run_id", "arm", "task", "rep", "model"}

// usageCounts are the five billable counters of one assistant turn, or their
// sum over a model.
type usageCounts struct {
	input, output, cacheRead, cacheWrite5m, cacheWrite1h int64
}

func (u usageCounts) add(o usageCounts) usageCounts {
	return usageCounts{
		u.input + o.input, u.output + o.output, u.cacheRead + o.cacheRead,
		u.cacheWrite5m + o.cacheWrite5m, u.cacheWrite1h + o.cacheWrite1h,
	}
}

func (u usageCounts) cost(r pricing.Rates) float64 {
	total := 0.0
	total += float64(u.input) * r.Input / pricing.TokensPerPriceUnit
	total += float64(u.output) * r.Output / pricing.TokensPerPriceUnit
	total += float64(u.cacheRead) * r.CacheRead / pricing.TokensPerPriceUnit
	total += float64(u.cacheWrite5m) * r.CacheWrite5m / pricing.TokensPerPriceUnit
	total += float64(u.cacheWrite1h) * r.CacheWrite1h / pricing.TokensPerPriceUnit
	return total
}

func (u usageCounts) tokens() TokenCounts {
	cacheWrite := u.cacheWrite5m + u.cacheWrite1h
	return TokenCounts{
		Input: u.input, Output: u.output, CacheRead: u.cacheRead, CacheWrite: cacheWrite,
		TotalBilled: u.input + u.output + u.cacheRead + cacheWrite,
	}
}

// priceUsage costs the per-model token totals. An unpriced model stops the run.
func priceUsage(byModel map[string]usageCounts, table pricing.Table, runID string) (float64, error) {
	total := 0.0
	for _, model := range slices.Sorted(maps.Keys(byModel)) {
		rates, err := table.Lookup(model)
		if err != nil {
			return 0, fmt.Errorf("%s: %w", runID, err)
		}
		total += byModel[model].cost(rates)
	}
	return total, nil
}

// contentText flattens a message/tool_result content field to the text a
// reader would see.
func contentText(content any) (string, error) {
	switch c := content.(type) {
	case nil:
		return "", nil
	case string:
		return c, nil
	case []any:
		parts := make([]string, len(c))
		for i, block := range c {
			text, err := blockText(block)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, "\n"), nil
	}
	raw, err := pycompat.Marshal(content, 0)
	return string(raw), err
}

func blockText(block any) (string, error) {
	switch b := block.(type) {
	case string:
		return b, nil
	case map[string]any:
		switch b["type"] {
		case "text":
			text, _ := b["text"].(string)
			return text, nil
		case "image":
			return "[image]", nil
		}
		raw, err := pycompat.Marshal(b, 0)
		return string(raw), err
	}
	return "", nil
}

func intField(m map[string]any, key string) (int64, bool) {
	n, ok := m[key].(pycompat.Number)
	if !ok {
		return 0, false
	}
	return n.Int64()
}

// intOrZero is `usage.get(key) or 0` for the counters a transcript may omit.
func intOrZero(m map[string]any, key string) int64 {
	i, _ := intField(m, key)
	return i
}

func stringField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

// splitUsage pulls the token counters out of one turn's usage; the flag says
// the TTL was guessed.
func splitUsage(usage map[string]any, runID string, index int64) (usageCounts, bool, error) {
	for _, key := range []string{"input_tokens", "output_tokens"} {
		if _, ok := intField(usage, key); !ok {
			return usageCounts{}, false, fmt.Errorf("%s: assistant record %d has no %s; refusing to report zero tokens", runID, index, key)
		}
	}
	var write5m, write1h int64
	if breakdown, ok := usage["cache_creation"].(map[string]any); ok {
		write5m = intOrZero(breakdown, "ephemeral_5m_input_tokens")
		write1h = intOrZero(breakdown, "ephemeral_1h_input_tokens")
	}
	ttlAssumed := false
	if write5m == 0 && write1h == 0 {
		// Only the flat counter is present, so the TTL is unknowable; 5m is the
		// API default and the cheaper of the two, which keeps the bill a floor.
		write5m = intOrZero(usage, "cache_creation_input_tokens")
		ttlAssumed = write5m > 0
	}
	counts := usageCounts{
		input:        intOrZero(usage, "input_tokens"),
		output:       intOrZero(usage, "output_tokens"),
		cacheRead:    intOrZero(usage, "cache_read_input_tokens"),
		cacheWrite5m: write5m,
		cacheWrite1h: write1h,
	}
	return counts, ttlAssumed, nil
}

// transcript is what one transcript.jsonl measures: tokens per model, turns,
// tools, reads. It is filled one record at a time by take; the seen sets are
// how a message streamed as several records counts once.
type transcript struct {
	runID, defaultModel, initModel string
	byModel                        map[string]usageCounts
	seenMessages                   map[string]bool
	seenToolUses                   map[string]bool
	turns                          int64
	cacheWriteTTLAssumed           bool
	toolCallsByName                map[string]int64
	readPaths                      []string
	toolResultBytes                int64
	reportedCostUSD                *float64
	resultOutput                   *int64
}

func (t *transcript) tokens() TokenCounts {
	var sum usageCounts
	for _, counts := range t.byModel {
		sum = sum.add(counts)
	}
	return sum.tokens()
}

func (t *transcript) toolCalls() int64 {
	var n int64
	for _, c := range t.toolCallsByName {
		n += c
	}
	return n
}

func (t *transcript) distinctFilesRead() int64 {
	seen := map[string]bool{}
	for _, p := range t.readPaths {
		seen[p] = true
	}
	return int64(len(seen))
}

func (t *transcript) reReadRate() float64 {
	reads := int64(len(t.readPaths))
	if reads == 0 {
		return 0.0
	}
	return float64(reads-t.distinctFilesRead()) / float64(reads)
}

func (t *transcript) take(rec any) error {
	m, ok := rec.(map[string]any)
	if !ok {
		return nil
	}
	switch m["type"] {
	case "system":
		if m["subtype"] == "init" {
			if model, _ := m["model"].(string); model != "" {
				t.initModel = model
			}
		}
	case "result":
		if cost, ok := m["total_cost_usd"].(pycompat.Number); ok {
			c := cost.Float64()
			t.reportedCostUSD = &c
		}
		if usage, ok := m["usage"].(map[string]any); ok {
			if out, ok := intField(usage, "output_tokens"); ok {
				t.resultOutput = &out
			}
		}
	case "assistant":
		if message, ok := m["message"].(map[string]any); ok {
			return t.takeAssistant(message)
		}
	case "user":
		if message, ok := m["message"].(map[string]any); ok {
			return t.takeUser(message)
		}
	}
	return nil
}

func (t *transcript) takeAssistant(message map[string]any) error {
	// The host streams one assistant record per content block, all carrying
	// the message id, so the blocks are walked on every record and only the
	// turn and its usage are counted once per id. Deduplicating the whole
	// record threw away every tool call after a message's first block; the
	// 2026-09-10 pilot reported 2 tool calls on a run that made 14.
	for _, block := range blocks(message) {
		if block["type"] == "tool_use" {
			t.takeToolUse(block)
		}
	}
	if id, ok := message["id"]; ok && id != nil {
		key, err := pycompat.Marshal(id, 0)
		if err != nil {
			return err
		}
		if t.seenMessages[string(key)] {
			return nil
		}
		t.seenMessages[string(key)] = true
	}
	t.turns++
	usage, ok := message["usage"].(map[string]any)
	if !ok {
		return fmt.Errorf("%s: assistant record %d has no message.usage; refusing to report zero tokens", t.runID, t.turns)
	}
	counts, assumed, err := splitUsage(usage, t.runID, t.turns)
	if err != nil {
		return err
	}
	t.cacheWriteTTLAssumed = t.cacheWriteTTLAssumed || assumed
	model, _ := message["model"].(string)
	if model == "" {
		model = t.initModel
	}
	if model == "" {
		model = t.defaultModel
	}
	if model == "" {
		return fmt.Errorf("%s: assistant record %d has no model", t.runID, t.turns)
	}
	t.byModel[model] = t.byModel[model].add(counts)
	return nil
}

// takeToolUse counts one tool call, once per block id: a block is repeated
// across the records of its message the same way the message id is.
func (t *transcript) takeToolUse(block map[string]any) {
	if id, _ := block["id"].(string); id != "" {
		if t.seenToolUses[id] {
			return
		}
		t.seenToolUses[id] = true
	}
	name, _ := block["name"].(string)
	if name == "" {
		name = "unknown"
	}
	t.toolCallsByName[name]++
	if readTools[name] {
		args, _ := block["input"].(map[string]any)
		p, _ := args["file_path"].(string)
		if p == "" {
			p, _ = args["notebook_path"].(string)
		}
		t.readPaths = append(t.readPaths, p)
	}
}

func (t *transcript) takeUser(message map[string]any) error {
	for _, block := range blocks(message) {
		if block["type"] == "tool_result" {
			text, err := contentText(block["content"])
			if err != nil {
				return err
			}
			t.toolResultBytes += int64(utf8.RuneCountInString(text))
		}
	}
	return nil
}

// billedOutput replaces the summed output tokens with the result record's.
// An assistant record's usage carries the output of one streamed chunk, not
// the turn, so their sum runs a whole run's output at a twentieth of what the
// host bills (118 summed against 2628 in the result record, pilot 2026-09-10).
// The input and cache counters agree between the two, so only output is
// replaced, apportioned across models by the share each summed to.
func (t *transcript) billedOutput() map[string]usageCounts {
	if t.resultOutput == nil {
		return t.byModel
	}
	var summed int64
	for _, counts := range t.byModel {
		summed += counts.output
	}
	billed := map[string]usageCounts{}
	for model, counts := range t.byModel {
		var share float64
		if summed != 0 {
			share = float64(counts.output) / float64(summed)
		} else {
			share = 1.0 / float64(len(t.byModel))
		}
		counts.output = int64(math.RoundToEven(float64(*t.resultOutput) * share))
		billed[model] = counts
	}
	return billed
}

func blocks(message map[string]any) []map[string]any {
	content, _ := message["content"].([]any)
	var out []map[string]any
	for _, block := range content {
		if b, ok := block.(map[string]any); ok {
			out = append(out, b)
		}
	}
	return out
}

// jsonlLines yields r's lines split on newlines, including a final
// unterminated one, without a cap on line length: a tool result can run to
// megabytes on one line.
func jsonlLines(r io.Reader, visit func(line []byte) error) error {
	reader := bufio.NewReader(r)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			if verr := visit(line); verr != nil {
				return verr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// readTranscript walks a stream-json transcript into token, tool and turn
// counts. It fails when no assistant record carries usage, or when one
// carries a usage block missing a token counter. Both cases are the vacuous
// metrics defect: silence there reads as a free run.
func readTranscript(file, runID, defaultModel string) (*transcript, error) {
	fh, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: transcript.jsonl is missing", runID)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: transcript.jsonl: %w", runID, err)
	}
	defer fh.Close()
	t := &transcript{
		runID: runID, defaultModel: defaultModel,
		byModel: map[string]usageCounts{}, seenMessages: map[string]bool{}, seenToolUses: map[string]bool{},
		toolCallsByName: map[string]int64{},
	}
	err = jsonlLines(fh, func(line []byte) error {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		rec, err := pycompat.Unmarshal(line)
		if err != nil {
			return fmt.Errorf("%s: malformed transcript.jsonl line: %w", runID, err)
		}
		return t.take(rec)
	})
	if err != nil {
		return nil, err
	}
	if t.turns == 0 {
		return nil, fmt.Errorf("%s: transcript carries no assistant records", runID)
	}
	t.byModel = t.billedOutput()
	return t, nil
}

// readGuardEvents counts guard denials, advisories and skill loads in the
// magus activity trail. captured is false when no trail exists; the caller
// must keep that null rather than substituting zero, which would read as a
// guard that never fired. A trail that exists but cannot be read is an error,
// not a third thing folded into null. A malformed line is skipped, not fatal.
func readGuardEvents(file string) (events GuardEvents, captured bool, err error) {
	fh, err := os.Open(file)
	if errors.Is(err, os.ErrNotExist) {
		return GuardEvents{}, false, nil
	}
	if err != nil {
		return GuardEvents{}, false, err
	}
	defer fh.Close()
	err = jsonlLines(fh, func(line []byte) error {
		doc, unmarshalErr := pycompat.Unmarshal(line)
		if unmarshalErr != nil {
			return nil //nolint:nilerr // a malformed line is skipped by design; the rest of the trail still counts
		}
		event, ok := doc.(map[string]any)
		if !ok || event["kind"] != "agent_command" {
			return nil
		}
		action := strings.ToLower(stringField(event, "action"))
		preview := strings.ToLower(stringField(event, "preview"))
		target := stringField(event, "path")
		if target == "" {
			target = stringField(event, "target")
		}
		target = strings.ToLower(target)
		if strings.Contains(preview, "guard: deny") {
			events.Denials++
		} else if strings.Contains(preview, "guard: advis") {
			events.Advisories++
		}
		if strings.HasPrefix(action, "skill.") || (action == "file.read" && strings.Contains(target, "/skills/")) {
			events.SkillLoads++
		}
		return nil
	})
	if err != nil {
		return GuardEvents{}, false, err
	}
	return events, true, nil
}

// readInvariantViolations finds deleted test files in a unified diff.
// Deletion is the only deterministic tell.
func readInvariantViolations(file string) (InvariantViolations, error) {
	raw, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return InvariantViolations{}, nil
	}
	if err != nil {
		return InvariantViolations{}, err
	}
	var deleted []string
	current := ""
	for _, line := range strings.SplitAfter(string(raw), "\n") {
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			current = ""
			if len(parts) >= 4 {
				// The token is "a/<path>"; the prefix is two ASCII bytes.
				current = parts[2][2:]
			}
		} else if strings.HasPrefix(line, "deleted file mode") && current != "" {
			name := path.Base(current)
			for _, marker := range testFileMarkers {
				if strings.Contains(name, marker) {
					deleted = append(deleted, current)
					break
				}
			}
		}
	}
	slices.Sort(deleted)
	return InvariantViolations{TestsDeleted: deleted}, nil
}

// readCheckExit reads check.exit as the run's outcome. Absent or unreadable
// means unknown, not failed.
func readCheckExit(runDir string) *int64 {
	raw, err := os.ReadFile(filepath.Join(runDir, "check.exit"))
	if err != nil {
		return nil
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return nil
	}
	code, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return nil
	}
	return &code
}

type timing struct {
	WallMs            *pycompat.Number `json:"wall_ms"`
	TimeToFirstEditMs *pycompat.Number `json:"time_to_first_edit_ms"`
	TimeToDoneMs      *pycompat.Number `json:"time_to_done_ms"`
}

func readTiming(runDir string) (timing, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, "timing.json"))
	if errors.Is(err, os.ErrNotExist) {
		return timing{}, nil
	}
	if err != nil {
		return timing{}, err
	}
	var t timing
	if err := json.Unmarshal(raw, &t); err != nil {
		return timing{}, fmt.Errorf("%s: timing.json: %w", runDir, err)
	}
	return t, nil
}

// readMeta returns meta.json's bytes and its control kind ("" for a scored
// run) once the keys every run needs are present and non-empty. rep is read
// as a Number so a float names the key at fault instead of failing the whole
// decode into the record's int64.
func readMeta(runDir string) (raw []byte, control string, err error) {
	raw, err = os.ReadFile(filepath.Join(runDir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("%s: meta.json is missing", runDir)
	}
	if err != nil {
		return nil, "", err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, "", fmt.Errorf("%s: meta.json: %w", runDir, err)
	}
	for _, name := range requiredMeta {
		if v := string(fields[name]); v == "" || v == "null" || v == `""` {
			return nil, "", fmt.Errorf("%s: meta.json has no %s", runDir, name)
		}
	}
	var rep pycompat.Number
	if err := json.Unmarshal(fields["rep"], &rep); err != nil {
		return nil, "", fmt.Errorf("%s: meta.json rep: %w", runDir, err)
	}
	if _, ok := rep.Int64(); !ok {
		return nil, "", fmt.Errorf("%s: meta.json rep is not an int", runDir)
	}
	if kind, ok := fields["control"]; ok {
		if err := json.Unmarshal(kind, &control); err != nil {
			return nil, "", fmt.Errorf("%s: meta.json control: %w", runDir, err)
		}
	}
	return raw, control, nil
}

// extractRun builds one metrics record for a single run directory.
//
// A control run (golden or null) yields a record with its identity, its
// control kind and its check verdict and nothing else: no agent ran, so there
// is no transcript to measure. The report reads those verdicts to say whether
// the task's check discriminates at all. A scored run without a transcript is
// still an error.
func extractRun(runDir string, table pricing.Table) (RunRecord, error) {
	raw, control, err := readMeta(runDir)
	if err != nil {
		return RunRecord{}, err
	}
	checkExit := readCheckExit(runDir)
	var success *bool
	if checkExit != nil {
		s := *checkExit == 0
		success = &s
	}
	if control != "" {
		if control != controlGolden && control != controlNull {
			return RunRecord{}, fmt.Errorf("%s: meta.json control %q is not golden or null", runDir, control)
		}
		c := &ControlRun{}
		if err := json.Unmarshal(raw, c); err != nil {
			return RunRecord{}, fmt.Errorf("%s: meta.json: %w", runDir, err)
		}
		c.CheckExit, c.Success = checkExit, success
		return RunRecord{Control: c}, nil
	}

	run := &ScoredRun{}
	if err := json.Unmarshal(raw, run); err != nil {
		return RunRecord{}, fmt.Errorf("%s: meta.json: %w", runDir, err)
	}
	// The runner writes control as "" for a scored run; the row's key is null.
	run.Control = nil
	tr, err := readTranscript(filepath.Join(runDir, "transcript.jsonl"), run.RunID, run.Model)
	if err != nil {
		return RunRecord{}, err
	}
	tableDollars, err := priceUsage(tr.byModel, table, run.RunID)
	if err != nil {
		return RunRecord{}, err
	}
	guard, captured, err := readGuardEvents(filepath.Join(runDir, "activity", "events.jsonl"))
	if err != nil {
		return RunRecord{}, err
	}
	violations, err := readInvariantViolations(filepath.Join(runDir, "final.diff"))
	if err != nil {
		return RunRecord{}, err
	}
	times, err := readTiming(runDir)
	if err != nil {
		return RunRecord{}, err
	}
	run.Tokens = tr.tokens()
	// The table is the basis, and the host's total_cost_usd is recorded beside
	// it rather than over it. That figure is a CLIENT-SIDE estimate from a price
	// table compiled into the CLI binary, which carries no entry for the opus-5
	// or sonnet-5 aliases and falls back to its default model's rates for both.
	// The 2026-09-10 pilot's divergence was mix-invariant (0.665x on sonnet-5,
	// 1.663x on opus-5), and reconstructing the implied rate vector from each
	// model independently yields the same one, 3.00/15.00/0.30/3.75 per MTok.
	// Those constants are 2/3 and 5/3, whose ratio is exactly the opus:sonnet
	// list-price ratio. A mix-invariant constant can only come from a
	// proportional rate vector, never from a token miscount, so the host's
	// number is not a bill and the table is not the thing that is wrong.
	run.Dollars = tableDollars
	run.TableDollarsUSD = tableDollars
	run.ReportedCostUSD = tr.reportedCostUSD
	run.CacheWriteTTLAssumed = tr.cacheWriteTTLAssumed
	run.Turns = tr.turns
	run.ToolCalls = tr.toolCalls()
	run.ToolCallsByName = tr.toolCallsByName
	run.FileReads = int64(len(tr.readPaths))
	run.DistinctFilesRead = tr.distinctFilesRead()
	run.ReReadRate = tr.reReadRate()
	run.ToolResultBytes = tr.toolResultBytes
	if captured {
		run.GuardEvents = &guard
	}
	run.CheckExit, run.Success = checkExit, success
	run.InvariantViolations = violations
	run.WallMs, run.TimeToFirstEditMs, run.TimeToDoneMs = times.WallMs, times.TimeToFirstEditMs, times.TimeToDoneMs
	return RunRecord{Scored: run}, nil
}

// Extract measures every run under results, sorted by run id. A tree of
// only controls is an error.
func Extract(results string, table pricing.Table) ([]RunRecord, error) {
	entries, err := os.ReadDir(results)
	if err != nil {
		return nil, err
	}
	// A run directory is one that carries meta.json; anything else under the
	// tree (a stray file, a checkout, a scratch directory) is not a run and is
	// left alone rather than failing the whole extraction.
	var runDirs []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(results, entry.Name())
		if _, err := os.Stat(filepath.Join(dir, "meta.json")); err == nil {
			runDirs = append(runDirs, dir)
		}
	}
	if len(runDirs) == 0 {
		return nil, fmt.Errorf("no run directories under %s", results)
	}
	records := make([]RunRecord, 0, len(runDirs))
	controls := 0
	for _, dir := range runDirs {
		record, err := extractRun(dir, table)
		if err != nil {
			return nil, err
		}
		if record.Control != nil {
			controls++
		}
		records = append(records, record)
	}
	if controls == len(records) {
		return nil, fmt.Errorf("no scored runs under %s (%d control runs)", results, controls)
	}
	slices.SortFunc(records, func(a, b RunRecord) int { return strings.Compare(a.ID(), b.ID()) })
	return records, nil
}

// ID is the run id of whichever record kind is set.
func (r RunRecord) ID() string {
	if r.Control != nil {
		return r.Control.RunID
	}
	return r.Scored.RunID
}

// JSON is one metrics.jsonl line: whichever record kind is set, in
// json.dumps form without the newline.
func (r RunRecord) JSON() ([]byte, error) {
	if r.Control != nil {
		return pycompat.Marshal(r.Control, 0)
	}
	return pycompat.Marshal(r.Scored, 0)
}
