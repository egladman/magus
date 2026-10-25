package job

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// unusedWritePaths returns the hard write paths of holder its footprint has not touched, the
// ones it could release to a job refused for wanting them. The footprint is holder's diff
// since its checkpoint in the checkout that took it, read through the store's version
// control. A path entry is used when a changed file falls under it ([types.PathWithin]); a
// claim `F#d` is used when a changed line of F lands in a declaration d names.
//
// A footprint that cannot be read returns nil, and a holder that has used every path
// returns an empty, non-nil slice, so a caller never reads "unknown" as "all used".
func unusedWritePaths(ctx context.Context, store *Store, rows []types.Job, holder types.Job) []string {
	if store == nil || store.root == "" {
		return nil
	}
	if !slices.ContainsFunc(rows, func(r types.Job) bool { return r.ID == holder.ID }) {
		rows = append(slices.Clone(rows), holder)
	}
	driver, _ := resolveDriver(ctx, store.root)
	fp := readFootprint(withFootprintCache(ctx, store.footprintCache()), driver, rows, holder.ID)
	if !fp.known {
		return nil
	}
	used := indexRegions(fp.regions)
	unused := []string{}
	for _, entry := range holder.HardPaths() {
		if !used.uses(entry) {
			unused = append(unused, entry)
		}
	}
	return unused
}

// usesEntry reports whether a change at loc falls under the write path entry. A change no
// driver placed could be in any declaration of its file, so it uses every claim on it.
func usesEntry(entry string, loc types.Location) bool {
	file, decl := types.SplitClaim(entry)
	switch {
	case decl == "":
		return types.PathWithin(loc.Path, entry)
	case path.Clean(file) != path.Clean(loc.Path):
		return false
	}
	return loc.Declaration == "" || types.NamesDeclaration(decl, loc.Declaration)
}

// regionIndex is a footprint's changed regions sorted by path, so asking whether a
// write path entry was used looks up the files under it rather than walking every
// region: an integration row holds hundreds of entries against a diff of thousands.
type regionIndex struct {
	paths []string            // cleaned literal paths, sorted, once each
	decls map[string][]string // declarations changed in each path; "" when unplaced
	odd   []types.Location    // regions with no path or a glob character in it
}

func indexRegions(regions []types.RegionChange) regionIndex {
	x := regionIndex{decls: map[string][]string{}}
	for _, r := range regions {
		loc := r.Location()
		p := path.Clean(loc.Path)
		if loc.Path == "" || types.IsGlobMeta(p) {
			x.odd = append(x.odd, loc)
			continue
		}
		if _, seen := x.decls[p]; !seen {
			x.paths = append(x.paths, p)
		}
		x.decls[p] = append(x.decls[p], loc.Declaration)
	}
	slices.Sort(x.paths)
	return x
}

// uses reports what usesEntry would for any one region of the index. Only a path at or
// under the entry's literal prefix can fall under the entry, so only those are asked.
func (x regionIndex) uses(entry string) bool {
	if slices.ContainsFunc(x.odd, func(loc types.Location) bool { return usesEntry(entry, loc) }) {
		return true
	}
	file, decl := types.SplitClaim(entry)
	if file == "" {
		return false
	}
	file = path.Clean(file)
	if decl != "" {
		return slices.ContainsFunc(x.decls[file], func(d string) bool {
			return usesEntry(entry, types.Location{Path: file, Declaration: d})
		})
	}
	return slices.ContainsFunc(x.under(types.LiteralPrefix(file)), func(p string) bool {
		return usesEntry(entry, types.Location{Path: p})
	})
}

// under is the indexed paths equal to prefix or beneath it; every path is under "".
// Paths beneath prefix are contiguous from prefix+"/", not from prefix, since a sibling
// such as prefix+"-x" sorts between the two.
func (x regionIndex) under(prefix string) []string {
	if prefix == "" {
		return x.paths
	}
	var out []string
	if _, ok := x.decls[prefix]; ok {
		out = append(out, prefix)
	}
	dir := prefix + "/"
	start, _ := slices.BinarySearch(x.paths, dir)
	end := start
	for end < len(x.paths) && strings.HasPrefix(x.paths[end], dir) {
		end++
	}
	return append(out, x.paths[start:end]...)
}

// describeUnused is unusedWritePaths as a refusal names it.
func describeUnused(holder string, unused []string) string {
	switch {
	case unused == nil:
		return fmt.Sprintf("%s's footprint could not be read, so which of its write paths it has not used is unknown", holder)
	case len(unused) == 0:
		return fmt.Sprintf("%s has changed something under every one of its write paths", holder)
	}
	return fmt.Sprintf("%s has changed nothing under %s", holder, strings.Join(unused, ", "))
}

// narrowAdvice is the clause a contention refusal's fix ends with when holder has write
// paths it has not used: the root narrows the holder's row to its footprint. It is "" when
// there is nothing to drop or nobody could read what there is.
func narrowAdvice(holder string, unused []string) string {
	if len(unused) == 0 {
		return ""
	}
	return fmt.Sprintf("; or the root narrows %s to what it has changed with `%s`, dropping %s",
		holder, hint.JobApply.With("-f", "<record>"), strings.Join(unused, ", "))
}
