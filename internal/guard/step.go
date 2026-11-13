package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/guard/steprule"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
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
	paths       []jsonPointer
	command     jsonPointer
	description jsonPointer
	schema      *jsonschema.Resolved
}

// NewStepTable checks rows against rules. A malformed row is an error. A
// row nothing would judge is left to CheckJudged, once the workspace's own
// rules are known.
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
		r, err := newStepRow(row)
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

func newStepRow(row typesagent.StepRow) (stepRow, error) {
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
	if row.SpawnDescriptionField != "" {
		ptr, err := parseJSONPointer(row.SpawnDescriptionField)
		if err != nil {
			return stepRow{}, fmt.Errorf("spawn_description_field: %w", err)
		}
		r.description = ptr
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
	return r, nil
}

// CheckJudged refuses each row no rule would judge: not inert, no capability
// a built-in rule judges, no Go step rule, and no kind in kinds. kinds are
// what magus\guard.step registered.
func (t StepTable) CheckJudged(kinds []string) error {
	var errs []error
	for i, r := range t.rows {
		if t.unjudged(r) && !slices.Contains(kinds, r.Kind) {
			errs = append(errs, fmt.Errorf("step row %d (kind %q): no built-in rule judges its capabilities and no step rule is registered: "+
				"declare it inert, give it runs, reads, writes or spawns, register a rule with magus.WithStepRule, "+
				`or register one for the kind with magus\guard.step in the root magusfile`, i, r.Kind))
		}
	}
	return errors.Join(errs...)
}

// unjudged reports a row only a workspace's magus\guard.step rule judges.
func (t StepTable) unjudged(r stepRow) bool {
	return len(t.rules) == 0 && !r.has(types.ToolInert) && !r.builtinJudged()
}

// changes reports a row whose step may change state: anything but inert
// or a read.
func (r stepRow) changes() bool {
	return len(r.Capabilities) == 0 || slices.ContainsFunc(r.Capabilities, func(c string) bool {
		return c != string(types.ToolInert) && c != string(types.ToolReads)
	})
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
	case has(types.ToolSpawns) && (has(types.ToolRuns) || has(types.ToolReads) || has(types.ToolWrites)):
		return errors.New("spawns is judged as a spawn, so it does not share a row with runs, reads or writes")
	case !has(types.ToolSpawns) && row.SpawnDescriptionField != "":
		return errors.New("spawn_description_field goes with spawns: only a spawn has a description")
	case has(types.ToolRuns) != (row.Command != ""):
		return errors.New("runs and command go together: runs judges the command the pointer names")
	case (has(types.ToolReads) || has(types.ToolWrites)) != (len(row.Paths) > 0):
		return errors.New("reads or writes and paths go together: each judges the paths the pointers name")
	}
	return nil
}

func (r stepRow) has(c types.ToolCapability) bool { return slices.Contains(r.Capabilities, string(c)) }

func (r stepRow) builtinJudged() bool {
	return r.has(types.ToolRuns) || r.has(types.ToolReads) || r.has(types.ToolWrites) || r.has(types.ToolSpawns)
}

