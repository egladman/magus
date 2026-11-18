package guard

import (
	"cmp"
	"context"
	"os"
	"slices"
)

// denyUnboundShellWrite refuses a shell line by an agent holding no job that writes into
// a checkout a job's fork made, as [denyUnboundJobCheckout] refuses the same write by the
// editor tool. The words graded are the ones lease-write places: a redirect target, a
// created or overwritten operand, a removed or moved one, and any other write candidate
// naming an existing file, since sed's `s/a/b` is spelled like a path.
func denyUnboundShellWrite(ctx context.Context, deps Dependencies, command string) string {
	location := hookLocation(ctx, deps)
	dialect := effectiveDialect(deps.ShellDialect)
	start, home := cmp.Or(deps.callDir, location.workspace), userHome()
	redirects := redirectTargets(command, 0, dialect)
	written := slices.Concat(destinationOperands(command, dialect, start, home),
		removedOrMovedOperands(command, 0, dialect, start, home), boundPathOperands(command, dialect, start))
	for _, candidate := range placedWriteCandidates(command, dialect, start, home) {
		if slices.Contains(redirects, candidate.word) {
			written = append(written, candidate)
			continue
		}
		if target, placed := candidate.place(home); placed {
			if info, err := os.Stat(target); err == nil && info.Mode().IsRegular() {
				written = append(written, candidate)
			}
		}
	}
	for _, w := range written {
		target, placed := w.place(home)
		if !placed {
			continue
		}
		if g := denyUnboundJobCheckout(ctx, location, target); g.Decision != "" {
			return g.Reason
		}
	}
	return ""
}
