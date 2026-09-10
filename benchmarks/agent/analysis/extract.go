package analysis

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/benchmarks/agent/analysis/pycompat"
)

const usdPerToken = 1_000_000.0

// A transcript records the dated model id the API served
// (claude-opus-5-20260301); the pricing table is keyed by alias, so the date
// is stripped before lookup.
var datedModelSuffix = regexp.MustCompile(`-\d{8}$`)

// Tool names whose call counts feed file_reads / re_read_rate.
var readTools = map[string]bool{"Read": true, "NotebookRead": true}

var testFileMarkers = []string{"_test.", ".test."}

var requiredMeta = []string{"run_id", "arm", "task", "rep", "model"}

// Rates are USD per million tokens for one model, in the pricing table's units.
type Rates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
}

// PriceTable prices tokens per model; it is the only source of prices.
type PriceTable struct {
	Models map[string]Rates
}

// Rates looks a model up by its id, then by the id with its date stripped.
func (t PriceTable) Rates(model string) (Rates, bool) {
	if r, ok := t.Models[model]; ok {
		return r, true
	}
	r, ok := t.Models[datedModelSuffix.ReplaceAllString(model, "")]
	return r, ok
}

// LoadPricing reads the per-model price table. A model must carry all five
// rates; a table with no models is an error.
func LoadPricing(file string) (PriceTable, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return PriceTable{}, err
	}
	doc, err := pycompat.Unmarshal(raw)
	if err != nil {
		return PriceTable{}, fmt.Errorf("pricing table %s: %w", file, err)
	}
	table, _ := doc.(map[string]any)
	models, _ := table["models"].(map[string]any)
	if len(models) == 0 {
		return PriceTable{}, fmt.Errorf("pricing table %s has no models", file)
	}
	out := PriceTable{Models: map[string]Rates{}}
	for model, v := range models {
		fields, _ := v.(map[string]any)
		var rates Rates
		for name, dst := range map[string]*float64{
			"input": &rates.Input, "output": &rates.Output, "cache_read": &rates.CacheRead,
			"cache_write_5m": &rates.CacheWrite5m, "cache_write_1h": &rates.CacheWrite1h,
		} {
			n, ok := fields[name].(Number)
			if !ok {
				return PriceTable{}, fmt.Errorf("pricing table %s: model %s lacks the five rates: no %s", file, model, name)
			}
			*dst = n.Float64()
		}
		out.Models[model] = rates
	}
	return out, nil
}

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

