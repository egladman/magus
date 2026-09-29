package doctor

import (
	"fmt"
	"slices"
	"time"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// checkToolchainLifecycle reports a tool whose installed release cycle is past its end of
// life, and a workspace window floor (a project's tools min) that still admits such a cycle.
//
// It reads only the answer `magus describe tools` stored. Doctor never fetches, for the
// reason it never probes (see checkReadinessProbes): a read-only report must not depend on
// a host being reachable. So a workspace that has not run describe tools since its spells
// last changed gets no verdict here, and is told which command produces one.
//
// Advice at most. End of life is information about the world outside the tree, and a
// release cycle ending is not a defect in it (docs/scope.md).
func (r *runner) checkToolchainLifecycle(projects []*types.Project) types.Check {
	const name = "toolchain-lifecycle"
	// A *magus.Magus answers both. The cache dir is the workspace's resolved one, which
	// r.root (the --root override, often empty) cannot rebuild.
	type provider interface {
		LifecycleProvider() string
		CacheDir() string
	}
	p, ok := any(r.ws).(provider)
	wired := ""
	if ok {
		wired = p.LifecycleProvider()
	}
	if wired == "" {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no lifecycle provider wired (magus\\lifecycle.provider)"}
	}
	keys := workspace.LifecycleKeys(projects)
	if len(keys) == 0 {
		return types.Check{Name: name, Status: types.CheckOK, Message: "no spell names a lifecycle product"}
	}
	answer, stored := workspace.CachedLifecycles(r.runCtx(), p.CacheDir(), r.ws.Root(), wired, keys)
	if !stored {
		return types.Check{
			Name: name, Status: types.CheckOK, Evidence: types.EvidenceUnknown,
			Message: fmt.Sprintf("no stored answer from %s for this workspace's spells; `magus describe tools` asks it", wired),
			Fix:     []string{"describe", "tools"},
		}
	}
	details := lifecycleFindings(projects, answer, time.Now())
	msg := fmt.Sprintf("no installed cycle or window floor is past its end of life (%s, fetched %s)", wired, answer.Status.FetchedAt)
	status := types.CheckOK
	if len(details) > 0 {
		status = types.CheckAdvice
		msg = fmt.Sprintf("%d past end of life, per %s as fetched %s", len(details), wired, answer.Status.FetchedAt)
	}
	return types.Check{Name: name, Status: status, Message: msg, Details: details}
}

// lifecycleFindings lists every installed version and every window floor whose cycle is
// past its end of life on now, deduplicated and sorted.
func lifecycleFindings(projects []*types.Project, answer workspace.LifecycleAnswer, now time.Time) []string {
	eol := func(key, version string) (spells.ReleaseCycle, bool) {
		i := slices.IndexFunc(answer.Lifecycles, func(l spells.Lifecycle) bool { return l.Key == key })
		if i < 0 {
			return spells.ReleaseCycle{}, false
		}
		c, s := answer.Lifecycles[i].SupportOf(version, now)
		return c, s == spells.SupportEOL
	}
	var out []string
	add := func(line string) {
		if !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	for _, in := range answer.Installed {
		if c, past := eol(in.Lifecycle, in.Version); past {
			add(fmt.Sprintf("%s/%s: installed %s is in %s %s, past end of life since %s", in.Project, in.Bin, in.Version, in.Lifecycle, c.Cycle, c.EOL))
		}
	}
	for _, p := range projects {
		for _, sp := range p.ResolvedSpells {
			for _, bin := range sp.ToolNames() {
				t, _ := sp.Tool(bin)
				if t.Lifecycle == "" {
					continue
				}
				// The workspace's own floor, not the spell's: a spell's min says what its ops
				// need to run, while the workspace's says what it has qualified, and only the
				// second is a policy this workspace can move.
				floor := p.ToolBounds[bin].Min
				if floor == "" {
					continue
				}
				if c, past := eol(t.Lifecycle, floor); past {
					add(fmt.Sprintf("%s/%s: window min %s admits %s %s, past end of life since %s", p.Path, bin, floor, t.Lifecycle, c.Cycle, c.EOL))
				}
			}
		}
	}
	slices.Sort(out)
	return out
}