// readsDir reports a row whose built-in rules judge relative to the
// step's Dir.
func (r stepRow) readsDir() bool {
	return len(r.paths) > 0 || r.command != nil || r.has(types.ToolSpawns)
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
// its command and paths, and a spawns row's step as the spawn it is. Then
// each step rule judges it, and the strictest decision wins. req carries
// the lease, authority and session. The error is a step that cannot be
// judged: a wrong api_version, no kind, an empty Dir its row needs, or a
// failed rule.
func JudgeStep(ctx context.Context, deps Dependencies, req Request, step typesagent.Step) (types.GuardJudgment, error) {
	if step.APIVersion != typesagent.StepAPIVersion {
		return types.GuardJudgment{}, fmt.Errorf("step api_version is %q, want %q", step.APIVersion, typesagent.StepAPIVersion)
	}
	if step.Kind == "" {
		return types.GuardJudgment{}, errors.New("step has no kind")
	}
	// The Judge calls below must not end a mark their caller owns.
	ctx = context.WithValue(context.WithValue(ctx, judgingStepKey{}, true), judgmentMarkKey{}, judgmentMark{})
	judged := types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionPass, Lease: req.Lease}
	row, ok := deps.Steps.covering(step.Kind, step.Name)
	if !ok {
		return gradeStepDeny(deps, judged, denyRuleUnjudgedStep, fmt.Sprintf(
			"magus workspace: no step row covers kind %q, name %q: declare the kind with magus.WithStepRows.", step.Kind, step.Name)), nil
	}
	if row.readsDir() && step.Dir == "" {
		return types.GuardJudgment{}, fmt.Errorf("step %s %q: dir is empty, and its row judges paths, a command or a spawn relative to it", step.Kind, step.Name)
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
	if refusal == "" && row.has(types.ToolSpawns) {
		read.spawn, refusal = row.spawnEnvelope(step, input)
	}
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
	return gradeWorkspaceStep(ctx, deps, req, stepReq, row, judged), nil
}

// seamStep is the magus\guard.step seam.
const seamStep functionSeam = "step"

// workspaceStepRule is the GuardJudgment.Rule an unnamed magus\guard.step
// answer carries.
const workspaceStepRule = workspaceShellPrefix + "step"

// gradeWorkspaceStep asks the workspace's magus\guard.step rule for the
// step's kind on both sides, strengthen only. A rule that fails judges
// nothing and says so. A row only that rule judges is denied when no side
// answers.
func gradeWorkspaceStep(ctx context.Context, deps Dependencies, req Request, stepReq typesagent.StepRequest, row stepRow, judged types.GuardJudgment) types.GuardJudgment {
	step := stepReq.Step
	unjudged := deps.Steps.unjudged(row)
	if judged.Decision == decisionDeny {
		return judged
	}
	var worktree steprule.Rule
	if deps.StepRule != nil {
		worktree = deps.StepRule(step.Kind)
	}
	if worktree == nil && deps.ApprovedStepRule == nil {
		if unjudged {
			return gradeStepDeny(deps, judged, denyRuleUnjudgedStep, noStepRuleReason(step, nil))
		}
		return judged
	}
	at := hookLocationAt(deps, step.Dir)
	who := hookAttribution{Host: req.Host, Form: req.Form, Session: cmp.Or(step.Session, req.Session), Agent: cmp.Or(step.Agent, req.Agent), Window: req.Window}
	facts := hint.NewGate(at.cacheDir, who.factsKey())
	bind := func(rule steprule.Rule) ruleCall {
		if rule == nil {
			return nil
		}
		return func(ctx context.Context) (types.GuardVerdict, error) { return rule(ctx, stepReq, facts) }
	}
	var resolve func(context.Context) (ruleCall, error)
	if deps.ApprovedStepRule != nil {
		resolve = func(ctx context.Context) (ruleCall, error) {
			rule, err := deps.ApprovedStepRule(ctx, step.Kind)
			return bind(rule), err
		}
	}
	asked := askWorkspaceRules(ctx, seamStep, deps, resolve, bind(worktree))
	denyLapse(&asked, judgedCall{seam: seamStep, changes: row.changes(), what: fmt.Sprintf("this %s step", step.Kind), lease: judged.Lease})
	decided := ""
	if judged.Decision != decisionPass {
		decided = decidedByBuiltin
	}
	judged, _ = applyWorkspaceAnswer(judged, decided, asked, workspaceStepRule)
	if unjudged && len(asked.answered) == 0 && judged.Decision != decisionDeny {
		return gradeStepDeny(deps, judged, denyRuleUnjudgedStep, noStepRuleReason(step, asked.failures))
	}
	if note := stepFailureNote(asked); note != "" && judged.Decision != decisionDeny {
		judged.Context = strings.Join(slices.DeleteFunc([]string{judged.Context, note}, func(s string) bool { return s == "" }), "\n\n")
		if judged.Decision == decisionPass {
			judged.Decision = decisionAdvise
		}
	}
	return judged
}

// noStepRuleReason is the deny for a step only a workspace rule judges when
// none answered, naming each side that failed.
func noStepRuleReason(step typesagent.Step, failures []trail.RuleFailure) string {
	reason := fmt.Sprintf("magus workspace: no rule judged the %s step: its row has no capability a built-in rule judges, no magus.WithStepRule is registered, "+
		`and no %s rule for kind %q answered. Nothing passes unjudged.`, step.Kind, seamStep.member(), step.Kind)
	for _, f := range failures {
		reason += fmt.Sprintf("\n  %s: %s", sideName(seamStep, f.Side), f.Error)
	}
	return reason
}

// stepFailureNote names each side of the step rule that judged nothing, ""
// when every side answered.
func stepFailureNote(asked rulesAnswer) string {
	if len(asked.failures) == 0 {
		return ""
	}
	lines := make([]string, len(asked.failures))
	for i, f := range asked.failures {
		lines[i] = fmt.Sprintf("%s: %s", sideName(seamStep, f.Side), f.Error)
	}
	return fmt.Sprintf("magus workspace: %s judged nothing, so this step stands on the other rules alone.\n%s",
		seamStep.member(), strings.Join(lines, "\n"))
}

// stepInput is what a row's pointers read from one step's input.
type stepInput struct {
	command string
	paths   []string
	// spawn is the hook envelope a spawns row's step is judged as.
	spawn string
}

// spawnEnvelope is the hook envelope a spawns step is judged as, so the
// spawn rules read it as they read a host's spawn. The refusal says
// which field holds the wrong type.
func (r stepRow) spawnEnvelope(step typesagent.Step, input map[string]any) (string, string) {
	if _, ok := envelopeSpawn(input); !ok {
		return "", "holds no spawn prompt: a prompt, or a message beside an agent field"
	}
	if r.description != nil {
		d, ok := r.description.get(input).(string)
		if !ok {
			return "", fmt.Sprintf("holds no description string at %s", r.SpawnDescriptionField)
		}
		// The spawn rules read the description where a host puts it.
		input = maps.Clone(input)
		input["description"] = d
	}
	step.Name, step.Input = cmp.Or(step.Name, step.Kind), input
	raw, err := json.Marshal(stepEnvelope(step))
	if err != nil {
		return "", fmt.Sprintf("does not encode as a spawn: %v", err)
	}
	return string(raw), ""
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

// judgeStepBuiltins runs Judge over the spawn, the command and each path.
// Each is the request the hook judges for a spawn, a shell line, a read
// or a write.
func judgeStepBuiltins(ctx context.Context, deps Dependencies, req Request, step typesagent.Step, row stepRow, read stepInput, judged types.GuardJudgment) types.GuardJudgment {
	base := req
	base.Dir, base.Session, base.Agent = step.Dir, cmp.Or(step.Session, req.Session), cmp.Or(step.Agent, req.Agent)
	base.IsPath = false
	var calls []Request
	if read.spawn != "" {
		c := base
		c.Input = read.spawn
		calls = append(calls, c)
	}
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

// hostToolKind is the kind of step a host's tool call is. The host's
// tool rows declare it, so a Step record of this kind is judged as the
// hook envelope it stands for.
const hostToolKind = "tool"

// stepGroup prefixes every Step record's api_version, whatever its
// version: "magus.step/".
var stepGroup = typesagent.StepAPIVersion[:strings.IndexByte(typesagent.StepAPIVersion, '/')+1]

// judgingStepKey marks a context JudgeStep judges a step's parts under.
type judgingStepKey struct{}

// stepRecord reads raw as a Step record, reporting whether it is one: a
// JSON object whose api_version names the Step group.
func stepRecord(raw string) (typesagent.Step, bool) {
	if !strings.HasPrefix(raw, "{") || !strings.Contains(raw, `"`+stepGroup) {
		return typesagent.Step{}, false
	}
	var step typesagent.Step
	if json.Unmarshal([]byte(raw), &step) != nil || !strings.HasPrefix(step.APIVersion, stepGroup) {
		return typesagent.Step{}, false
	}
	return step, true
}

// hostToolStep reports a step that reads as a host's hook envelope.
func hostToolStep(step typesagent.Step) bool {
	return step.APIVersion == typesagent.StepAPIVersion && step.Kind == hostToolKind && step.Name != ""
}

// stepEnvelope is the hook envelope a tool step stands for: the name is
// the tool, and the input its tool_input.
func stepEnvelope(step typesagent.Step) hookEnvelope {
	return hookEnvelope{ToolName: step.Name, ToolInput: step.Input, Cwd: step.Dir, SessionID: step.Session, AgentID: step.Agent}
}

// declaredStep is the Step record input holds when Judge hands it to
// JudgeStep. That is every record but a host tool step no row of table
// covers, which decodeHookEnvelope reads as its envelope.
func declaredStep(ctx context.Context, input string, table StepTable) (typesagent.Step, bool) {
	if judging, _ := ctx.Value(judgingStepKey{}).(bool); judging {
		return typesagent.Step{}, false
	}
	step, ok := stepRecord(input)
	if !ok {
		return typesagent.Step{}, false
	}
	if _, covered := table.covering(step.Kind, step.Name); hostToolStep(step) && !covered {
		return typesagent.Step{}, false
	}
	return step, true
}

// judgeStepRecord is JudgeStep for a record a hook or `magus shell` sent.
// A step it cannot judge is denied rather than cleared, since a hook
// fails open on an error.
func judgeStepRecord(ctx context.Context, deps Dependencies, req Request, step typesagent.Step) types.GuardJudgment {
	j, err := JudgeStep(ctx, deps, req, step)
	if err != nil {
		return types.GuardJudgment{APIVersion: agent.GuardAPIVersion, Decision: decisionDeny, Rule: string(denyRuleStepInput),
			Reason: fmt.Sprintf("magus workspace: the step could not be judged: %v. Nothing was judged, so it is blocked rather than cleared.", err)}
	}
	return j
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
