package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// notVerified opens the reason of a point the host's docs did not settle.
const notVerified = "not verified: "

// docURL is the page of the host's docs, or of its source, a reason rests on.
// A vendored copy does not count: it goes stale while it still cites.
var docURL = regexp.MustCompile(`https://[^\s()]+`)

// checkedOn is the ISO date a not-verified reason says its URL was read.
var checkedOn = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)

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
	}
	if err := citesSource(row.Reason); err != nil {
		return fmt.Errorf("%s gives the reason %q, %w", row.Point, row.Reason, err)
	}
	seen[row.Point] = true
	return nil
}

// InstalledHosts are the harnesses whose tool rows are installed under root,
// sorted. It is the registry a hook can read without loading the workspace:
// install writes a host file only for a harness the workspace loads.
func InstalledHosts(root string) []string {
	matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(toolRowsGlob)))
	var hosts []string
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if host := strings.TrimSpace(string(body)); err == nil && host != "" && !slices.Contains(hosts, host) {
			hosts = append(hosts, host)
		}
	}
	slices.Sort(hosts)
	return hosts
}

// CheckHostInstalled refuses a host that none of installed names. With
// nothing installed there is no registry to hold host to, and the missing
// tool rows are reported where they are read.
func CheckHostInstalled(installed []string, host string) error {
	if len(installed) == 0 || slices.Contains(installed, host) {
		return nil
	}
	return fmt.Errorf("--agent-name %q names no harness installed here; installed: %s. "+
		"Fix the hook entry's --agent-name, or install that harness with `magus agent harness install`",
		host, strings.Join(installed, ", "))
}

// citesSource refuses a reason with no https URL. A not-verified reason
// also owes the date its URL was read.
func citesSource(reason string) error {
	if !docURL.MatchString(reason) {
		return fmt.Errorf("which cites no https URL; cite the host's docs page, or say %q",
			notVerified+"<url checked> <YYYY-MM-DD> <what is unsettled>")
	}
	if strings.HasPrefix(reason, notVerified) && !checkedOn.MatchString(reason) {
		return errors.New("which names no date; say when the URL was read, as YYYY-MM-DD")
	}
	return nil
}
