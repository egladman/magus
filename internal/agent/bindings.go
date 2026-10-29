package agent

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// notVerified opens the reason of a point nobody has checked the host for.
const notVerified = "not verified: "

// docCite is a slash-joined path or URL in a reason: where the host's
// documentation, or a vendored schema of it, says the hook is absent.
var docCite = regexp.MustCompile(`[\w.-]+/[\w./#:-]+`)

// HookBindings is d's binding table, one row per hook point in HookPoints
// order. A point a hook lists is Bound; any other takes its row from
// d.UnsupportedHooks.
//
// The error names every point with no row, and every Unsupported row a
// hook binds anyway. The table comes back whole either way.
func HookBindings(d HarnessDescriptor) ([]types.HookBinding, error) {
	unsupported := map[types.HookPoint]types.HookBinding{}
	for _, row := range d.UnsupportedHooks {
		unsupported[row.Point] = row
	}
	var errs []error
	points := types.HookPoints()
	table := make([]types.HookBinding, 0, len(points))
	for _, point := range points {
		row := types.HookBinding{Harness: d.ID, Point: point}
		scripts := boundScripts(d.Hooks, point)
		reason, said := unsupported[point]
		switch {
		case len(scripts) > 0:
			row.Status, row.Adapter = types.Bound, strings.Join(scripts, ", ")
			if said {
				errs = append(errs, fmt.Errorf(
					"%s: mgs_listUnsupportedHooks says %s is unsupported, but %s binds it; drop the row",
					d.ID, point, row.Adapter))
			}
		case said:
			row.Status, row.Reason = types.Unsupported, reason.Reason
		default:
			errs = append(errs, fmt.Errorf(
				"%s: no hook binds %s and mgs_listUnsupportedHooks gives no reason; "+
					"list %s in a hook's points, or add an Unsupported row citing the host's docs",
				d.ID, point, point))
		}
		table = append(table, row)
	}
	return table, errors.Join(errs...)
}

// boundScripts are the distinct scripts of the hooks that list point, in
// the order the hooks come.
func boundScripts(hooks []types.HookCommand, point types.HookPoint) []string {
	var scripts []string
	for _, h := range hooks {
		if slices.Contains(h.Points, point) && !slices.Contains(scripts, h.Script) {
			scripts = append(scripts, h.Script)
		}
	}
	return scripts
}

// validateHookBindings refuses rows that cannot be read. A point with no
// row is not refused here: HookBindings reports it, through doctor, so a
// copied spell still loads to say what it lacks.
func validateHookBindings(d HarnessDescriptor) error {
	for i, h := range d.Hooks {
		for _, point := range h.Points {
			if !point.Valid() {
				return fmt.Errorf("hooks[%d]: unknown hook point %q; one of %s",
					i, point, strings.Join(point.Values(), ", "))
			}
		}
	}
	seen := map[types.HookPoint]bool{}
	for i, row := range d.UnsupportedHooks {
		if err := validateUnsupportedRow(d.ID, row, seen); err != nil {
			return fmt.Errorf("unsupported_hooks[%d]: %w", i, err)
		}
	}
	return nil
}

func validateUnsupportedRow(id string, row types.HookBinding, seen map[types.HookPoint]bool) error {
	switch {
	case !row.Point.Valid():
		return fmt.Errorf("unknown hook point %q; one of %s",
			row.Point, strings.Join(row.Point.Values(), ", "))
	case seen[row.Point]:
		return fmt.Errorf("a second row for %s", row.Point)
	case row.Status != types.Unsupported:
		return fmt.Errorf("%s is %s: this list holds Unsupported rows only, "+
			"and a Bound point is one a hook lists", row.Point, row.Status)
	case row.Harness != "" && row.Harness != id:
		return fmt.Errorf("%s names harness %q, not %q", row.Point, row.Harness, id)
	case row.Adapter != "":
		return fmt.Errorf("%s names adapter %q, but nothing runs at an unsupported point",
			row.Point, row.Adapter)
	case !citesSource(row.Reason):
		return fmt.Errorf("%s gives the reason %q, which cites nothing; cite the host's docs, "+
			"or say %q and what is unchecked", row.Point, row.Reason, notVerified+"<what>")
	}
	seen[row.Point] = true
	return nil
}

// citesSource reports whether a reason cites the host's docs or says
// plainly that nobody checked.
func citesSource(reason string) bool {
	if rest, ok := strings.CutPrefix(reason, notVerified); ok {
		return strings.TrimSpace(rest) != ""
	}
	return docCite.MatchString(reason)
}
