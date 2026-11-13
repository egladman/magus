package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
)

// denyRuleStepInput is a step whose input its row cannot read.
const denyRuleStepInput denyRuleName = "step-input"

// stepRulePrefix is what a Go step rule's verdict reports under.
const stepRulePrefix = "step:"

// StepRule is a Go program's rule over every step JudgeStep judges. An
// error means the rule judged nothing, and the judgment fails with it.
type StepRule func(ctx context.Context, req typesagent.StepRequest) (types.GuardVerdict, error)

// StepTable is the step rows and rules a program declared, each row
// checked once by NewStepTable.
type StepTable struct {
	rows  []stepRow
	rules []StepRule
}

type stepRow struct {
	typesagent.StepRow
	paths   []jsonPointer
	command jsonPointer
	schema  *jsonschema.Resolved
}

// NewStepTable checks rows against rules. A malformed row is an error, and
// so is a row no built-in rule and no step rule would judge.
func NewStepTable(rows []typesagent.StepRow, rules []StepRule) (StepTable, error) {
	for _, r := range rules {
		if r == nil {
			return StepTable{}, errors.New("a step rule is nil")
		}
	}
	t := StepTable{rules: rules}
	seen := map[[2]string]bool{}
	var errs []error
	for i, row := range rows {
		r, err := newStepRow(row, len(rules) > 0)
		if err != nil {
			errs = append(errs, fmt.Errorf("step row %d (kind %q): %w", i, row.Kind, err))
			continue
		}
		key := [2]string{row.Kind, row.Name}
		if seen[key] {
			errs = append(errs, fmt.Errorf("step row %d repeats kind %q and name %q", i, row.Kind, row.Name))
			continue
		}
		seen[key] = true
		t.rows = append(t.rows, r)
	}
	if err := errors.Join(errs...); err != nil {
		return StepTable{}, err
	}
	return t, nil
}

func newStepRow(row typesagent.StepRow, ruled bool) (stepRow, error) {
	if row.Kind == "" {
		return stepRow{}, errors.New("no kind")
	}
	if err := types.ValidateGuardRuleName(row.Kind); err != nil {
		return stepRow{}, fmt.Errorf("kind: %w", err)
	}
	r := stepRow{StepRow: row}
	if err := checkCapabilities(row); err != nil {
		return stepRow{}, err
	}
	for _, p := range row.Paths {
		ptr, err := parseJSONPointer(p)
		if err != nil {
			return stepRow{}, fmt.Errorf("paths: %w", err)
		}
		r.paths = append(r.paths, ptr)
	}
	if row.Command != "" {
		ptr, err := parseJSONPointer(row.Command)
		if err != nil {
			return stepRow{}, fmt.Errorf("command: %w", err)
		}
		r.command = ptr
	}
	if len(row.InputSchema) > 0 {
		var s jsonschema.Schema
		if err := json.Unmarshal(row.InputSchema, &s); err != nil {
			return stepRow{}, fmt.Errorf("input_schema does not parse: %w", err)
		}
		resolved, err := s.Resolve(nil)
		if err != nil {
			return stepRow{}, fmt.Errorf("input_schema does not resolve: %w", err)
		}
		r.schema = resolved
	}
	if !ruled && !r.has(types.ToolInert) && !r.builtinJudged() {
		return stepRow{}, errors.New("no built-in rule judges its capabilities and no step rule is registered: " +
			"declare it inert, give it runs, reads or writes, or register a rule with magus.WithStepRule")
	}
	return r, nil
}

// checkCapabilities refuses a row whose capabilities and pointers disagree,
// since a built-in rule would then have nothing to read.
func checkCapabilities(row typesagent.StepRow) error {
	known := types.ToolCapabilities()
	for _, c := range row.Capabilities {
		if !slices.Contains(known, types.ToolCapability(c)) {
			return fmt.Errorf("unknown capability %q (want one of %v)", c, known)
		}
	}
	has := func(c types.ToolCapability) bool { return slices.Contains(row.Capabilities, string(c)) }
	switch {
	case has(types.ToolInert) && len(row.Capabilities) > 1:
		return errors.New("inert says nothing judges the row, so it stands alone")
	case has(types.ToolSpawns):
		return errors.New("spawns is not judged through Judge yet: a spawn is judged on the hook path")
	case has(types.ToolRuns) != (row.Command != ""):
		return errors.New("runs and command go together: runs judges the command the pointer names")
	case (has(types.ToolReads) || has(types.ToolWrites)) != (len(row.Paths) > 0):
		return errors.New("reads or writes and paths go together: each judges the paths the pointers name")
	}
	return nil
}

func (r stepRow) has(c types.ToolCapability) bool { return slices.Contains(r.Capabilities, string(c)) }

