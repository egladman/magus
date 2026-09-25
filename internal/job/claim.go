package job

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

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
