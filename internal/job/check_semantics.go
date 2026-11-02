package job

import (
	"fmt"
	"path"
	"slices"
	"strings"

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
	opSelection := func(spell, name string) (string, bool) {
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
	if !qualified && slices.Contains(p.MagusfileTargets, target) {
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
				found, known := opSelection(use.Spell, name)
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

func uncheckedTestSelection(ws Workspace, row types.Job) []string {
	var unchecked []string
	for _, goal := range row.EffectiveGoals() {
		if goal.Kind != types.GoalKindCheck || goal.Check.Script != "" {
			continue
		}
		if _, known := resolvedTestSelection(ws, goal.Check); known {
			continue
		}
		unchecked = append(unchecked, fmt.Sprintf("test selection for `%s`: the workspace does not prove that forwarded arguments reach a declared selector; selector-specific narrowing was not judged and recorded evidence arguments must match exactly (use a qualified spell op or a target that only forwards its own context)", goal.Check.String()))
	}
	return unchecked
}
