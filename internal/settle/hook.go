package settle

import (
	"maps"
	"slices"

	"github.com/egladman/magus/vcs"
)

// Invocations groups the runs into one `<target>:rw <projects...>` invocation per
// target, sorted by target: the generate target of every project in projects that
// defines one, plus each owed record's own target. Every project of a target shares one
// run, so the scheduler orders them by their declared dependencies and runs the rest in
// parallel. A generator that reads another project's generated output declares that
// project (depends_on or ctx.needs), or the run may build it first.
func Invocations(projects []string, owed []vcs.OwedRegeneration, defines func(path, target string) bool) [][]string {
	groups := map[string]map[string]bool{}
	add := func(project, target string) {
		if groups[target] == nil {
			groups[target] = map[string]bool{}
		}
		groups[target][project] = true
	}
	for _, p := range projects {
		if defines(p, "generate") {
			add(p, "generate")
		}
	}
	for _, o := range owed {
		if defines(o.Project, o.Target) {
			add(o.Project, o.Target)
		}
	}
	out := make([][]string, 0, len(groups))
	for _, target := range slices.Sorted(maps.Keys(groups)) {
		out = append(out, append([]string{target + ":rw"}, slices.Sorted(maps.Keys(groups[target]))...))
	}
	return out
}