func (r stepRow) builtinJudged() bool {
	return r.has(types.ToolRuns) || r.has(types.ToolReads) || r.has(types.ToolWrites)
}

// covering is the row for kind and name, a named row winning over the
// kind's catch-all.
func (t StepTable) covering(kind, name string) (stepRow, bool) {
	var all *stepRow
	for i, r := range t.rows {
		if r.Kind != kind {
			continue
		}
		if r.Name == name {
			return r, true
		}
		if r.Name == "" {
			all = &t.rows[i]
		}
	}
	if all == nil {
		return stepRow{}, false
	}
	return *all, true
}

// JudgeStep judges step against deps.Steps. Judge's built-in rules judge
// its command and paths, then each step rule judges it, and the strictest
// decision wins. req carries the lease, authority and session. The error
// is a step that cannot be judged: a wrong api_version, no kind, an empty
// Dir its row needs, or a failed rule.
func JudgeStep(ctx context.Context, deps Dependencies, req Request, step typesagent.Step) (types.GuardJudgment, error) {
	if step.APIVersion != typesagent.StepAPIVersion {
		return types.GuardJudgment{}, fmt.Errorf("step api_version is %q, want %q", step.APIVersion, typesagent.StepAPIVersion)
	}
	if step.Kind == "" {
		return types.GuardJudgment{}, errors.New("step has no kind")
	}
	judged := types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionPass, Lease: req.Lease}
	row, ok := deps.Steps.covering(step.Kind, step.Name)
	if !ok {
		return gradeStepDeny(deps, judged, denyRuleUnjudgedTool, fmt.Sprintf(
			"magus workspace: no step row covers kind %q, name %q: declare the kind with magus.WithStepRows.", step.Kind, step.Name)), nil
	}
	if (len(row.paths) > 0 || row.command != nil) && step.Dir == "" {
		return types.GuardJudgment{}, fmt.Errorf("step %s %q: dir is empty, and its row reads paths or a command relative to it", step.Kind, step.Name)
	}
	input := step.Input
	if input == nil {
		input = map[string]any{}
	}
	if row.schema != nil {
		if err := row.schema.Validate(input); err != nil {
			return gradeStepDeny(deps, judged, denyRuleStepInput, fmt.Sprintf(
				"magus workspace: the %s step's input does not match its row's input_schema: %v.", step.Kind, err)), nil
		}
	}
	read, refusal := row.read(input)
	if refusal != "" {
		return gradeStepDeny(deps, judged, denyRuleStepInput, fmt.Sprintf("magus workspace: the %s step's input %s.", step.Kind, refusal)), nil
	}
	judged = judgeStepBuiltins(ctx, deps, req, step, row, read, judged)
	stepReq := typesagent.StepRequest{Step: step, Capabilities: slices.Clone(row.Capabilities), Paths: read.paths, Command: read.command, Lease: judged.Lease}
	for _, rule := range deps.Steps.rules {
		v, err := rule(ctx, stepReq)
		if err != nil {
			return types.GuardJudgment{}, fmt.Errorf("step rule on %s %q: %w", step.Kind, step.Name, err)
		}
		ruled, err := stepRuleJudgment(step, v)
		if err != nil {
			return types.GuardJudgment{}, err
		}
		judged = stricterStep(judged, ruled)
	}
	return judged, nil
}

// stepInput is what a row's pointers read from one step's input.
type stepInput struct {
	command string
	paths   []string
}

// read follows the row's pointers into input, or says which field holds
// the wrong type: the agent can correct that, so it is a deny.
func (r stepRow) read(input map[string]any) (stepInput, string) {
	var got stepInput
	if r.command != nil {
		s, ok := r.command.get(input).(string)
		if !ok || s == "" {
			return stepInput{}, fmt.Sprintf("holds no command string at %s", r.Command)
		}
		got.command = s
	}
	for i, p := range r.paths {
		switch v := p.get(input).(type) {
		case string:
			got.paths = append(got.paths, v)
		case []any:
			for _, e := range v {
				s, ok := e.(string)
				if !ok {
					return stepInput{}, fmt.Sprintf("holds a non-string path in the array at %s", r.Paths[i])
				}
				got.paths = append(got.paths, s)
			}
		case []string:
			got.paths = append(got.paths, v...)
		default:
			return stepInput{}, fmt.Sprintf("holds no path string or array of strings at %s", r.Paths[i])
		}
	}
	return got, ""
}

