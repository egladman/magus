package job

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// testSelection is resolved from the loaded workspace, never from a check's
// spelling. Nil means it was unavailable and permits no selector-specific evidence.
type testSelection func(types.LeaseCheck) (string, bool)

func (selection testSelection) goTests(check types.LeaseCheck) bool {
	if check.Script != "" || selection == nil {
		return false
	}
	dialect, known := selection(check)
	return known && dialect == "go"
}

func (selection testSelection) known(check types.LeaseCheck) bool {
	if selection == nil {
		return false
	}
	_, known := selection(check)
	return known
}

func (store *Store) testSelection() testSelection {
	ws, why := store.loadedWorkspace()
	if why != "" {
		return nil
	}
	return func(check types.LeaseCheck) (string, bool) { return resolvedTestSelection(ws, check) }
}

// resolvedTestSelection follows the same direct dispatch the runner proves.
// ctx.needs clears forwarded args; a body merely mentioning an op, or supplying
// explicit opts.args, is not evidence that its caller's selector reaches that op.
func resolvedTestSelection(ws Workspace, check types.LeaseCheck) (string, bool) {
	if check.Script != "" {
		return "", true
	}
	if ws.Unavailable != "" {
		return "", false
	}
	project := path.Clean(check.Project)
	if check.Project == "" {
		project = "."
	}
	i := slices.IndexFunc(ws.Projects, func(p *types.Project) bool { return p.Path == project })
	if i < 0 {
		return "", false
	}
	p := ws.Projects[i]
	spell, target, qualified := strings.Cut(check.Target, "::")
	if !qualified {
		spell, target = "", check.Target
	}
	target, _, _ = strings.Cut(target, ":")
	target = types.Normalize(target)
	if !qualified && slices.Contains(p.MagusfileTargets, target) {
		return dispatchedSelection(p, target)
	}
	found, dialect := false, ""
	for _, bound := range p.ResolvedSpells {
		if qualified && bound.Name() != spell {
			continue
		}
		op, ok := bound.Op(target)
		if !ok {
			continue
		}
		// An unqualified op can fan out across spells. Conflicting dialects cannot
		// share one parser, so none is inferred in that case.
		if found && dialect != op.TestSelection {
			return "", false
		}
		found, dialect = true, op.TestSelection
	}
	return dialect, found
}

func opSelection(p *types.Project, spell, name string) (string, bool) {
	for _, bound := range p.ResolvedSpells {
		if bound.Name() != spell {
			continue
		}
		op, ok := bound.Op(types.Normalize(name))
		if !ok {
			return "", false
		}
		return op.TestSelection, true
	}
	return "", false
}

// dispatchedSelection judges a target that only dispatches to spell ops.
func dispatchedSelection(p *types.Project, target string) (string, bool) {
	if !slices.Contains(p.DispatchOnlyTargets, target) {
		return "", false
	}
	uses := p.TargetSpellOps[target]
	if len(uses) == 0 {
		return "", false
	}
	dialect := ""
	for _, use := range uses {
		if len(use.Ops) == 0 {
			return "", false
		}
		for _, name := range use.Ops {
			found, known := opSelection(p, use.Spell, name)
			if !known {
				return "", false
			}
			if found != "" {
				if dialect != "" && dialect != found {
					return "", false
				}
				dialect = found
			}
		}
	}
	return dialect, true
}

// TestScope is the narrow test run for the write paths one spell claims in
// one project. It holds the operands its test op's testScope expands them to,
// or why the run cannot be narrowed.
type TestScope struct {
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	Spell   string `json:"spell,omitempty"   yaml:"spell,omitempty"`
	// Op is the qualified test op, `spell::op`, whose testScope was expanded.
	Op    string   `json:"op,omitempty" yaml:"op,omitempty"`
	Files []string `json:"files"        yaml:"files"`
	// Operands exist in the checkout, relative to the project directory.
	Operands []string `json:"operands,omitempty" yaml:"operands,omitempty"`
	// Whole says why no operand narrows the run, so the project's whole test op
	// runs. It is empty when Operands is set.
	Whole string `json:"whole,omitempty" yaml:"whole,omitempty"`
}

