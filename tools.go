package magus

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// Tools reports the tools of the projects at the given paths, or of every project when none
// is given: each probed version, the window it is held to, and where its release cycle
// stands according to the wired lifecycle provider. `magus describe tools` and
// magus\tools() both return it.
//
// It forks every tool's version probe, memoized per (spell, dir, bin), and the lifecycle
// provider fetches (see Lifecycles). A tool without a probe is skipped: a constant-keyed
// tool's token was typed by an author, not read off anything installed.
func (m *Magus) Tools(ctx context.Context, projects ...string) (types.ToolReport, error) {
	type reading struct {
		raw string
		err error
	}
	memo := map[string]reading{}
	var rows []types.ToolRow
	for _, p := range m.ws.All() {
		if len(projects) > 0 && !slices.Contains(projects, p.Path) {
			continue
		}
		for _, sp := range p.ResolvedSpells {
			for _, bin := range sp.ToolNames() {
				t, _ := sp.Tool(bin) // ToolNames ranges the same map Tool reads
				if t.Probe.Bin == "" {
					continue
				}
				// Probing forks. An abandoned call stops rather than reporting failures
				// that only mean it was interrupted.
				if err := ctx.Err(); err != nil {
					return types.ToolReport{}, err
				}
				k := sp.Name() + "\x00" + p.Dir + "\x00" + bin
				r, hit := memo[k]
				if !hit {
					r.raw, r.err = sp.ProbeVersion(ctx, bin, p.Dir)
					memo[k] = r
				}
				rows = append(rows, buildToolRow(p.Path, bin, sp.Name(), t, p.ToolBounds[bin], r.raw, r.err))
			}
		}
	}
	status, lifecycles, err := m.Lifecycles(ctx, rows...)
	if err != nil {
		return types.ToolReport{}, err
	}
	now := time.Now()
	for i := range rows {
		joinLifecycle(&rows[i], status.State, lifecycles, now)
	}
	sortToolRows(rows)
	return types.ToolReport{
		Definition: types.ToolDefinition,
		Workspace:  m.ws.Root,
		Count:      len(rows),
		Lifecycle:  status,
		Tools:      rows,
	}, nil
}

// sortToolRows orders by project, bin, then spell. Spell is the third key, not decoration:
// two spells in one project can declare the same bin, and without it their order is
// whatever pdqsort happened to do.
func sortToolRows(rows []types.ToolRow) {
	slices.SortFunc(rows, func(a, b types.ToolRow) int {
		return cmp.Or(cmp.Compare(a.Project, b.Project), cmp.Compare(a.Bin, b.Bin), cmp.Compare(a.Spell, b.Spell))
	})
}

// renderWindow prints a window the way the docs write it. below is the first version
// REJECTED, so it renders as "< x" and never as a max.
func renderWindow(b spells.VersionBounds) string {
	switch {
	case b.Min != "" && b.Below != "":
		return ">= " + b.Min + ", < " + b.Below
	case b.Min != "":
		return ">= " + b.Min
	case b.Below != "":
		return "< " + b.Below
	default:
		return ""
	}
}

// buildToolRow turns one probe outcome into a row. Pure (no context, no exec) because
// welding this state machine inside the fork loop is what let four distinct outcomes
// collapse into one blank "not found".
func buildToolRow(project, bin, spell string, t spells.Tool, projBounds spells.VersionBounds, raw string, probeErr error) types.ToolRow {
	window := t.Supported.Intersect(projBounds)
	row := types.ToolRow{
		Project: project, Bin: bin, Spell: spell,
		SpellBounds:     renderWindow(t.Supported),
		WorkspaceBounds: renderWindow(projBounds),
		Effective:       renderWindow(window),
		Lifecycle:       t.Lifecycle,
	}
	if probeErr != nil {
		// Could not run at all: the only outcome that means "not installed".
		row.Verdict, row.ProbeError = types.ToolVerdictUnprobed, probeErr.Error()
		return row
	}
	v, ok := spells.ExtractVersion(raw)
	if !ok {
		// It ran and said something unreadable. "not found" would be a claim about a
		// binary that is demonstrably present.
		row.Verdict = types.ToolVerdictUnreadable
		return row
	}
	row.InstalledVersion = v
	switch window.Check(row.InstalledVersion) {
	case spells.VerdictTooOld:
		row.Verdict, row.DiagnosticCode = types.ToolVerdictTooOld, string(types.ToolTooOld)
	case spells.VerdictTooNew:
		row.Verdict, row.DiagnosticCode = types.ToolVerdictTooNew, string(types.ToolTooNew)
	case spells.VerdictInside:
		row.Verdict = types.ToolVerdictInside
	default:
		// An unparsed bound leaves nothing to compare. Explicit rather than defaulting to
		// inside: "could not check" must not read as "fine".
		row.Verdict = types.ToolVerdictUnknown
	}
	return row
}

// joinLifecycle fills a row's cycle, eol and support from the provider's answer. A row
// whose spell names no product, or a workspace that wires no provider, keeps the columns
// empty; everything else the answer cannot place reads unknown, and the report's
// lifecycle state says why.
func joinLifecycle(row *types.ToolRow, state string, lifecycles []spells.Lifecycle, now time.Time) {
	if row.Lifecycle == "" || state == types.LifecycleUnwired {
		return
	}
	row.Support = string(spells.SupportUnknown)
	i := slices.IndexFunc(lifecycles, func(l spells.Lifecycle) bool { return l.Key == row.Lifecycle })
	if i < 0 || row.InstalledVersion == "" {
		return
	}
	c, support := lifecycles[i].SupportOf(row.InstalledVersion, now)
	row.Cycle, row.EOL, row.Support = c.Cycle, c.EOL, string(support)
}
