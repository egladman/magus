package doctor

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/dry"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// checkManifestScripts is MGS1049: a target that runs a script a manifest defines.
// The spells declare which argv shapes do (mgs_listScriptRunners); the dry host
// traces each target body's proc\exec calls. A call behind a helper is seen, a branch
// the probe does not take is not.
func (r *runner) checkManifestScripts(projects []*types.Project) types.Check {
	return checkManifestScripts(r.runCtx(), r.root, projects, project.DefaultSpellRegistry().All())
}

func checkManifestScripts(ctx context.Context, root string, projects []*types.Project, all []*spells.Spell) types.Check {
	const name = "manifest-scripts"
	declaring := slices.DeleteFunc(slices.Clone(all), func(s *spells.Spell) bool { return len(s.ScriptRunners()) == 0 })
	if len(declaring) == 0 {
		return types.Check{
			Name: name, Status: types.CheckOK, Evidence: types.EvidenceDeclared,
			Message: "no registered spell declares a script runner",
		}
	}
	slices.SortFunc(declaring, func(a, b *spells.Spell) int { return strings.Compare(a.Name(), b.Name()) })

	var findings, untraced []string
	var traced int
	for _, p := range projects {
		for _, f := range magusfileSourcesInDir(p.Dir) {
			rel := f
			if rp, err := filepath.Rel(root, f); err == nil {
				rel = filepath.ToSlash(rp)
			}
			data, err := os.ReadFile(f)
			if err != nil {
				untraced = append(untraced, fmt.Sprintf("%s: not traced: %v", rel, err))
				continue
			}
			execs, diag := dry.Execs(ctx, string(data))
			if diag != nil {
				untraced = append(untraced, fmt.Sprintf("%s:%d: not traced: %s", rel, diag.Line, diag.Msg))
				continue
			}
			traced++
			for _, op := range execs {
				for _, argv := range commandLines(op.Argv) {
					if finding, ok := scriptFinding(declaring, argv); ok {
						findings = append(findings, fmt.Sprintf("%s: target %q runs `%s`, %s (%s)",
							p.Path, op.Target, strings.Join(argv, " "), finding, rel))
					}
				}
			}
		}
	}
	slices.Sort(findings)
	slices.Sort(untraced)

	switch {
	case len(findings) > 0:
		return types.Check{
			Name:   name,
			Status: types.CheckFail,
			Message: fmt.Sprintf("%d call(s) run a script a manifest defines, so its steps, inputs and outputs are "+
				"invisible to the cache key; declare those steps in the magusfile (see %s)",
				len(findings), types.CodeURL(types.ManifestScriptDelegation)),
			Details: append(findings, untraced...),
		}
	case len(untraced) > 0:
		return types.Check{
			Name: name, Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: fmt.Sprintf("could not trace %d of %d magusfile(s)", len(untraced), len(untraced)+traced),
			Details: untraced,
		}
	}
	return types.Check{
		Name: name, Status: types.CheckOK,
		Message: fmt.Sprintf("no target in %d magusfile(s) runs a manifest-defined script", traced),
	}
}

// scriptFinding names the spell whose runner argv matches, and the script when the
// argv names one.
func scriptFinding(declaring []*spells.Spell, argv []string) (string, bool) {
	for _, s := range declaring {
		runner, script, ok := spells.MatchScriptRunner(s.ScriptRunners(), argv)
		if !ok {
			continue
		}
		prefix := strings.Join(append([]string{runner.Bin}, runner.Args...), " ")
		if script == "" {
			return fmt.Sprintf("a script its %s manifest defines (runner `%s`)", s.Name(), prefix), true
		}
		return fmt.Sprintf("script %q its %s manifest defines (runner `%s`)", script, s.Name(), prefix), true
	}
	return "", false
}

// shellOperators separate the commands of a `sh -c` line.
var shellOperators = []string{"&&", "||", ";", "|"}

// commandLines returns argv, plus each command of the line when argv runs a POSIX
// shell with -c. The line is split on whitespace, so quoting is not honored: a
// quoted operator splits where the shell would not.
func commandLines(argv []string) [][]string {
	out := [][]string{argv}
	if len(argv) != 3 || argv[1] != "-c" {
		return out
	}
	switch path.Base(filepath.ToSlash(argv[0])) {
	case "sh", "bash", "dash", "zsh", "ksh":
	default:
		return out
	}
	var cur []string
	for _, tok := range strings.Fields(argv[2]) {
		isOp := slices.Contains(shellOperators, tok)
		word := strings.TrimSuffix(tok, ";")
		if !isOp && word != "" {
			cur = append(cur, word)
		}
		if (isOp || word != tok) && len(cur) > 0 {
			out = append(out, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
