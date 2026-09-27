package job

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/describe"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// RefuseUngradableClaims refuses a fork whose write paths claim a declaration
// (`run.go#executeStages`, see types.SplitClaim) that the footprint could never grade
// (MGS3031): an empty declaration, a claim on a glob rather than one file, a checkout whose
// version control does not place changed lines, or a file whose extension has no diff
// driver. An entry that names an existing path as written (`notes/a#b.md`) is refused as
// ambiguous, naming the two spellings that mean the path. A claim nothing checks reads as a boundary and is none, so it is an error rather
// than a note on the row.
//
// Only a fork with a `#` entry reads the version control; any other fork refuses nothing
// here. A nil store, or one with no workspace root, has no tree to ask and refuses nothing.
func RefuseUngradableClaims(ctx context.Context, store *Store, id string, candidate types.Job) error {
	if store == nil || store.root == "" {
		return nil
	}
	var claimed, refused []string
	for _, entry := range candidate.WritePaths {
		if !types.HasClaim(entry) {
			continue
		}
		p, decl := types.SplitClaim(entry)
		literal := strings.TrimSpace(entry)
		if _, err := os.Lstat(filepath.Join(store.root, filepath.FromSlash(literal))); err == nil {
			refused = append(refused, fmt.Sprintf("%q is a path in this tree and also reads as a claim on %q; spell the path `./%s` or `%s`,"+
				" since an unescaped # starts a claimed declaration", entry, p, literal, strings.ReplaceAll(literal, "#", `\#`)))
			continue
		}
		switch {
		case decl == "":
			refused = append(refused, fmt.Sprintf("%q names no declaration after its #", entry))
		case p == "" || strings.ContainsAny(p, globMeta):
			refused = append(refused, fmt.Sprintf("%q claims a declaration of a pattern, and a declaration lives in one file", entry))
		default:
			claimed = append(claimed, path.Clean(p))
		}
	}
	if len(refused) == 0 && len(claimed) > 0 {
		drivers, why := claimDrivers(ctx, store.root, claimed)
		for _, p := range claimed {
			switch {
			case why != "":
				refused = append(refused, fmt.Sprintf("%q: %s", p, why))
			case drivers[p] == "":
				refused = append(refused, fmt.Sprintf("%q has no diff driver, so no changed line of it can be placed in a declaration;"+
					" give %s one in .gitattributes (`magus doctor` lists the managed ones)", p, driverPattern(p)))
			}
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return types.DiagnosticErrorf(types.WritePathClaimUngradable,
		"job: %s claims a declaration no footprint can grade: %s. Claim the whole file instead, or fix what is named",
		id, strings.Join(refused, "; "))
}

// claimDrivers asks root's version control which diff driver names each path's
// declarations, or says why it cannot.
func claimDrivers(ctx context.Context, root string, paths []string) (map[string]string, string) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	switch {
	case err != nil:
		return nil, fmt.Sprintf("no version control answered here (%v)", err)
	case res.Source == types.VCSSourceDisabled || res.VCS == nil:
		return nil, "version control is disabled here, so no diff places a changed line"
	}
	drivers, err := res.VCS.Drivers(ctx, root, paths)
	if err != nil {
		return nil, fmt.Sprintf("its diff driver could not be read: %v", err)
	}
	return drivers, ""
}

// driverPattern is the .gitattributes pattern a driver for p would be declared under.
func driverPattern(p string) string {
	if ext := path.Ext(p); ext != "" {
		return "`*" + ext + "`"
	}
	return "`" + path.Base(p) + "`"
}

// RefuseUnorderedFileShare refuses a fork whose write paths would hand one claimable file
// (no glob, not a directory, not a project root, with a diff driver) to two live, unordered
// jobs at once (MGS3032): a candidate entry and a holder's entry name the same file, still
// intersect (types.PathsIntersect), and neither row is the other's ancestor, descendant, or
// depends_on partner.
//
// Left alone: a same-checkout or not-yet-taken holder (CheckoutRoot == ""), which
// RefuseSharedCheckout and the overlap report already watch; a holder blocked on
// depends_on, which owns none of its write paths yet (types.JobBlockedOn); and a file with
// no diff driver, for which depends_on is the only move.
func RefuseUnorderedFileShare(ctx context.Context, store *Store, rows []types.Job, id string, candidate types.Job) error {
	if store == nil || store.root == "" || len(candidate.WritePaths) == 0 {
		return nil
	}
	here, _ := filepath.Abs(store.root)
	combined := append(slices.Clone(rows), candidate)
	for _, mine := range candidate.WritePaths {
		file, ok := literalClaimableFile(store.root, mine)
		if !ok {
			continue
		}
		if _, driven := types.DiffDriverFor(file); !driven {
			continue
		}
		for _, holder := range rows {
			if holder.ID == id || !holder.State.Live() || len(holder.WritePaths) == 0 {
				continue
			}
			// A row nobody has taken yet is not in a different checkout from anyone.
			if holder.CheckoutRoot == "" || (here != "" && holder.CheckoutRoot == here) {
				continue
			}
			// Blocked means holder owns none of its write paths yet; the fork that
			// unblocks it runs this same check again, against whatever holds the file then.
			if _, blocked := types.JobBlockedOn(rows, holder); blocked {
				continue
			}
			if orderedPair(combined, id, holder.ID) {
				continue
			}
			for _, theirs := range holder.WritePaths {
				if other, ok := literalClaimableFile(store.root, theirs); !ok || other != file {
					continue
				}
				if !types.PathsIntersect(mine, theirs) {
					continue
				}
				touched := touchedInFile(ctx, store, rows, holder.ID, file)
				return types.DiagnosticErrorf(types.WritePathFileShared,
					"job: %s declares %q, and %s (%s, updated %s ago) already holds %q. Both are live and neither"+
						" is the other's parent, child, or depends_on partner, so %s has one owner unless the rows"+
						" say otherwise. %s has touched %s in it so far. Claim `%s#<declaration>` on both rows, add"+
						" `--depends-on %s` to %s, or fold %s into %s",
					id, mine, holder.ID, holder.State, updatedAgo(holder), theirs,
					file,
					holder.ID, touched,
					file, holder.ID, id,
					id, holder.ID)
			}
		}
	}
	return nil
}

// literalClaimableFile is the file entry names, whole or by declaration, cleaned and
// workspace-relative, or "" when entry is a glob, the whole tree, a project root, or an
// existing directory: none of those can be shared by a claim, so RefuseUnorderedFileShare
// has nothing to compare them against.
func literalClaimableFile(root, entry string) (string, bool) {
	p, _ := types.SplitClaim(entry)
	p = path.Clean(strings.TrimSpace(p))
	if p == "" || p == "." || strings.ContainsAny(p, globMeta) {
		return "", false
	}
	if describe.IsProjectRoot(root, p) {
		return "", false
	}
	if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err == nil && info.IsDir() {
		return "", false
	}
	return p, true
}

// orderedPair reports whether id and other are already sequenced: one an ancestor or
// descendant of the other by parent chain, walked over combined (which must carry id even
// when it is not declared yet), or one reaches the other through depends_on, any number of
// hops.
func orderedPair(combined []types.Job, id, other string) bool {
	if slices.ContainsFunc(types.JobAncestors(combined, id), func(r types.Job) bool { return r.ID == other }) {
		return true
	}
	if slices.ContainsFunc(types.JobAncestors(combined, other), func(r types.Job) bool { return r.ID == id }) {
		return true
	}
	return dependsOnTransitively(combined, id, other) || dependsOnTransitively(combined, other, id)
}

// dependsOnTransitively reports whether from reaches on by following depends_on edges over
// combined. seen guards a depends_on cycle from looping forever.
func dependsOnTransitively(combined []types.Job, from, on string) bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		i := slices.IndexFunc(combined, func(r types.Job) bool { return r.ID == queue[0] })
		queue = queue[1:]
		if i < 0 {
			continue
		}
		for _, dep := range combined[i].DependsOn {
			if dep == on {
				return true
			}
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, dep)
			}
		}
	}
	return false
}

// touchedInFile is what holder has changed inside file so far, restricted to file, for the
// refusal to name: the declarations already touched, "none yet" before any diff exists, or
// why that cannot be read. Reads the tree once per refusal; a fork this rule passes never
// pays for it.
func touchedInFile(ctx context.Context, store *Store, rows []types.Job, holder, file string) string {
	driver, _ := resolveDriver(ctx, store.root)
	fp := readFootprint(ctx, driver, rows, holder)
	if !fp.known {
		reason := fp.reason
		if reason == "" {
			reason = "its footprint could not be read"
		}
		return fmt.Sprintf("unknown (%s)", reason)
	}
	var decls []string
	for _, r := range fp.regions {
		loc := r.Location()
		if loc.Path != file {
			continue
		}
		d := loc.Declaration
		if d == "" {
			d = "the top of the file"
		}
		if !slices.Contains(decls, d) {
			decls = append(decls, d)
		}
	}
	if len(decls) == 0 {
		return "none yet"
	}
	slices.Sort(decls)
	return strings.Join(decls, ", ")
}

// updatedAgo is how long ago row last wrote itself, as a refusal names it.
func updatedAgo(row types.Job) string {
	return time.Since(time.Unix(row.Updated, 0)).Round(time.Second).String()
}