// NarrowTestScope groups writePaths, workspace-relative, by owning project and
// claiming spell. It expands each through the testScope of that spell's test
// op and drops an operand naming nothing under checkout. Every group carries
// Operands or a Whole reason. Core holds no language's notion of which tests
// a file has, so a spell declaring no testScope gets a reason.
func NarrowTestScope(projects []*types.Project, writePaths []string, checkout string) []TestScope {
	var out []TestScope
	for _, file := range writePaths {
		one := fileTestScope(projects, file, checkout)
		i := slices.IndexFunc(out, func(s TestScope) bool {
			return s.Project == one.Project && s.Spell == one.Spell && s.Op == one.Op && s.Whole == one.Whole
		})
		if i < 0 {
			out = append(out, one)
			continue
		}
		out[i].Files = append(out[i].Files, file)
		for _, operand := range one.Operands {
			if !slices.Contains(out[i].Operands, operand) {
				out[i].Operands = append(out[i].Operands, operand)
			}
		}
	}
	for i := range out {
		if out[i].Op != "" && out[i].Whole == "" && len(out[i].Operands) == 0 {
			out[i].Whole = fmt.Sprintf("nothing %s's testScope expands these files to exists in the checkout", out[i].Op)
		}
	}
	return out
}

func fileTestScope(projects []*types.Project, file, checkout string) TestScope {
	p := owningProject(projects, file)
	if p == nil {
		return TestScope{Files: []string{file}, Whole: "no project contains it"}
	}
	scope := TestScope{Project: p.Path, Files: []string{file}}
	rel := file
	if p.Path != "." {
		rel = strings.TrimPrefix(file, p.Path+"/")
	}
	ext := strings.ToLower(path.Ext(rel))
	i := slices.IndexFunc(p.ResolvedSpells, func(sp *spells.Spell) bool { return slices.Contains(sp.LanguageExtensions(), ext) })
	if i < 0 {
		scope.Whole = fmt.Sprintf("no spell bound to %s claims its extension %q", p.Path, ext)
		return scope
	}
	sp := p.ResolvedSpells[i]
	scope.Spell = sp.Name()
	names := scopedOps(sp)
	switch len(names) {
	case 0:
		scope.Whole = fmt.Sprintf("spell %s declares no testScope, so its whole project runs", sp.Name())
		return scope
	case 1:
	default:
		scope.Whole = fmt.Sprintf("spell %s declares testScope on ops %s, so which one is its test op is unknown", sp.Name(), strings.Join(names, ", "))
		return scope
	}
	scope.Op = fmt.Sprintf("%s::%s", sp.Name(), names[0])
	if strings.ContainsAny(rel, "*?[{") {
		scope.Whole = "a glob names no one file to expand"
		return scope
	}
	op, _ := sp.Op(types.Normalize(names[0]))
	for _, operand := range op.ExpandTestScope(rel) {
		if _, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(p.Path), filepath.FromSlash(operand))); err == nil {
			scope.Operands = append(scope.Operands, operand)
		}
	}
	return scope
}

// scopedOps are the sorted names of sp's ops that declare a testScope.
func scopedOps(sp *spells.Spell) []string {
	var names []string
	for _, name := range sp.Targets() {
		op, ok := sp.Op(types.Normalize(name))
		if ok && len(op.TestScope) > 0 && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	return names
}

// uncheckedTestSelection skips a check already in refused: it cannot run.
func uncheckedTestSelection(ws Workspace, row types.Job, refused types.JobProblems) []string {
	var unchecked []string
	for _, goal := range row.EffectiveGoals() {
		if goal.Kind != types.GoalKindCheck || goal.Check.Script != "" {
			continue
		}
		field := goalCheckField(row, goal)
		if slices.ContainsFunc(refused, func(p types.JobProblem) bool {
			return p.Code == types.JobCheckUnrunnable && strings.HasPrefix(p.Field, field+".")
		}) {
			continue
		}
		if _, known := resolvedTestSelection(ws, goal.Check); known {
			continue
		}
		unchecked = append(unchecked, fmt.Sprintf("test selection for `%s`: the workspace does not prove that forwarded arguments reach a declared selector; selector-specific narrowing was not judged and recorded evidence arguments must match exactly (use a qualified spell op or a target that only forwards its own context)", goal.Check.String()))
	}
	return unchecked
}