func (u usageCounts) cost(r Rates) float64 {
	total := 0.0
	total += float64(u.input) * r.Input / usdPerToken
	total += float64(u.output) * r.Output / usdPerToken
	total += float64(u.cacheRead) * r.CacheRead / usdPerToken
	total += float64(u.cacheWrite5m) * r.CacheWrite5m / usdPerToken
	total += float64(u.cacheWrite1h) * r.CacheWrite1h / usdPerToken
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
func priceUsage(byModel map[string]usageCounts, pricing PriceTable, runID string) (float64, error) {
	total := 0.0
	for _, model := range sortedKeys(byModel) {
		rates, ok := pricing.Rates(model)
		if !ok {
			return 0, fmt.Errorf("%s: model '%s' is absent from the pricing table; add its published prices", runID, model)
		}
		total += byModel[model].cost(rates)
	}
	return total, nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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
	n, ok := m[key].(Number)
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
// tools, reads.
type transcript struct {
	byModel              map[string]usageCounts
	cacheWriteTTLAssumed bool
	turns                int64
	toolCallsByName      map[string]int64
	readPaths            []string
	toolResultBytes      int64
	reportedCostUSD      *float64
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

// transcriptTally is the accumulator behind readTranscript; one method per
// record kind.
type transcriptTally struct {
	runID        string
	defaultModel string
	byModel      map[string]usageCounts
	modelOrder   []string
	seenMessages map[string]bool
	turns        int64
	ttlAssumed   bool
	toolCalls    map[string]int64
	readPaths    []string
	resultBytes  int64
	initModel    string
	reportedCost *float64
	resultOutput *int64
}

func (t *transcriptTally) take(rec any) error {
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
		if cost, ok := m["total_cost_usd"].(Number); ok {
			c := cost.Float64()
			t.reportedCost = &c
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

func (t *transcriptTally) takeAssistant(message map[string]any) error {
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
	t.ttlAssumed = t.ttlAssumed || assumed
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
	if _, seen := t.byModel[model]; !seen {
		t.modelOrder = append(t.modelOrder, model)
	}
	t.byModel[model] = t.byModel[model].add(counts)
	for _, block := range blocks(message) {
		if block["type"] == "tool_use" {
			t.takeToolUse(block)
		}
	}
	return nil
}

func (t *transcriptTally) takeToolUse(block map[string]any) {
	name, _ := block["name"].(string)
	if name == "" {
		name = "unknown"
	}
	t.toolCalls[name]++
	if readTools[name] {
		args, _ := block["input"].(map[string]any)
		p, _ := args["file_path"].(string)
		if p == "" {
			p, _ = args["notebook_path"].(string)
		}
		t.readPaths = append(t.readPaths, p)
	}
}

func (t *transcriptTally) takeUser(message map[string]any) error {
	for _, block := range blocks(message) {
		if block["type"] == "tool_result" {
			text, err := contentText(block["content"])
			if err != nil {
				return err
			}
			t.resultBytes += int64(utf8.RuneCountInString(text))
		}
	}
	return nil
}

func (t *transcriptTally) finish() (*transcript, error) {
	if t.turns == 0 {
		return nil, fmt.Errorf("%s: transcript carries no assistant records", t.runID)
	}
	return &transcript{
		byModel:              t.billedOutput(),
		cacheWriteTTLAssumed: t.ttlAssumed,
		turns:                t.turns,
		toolCallsByName:      t.toolCalls,
		readPaths:            t.readPaths,
		toolResultBytes:      t.resultBytes,
		reportedCostUSD:      t.reportedCost,
	}, nil
}

// billedOutput replaces the summed output tokens with the result record's.
// An assistant record's usage carries the output of one streamed chunk, not
// the turn, so their sum runs a whole run's output at a twentieth of what the
// host bills (118 summed against 2628 in the result record, pilot 2026-09-10).
// The input and cache counters agree between the two, so only output is
// replaced, apportioned across models by the share each summed to.
func (t *transcriptTally) billedOutput() map[string]usageCounts {
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

// jsonlLines yields the file's lines split on newlines, including a final
// unterminated one, without a cap on line length: a tool result can run to
// megabytes on one line.
func jsonlLines(file string, visit func(line []byte) error) error {
	fh, err := os.Open(file)
	if err != nil {
		return err
	}
	defer fh.Close()
	reader := bufio.NewReader(fh)
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
	if _, err := os.Stat(file); err != nil {
		return nil, fmt.Errorf("%s: transcript.jsonl is missing", runID)
	}
	tally := &transcriptTally{
		runID: runID, defaultModel: defaultModel,
		byModel: map[string]usageCounts{}, seenMessages: map[string]bool{}, toolCalls: map[string]int64{},
	}
	err := jsonlLines(file, func(line []byte) error {
		if len(bytes.TrimSpace(line)) == 0 {
			return nil
		}
		rec, err := pycompat.Unmarshal(line)
		if err != nil {
			return fmt.Errorf("%s: malformed transcript.jsonl line: %w", runID, err)
		}
		return tally.take(rec)
	})
	if err != nil {
		return nil, err
	}
	return tally.finish()
}

// readGuardEvents counts guard denials, advisories and skill loads in the
// magus activity trail. captured is false when no trail exists; the caller
// must keep that null rather than substituting zero, which would read as a
// guard that never fired. A malformed line is skipped, not fatal.
func readGuardEvents(file string) (events GuardEvents, captured bool, err error) {
	if _, err := os.Stat(file); err != nil {
		return GuardEvents{}, false, nil
	}
	err = jsonlLines(file, func(line []byte) error {
		doc, err := pycompat.Unmarshal(line)
		if err != nil {
			return nil
		}
		event, ok := doc.(map[string]any)
		if !ok || event["kind"] != "agent_command" {
			return nil
		}
		action := strings.ToLower(stringOr(event, "action"))
		preview := strings.ToLower(stringOr(event, "preview"))
		target := stringOr(event, "path")
		if target == "" {
			target = stringOr(event, "target")
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

func stringOr(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
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
				current = dropTwoChars(parts[2])
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
	sort.Strings(deleted)
	return InvariantViolations{TestsDeleted: deleted}, nil
}

// dropTwoChars is s[2:] over code points, which is what the Python sliced off
// a diff header's "a/" prefix.
func dropTwoChars(s string) string {
	n := 2
	for i := range s {
		if n == 0 {
			return s[i:]
		}
		n--
	}
	return ""
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
	wallMs, timeToFirstEditMs, timeToDoneMs *Number
}

func readTiming(runDir string) (timing, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, "timing.json"))
	if errors.Is(err, os.ErrNotExist) {
		return timing{}, nil
	}
	if err != nil {
		return timing{}, err
	}
	doc, err := pycompat.Unmarshal(raw)
	if err != nil {
		return timing{}, fmt.Errorf("%s: timing.json: %w", runDir, err)
	}
	fields, ok := doc.(map[string]any)
	if !ok {
		return timing{}, fmt.Errorf("%s: timing.json is not an object", runDir)
	}
	var t timing
	for name, dst := range map[string]**Number{
		"wall_ms": &t.wallMs, "time_to_first_edit_ms": &t.timeToFirstEditMs, "time_to_done_ms": &t.timeToDoneMs,
	} {
		switch v := fields[name].(type) {
		case nil:
		case Number:
			*dst = &v
		default:
			return timing{}, fmt.Errorf("%s: timing.json %s is not a number", runDir, name)
		}
	}
	return t, nil
}

func loadMeta(runDir string) (map[string]any, error) {
	raw, err := os.ReadFile(filepath.Join(runDir, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s: meta.json is missing", runDir)
	}
	if err != nil {
		return nil, err
	}
	doc, err := pycompat.Unmarshal(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: meta.json: %w", runDir, err)
	}
	meta, ok := doc.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: meta.json is not an object", runDir)
	}
	for _, name := range requiredMeta {
		if v := meta[name]; v == nil || v == "" {
			return nil, fmt.Errorf("%s: meta.json has no %s", runDir, name)
		}
	}
	return meta, nil
}

func metaString(meta map[string]any, runDir, name string) (string, error) {
	s, ok := meta[name].(string)
	if !ok {
		return "", fmt.Errorf("%s: meta.json %s is not a string", runDir, name)
	}
	return s, nil
}

// setOptionalString is `meta.get(name)` into dst: absent and null both leave
// it nil.
func setOptionalString(meta map[string]any, runDir, name string, dst **string) error {
	switch v := meta[name].(type) {
	case nil:
		*dst = nil
	case string:
		*dst = &v
	default:
		return fmt.Errorf("%s: meta.json %s is not a string", runDir, name)
	}
	return nil
}

func setOptionalNumber(meta map[string]any, runDir, name string, dst **Number) error {
	switch v := meta[name].(type) {
	case nil:
		*dst = nil
	case Number:
		*dst = &v
	default:
		return fmt.Errorf("%s: meta.json %s is not a number", runDir, name)
	}
	return nil
}

// ExtractRun builds one metrics record for a single run directory.
//
// A control run (golden or null) yields a record with its identity, its
// control kind and its check verdict and nothing else: no agent ran, so there
// is no transcript to measure. The report reads those verdicts to say whether
// the task's check discriminates at all. A scored run without a transcript is
// still an error.
func ExtractRun(runDir string, pricing PriceTable) (RunRecord, error) {
	meta, err := loadMeta(runDir)
	if err != nil {
		return RunRecord{}, err
	}
	var ids [4]string
	for i, name := range []string{"run_id", "arm", "task", "model"} {
		if ids[i], err = metaString(meta, runDir, name); err != nil {
			return RunRecord{}, err
		}
	}
	runID, arm, task, model := ids[0], ids[1], ids[2], ids[3]
	rep, ok := meta["rep"].(Number)
	repInt, isInt := rep.Int64()
	if !ok || !isInt {
		return RunRecord{}, fmt.Errorf("%s: meta.json rep is not an int", runDir)
	}
	checkExit := readCheckExit(runDir)
	var success *bool
	if checkExit != nil {
		s := *checkExit == 0
		success = &s
	}
	if control := meta["control"]; truthy(control) {
		kind, _ := control.(string)
		if kind != ControlGolden && kind != ControlNull {
			return RunRecord{}, fmt.Errorf("%s: meta.json control '%v' is not golden or null", runDir, control)
		}
		var exitReason *string
		if err = setOptionalString(meta, runDir, "exit_reason", &exitReason); err != nil {
			return RunRecord{}, err
		}
		return RunRecord{Control: &ControlRun{
			RunID: runID, Arm: arm, Task: task, Rep: repInt, Model: model,
			Control: kind, ExitReason: exitReason, CheckExit: checkExit, Success: success,
		}}, nil
	}

	tr, err := readTranscript(filepath.Join(runDir, "transcript.jsonl"), runID, model)
	if err != nil {
		return RunRecord{}, err
	}
	tableDollars, err := priceUsage(tr.byModel, pricing, runID)
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
	run := &ScoredRun{
		RunID: runID, Arm: arm, Task: task, Rep: repInt, Model: model,
		Tokens: tr.tokens(),
		// The host's own bill wins when it recorded one: the table is a floor
		// kept for transcripts that end without a result record, and it
		// disagreed with the host by a constant 0.665x on Sonnet 5 and 1.663x
		// on Opus 5 in the 2026-09-10 pilot, which is a table error rather
		// than noise.
		Dollars:              tableDollars,
		TableDollarsUSD:      tableDollars,
		ReportedCostUSD:      tr.reportedCostUSD,
		CacheWriteTTLAssumed: tr.cacheWriteTTLAssumed,
		Turns:                tr.turns,
		ToolCalls:            tr.toolCalls(),
		ToolCallsByName:      tr.toolCallsByName,
		FileReads:            int64(len(tr.readPaths)),
		DistinctFilesRead:    tr.distinctFilesRead(),
		ReReadRate:           tr.reReadRate(),
		ToolResultBytes:      tr.toolResultBytes,
		CheckExit:            checkExit,
		Success:              success,
		InvariantViolations:  violations,
		WallMs:               times.wallMs,
		TimeToFirstEditMs:    times.timeToFirstEditMs,
		TimeToDoneMs:         times.timeToDoneMs,
	}
	if captured {
		run.GuardEvents = &guard
	}
	if tr.reportedCostUSD != nil && *tr.reportedCostUSD != 0 {
		run.Dollars = *tr.reportedCostUSD
	}
	for name, dst := range map[string]**string{
		"effort": &run.Effort, "magus_binary": &run.MagusBinary, "magus_version": &run.MagusVersion,
		"fixture_sha": &run.FixtureSha, "started": &run.Started, "ended": &run.Ended, "exit_reason": &run.ExitReason,
	} {
		if err = setOptionalString(meta, runDir, name, dst); err != nil {
			return RunRecord{}, err
		}
	}
	for name, dst := range map[string]**Number{"max_turns": &run.MaxTurns, "budget_usd": &run.BudgetUSD} {
		if err = setOptionalNumber(meta, runDir, name, dst); err != nil {
			return RunRecord{}, err
		}
	}
	return RunRecord{Scored: run}, nil
}

// truthy is Python's `if value` over a decoded JSON value.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case Number:
		return !x.IsZero()
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// SummaryLine is one line per run, for the operator watching extraction.
func SummaryLine(record RunRecord) string {
	if c := record.Control; c != nil {
		check := "None"
		if c.CheckExit != nil {
			check = strconv.FormatInt(*c.CheckExit, 10)
		}
		return fmt.Sprintf("%s: %s control, check=%s", c.RunID, c.Control, check)
	}
	r := record.Scored
	outcome := "NOCHECK"
	if r.Success != nil {
		outcome = "FAIL"
		if *r.Success {
			outcome = "PASS"
		}
	}
	guard := "guard=none"
	if g := r.GuardEvents; g != nil {
		guard = fmt.Sprintf("guard=%d/%d/%d", g.Denials, g.Advisories, g.SkillLoads)
	}
	return fmt.Sprintf("%-34s %-8s %-10s %-7s tok=%-9d $%.4f turns=%-3d tools=%-3d %s",
		r.RunID, r.Arm, r.Task, outcome, r.Tokens.TotalBilled, r.Dollars, r.Turns, r.ToolCalls, guard)
}

// ExtractAll measures every run under results, sorted by run id. A tree of
// only controls is an error.
func ExtractAll(results string, pricing PriceTable) ([]RunRecord, error) {
	entries, err := os.ReadDir(results)
	if err != nil {
		return nil, err
	}
	var runDirs []string
	for _, entry := range entries {
		dir := filepath.Join(results, entry.Name())
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			runDirs = append(runDirs, dir)
		}
	}
	if len(runDirs) == 0 {
		return nil, fmt.Errorf("no run directories under %s", results)
	}
	records := make([]RunRecord, 0, len(runDirs))
	controls := 0
	for _, dir := range runDirs {
		record, err := ExtractRun(dir, pricing)
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
	sort.SliceStable(records, func(i, j int) bool { return records[i].ID() < records[j].ID() })
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