// judgeStepBuiltins runs Judge over the command and each path, the
// requests the hook judges for a shell line, a read and a write.
func judgeStepBuiltins(ctx context.Context, deps Dependencies, req Request, step typesagent.Step, row stepRow, read stepInput, judged types.GuardJudgment) types.GuardJudgment {
	base := req
	base.Dir, base.Session, base.Agent = step.Dir, cmp.Or(step.Session, req.Session), cmp.Or(step.Agent, req.Agent)
	var calls []Request
	if row.has(types.ToolRuns) {
		c := base
		c.Input = read.command
		calls = append(calls, c)
	}
	for _, p := range read.paths {
		if row.has(types.ToolReads) {
			c := base
			c.Input = readLine(p, nil)
			calls = append(calls, c)
		}
		if row.has(types.ToolWrites) {
			c := base
			c.Input, c.IsPath = p, true
			calls = append(calls, c)
		}
	}
	for i, c := range calls {
		j := Judge(ctx, deps, c)
		if i == 0 {
			judged.Lease, judged.LeaseFrom = j.Lease, j.LeaseFrom
		}
		judged = stricterStep(judged, j)
	}
	return judged
}

// gradeStepDeny grades a step-level deny through the workspace's builtin
// settings, as a compiled rule's deny is.
func gradeStepDeny(deps Dependencies, judged types.GuardJudgment, rule denyRuleName, reason string) types.GuardJudgment {
	switch g := deps.grade(ShellVerdict{Deny: reason, Rule: denyRule{Name: rule}}); {
	case g.Deny != "":
		judged.Decision, judged.Reason, judged.Rule = decisionDeny, g.Deny, string(rule)
	case g.Context != "":
		judged.Decision, judged.Context, judged.Rule = decisionAdvise, g.Context, string(rule)
	}
	return judged
}

// stepRuleJudgment is a Go rule's verdict as a judgment, reported under
// step:<rule>, or step:<kind> when the rule names none.
func stepRuleJudgment(step typesagent.Step, v types.GuardVerdict) (types.GuardJudgment, error) {
	if !v.Decision.Valid() {
		return types.GuardJudgment{}, fmt.Errorf("step rule on %s returned decision %q (want one of %v)", step.Kind, v.Decision, v.Decision.Values())
	}
	if v.Rule != "" {
		if err := types.ValidateGuardRuleName(v.Rule); err != nil {
			return types.GuardJudgment{}, fmt.Errorf("step rule on %s: %w", step.Kind, err)
		}
	}
	rule := stepRulePrefix + cmp.Or(v.Rule, step.Kind)
	j := types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionPass}
	switch v.Decision {
	case types.GuardDeny:
		j.Decision, j.Reason, j.Rule = decisionDeny, v.Reason, rule
	case types.GuardAdvise:
		j.Decision, j.Context, j.Rule = decisionAdvise, v.Reason, rule
	}
	return j, nil
}

// stepDecisionRank orders decisions by strictness, deny first and pass last.
func stepDecisionRank(d string) int {
	switch d {
	case decisionDeny:
		return 3
	case decisionAsk:
		return 2
	case decisionAdvise:
		return 1
	}
	return 0
}

// stricterStep keeps the stricter of a and b, and a's lease. Two advisories
// keep both contexts; two denies keep a's.
func stricterStep(a, b types.GuardJudgment) types.GuardJudgment {
	ra, rb := stepDecisionRank(a.Decision), stepDecisionRank(b.Decision)
	switch {
	case rb > ra:
		b.Lease, b.LeaseFrom = cmp.Or(a.Lease, b.Lease), cmp.Or(a.LeaseFrom, b.LeaseFrom)
		b.APIVersion = cmp.Or(a.APIVersion, b.APIVersion)
		return b
	case ra == rb && ra == stepDecisionRank(decisionAdvise) && b.Context != "" && b.Context != a.Context:
		a.Context = strings.TrimSpace(strings.Join([]string{a.Context, b.Context}, "\n\n"))
	}
	return a
}

// jsonPointer is a parsed RFC 6901 pointer, one reference token each.
type jsonPointer []string

func parseJSONPointer(s string) (jsonPointer, error) {
	if !strings.HasPrefix(s, "/") {
		return nil, fmt.Errorf("%q is not a JSON Pointer: it must start with /", s)
	}
	var ptr jsonPointer
	for _, tok := range strings.Split(s[1:], "/") {
		if strings.Contains(strings.ReplaceAll(strings.ReplaceAll(tok, "~0", ""), "~1", ""), "~") {
			return nil, fmt.Errorf("%q is not a JSON Pointer: ~ escapes only 0 or 1", s)
		}
		ptr = append(ptr, strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~"))
	}
	return ptr, nil
}

// get is the value p names in v, nil when nothing is there.
func (p jsonPointer) get(v any) any {
	for _, tok := range p {
		switch node := v.(type) {
		case map[string]any:
			v = node[tok]
		case []any:
			i, err := strconv.Atoi(tok)
			if err != nil || i < 0 || i >= len(node) {
				return nil
			}
			v = node[i]
		default:
			return nil
		}
	}
	return v
}
