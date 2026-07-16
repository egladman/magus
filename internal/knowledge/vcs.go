package knowledge

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/egladman/magus/types"
)

// Git history is EXTRACTED, never inferred: the @vcs shard folds a file's last-commit
// SHA/time/count onto the file node as attrs, and adds an `author` node per contributor
// with `authored` edges to the files they touched. These are verifiable FACTS (who edited
// what); the aggregate ANALYTICS (concentration, bus factor) stay in insight. The values
// are deterministic per commit, so - unlike @runtime - the shard is remote-shareable, and
// it is keyed on the git input alone (path-based, no project structure) so the input-
// fingerprint cache can reuse it whole. The scan lives in the composition root (git is not
// a dependency of this package); here we only shape the gathered metadata into a shard.

// VCSShardName is the singleton shard holding git-history attrs and author edges.
const VCSShardName = "@vcs"

// maxAuthorFanout caps how many `authored` edges one author may emit. A contributor over
// it (a solo maintainer who touches nearly every file) would be a god node that informs
// little, so they get a files_authored COUNT attr and no edges instead. Same guard as
// maxIOFanout, just per author.
const maxAuthorFanout = 60

// assembleVCS folds git history metadata onto the file nodes named by fileNodePaths - a
// typed partial file node per file so the merge is order-independent - and mints an author
// node per contributor with `authored` edges to the (node-backed) files they touched.
// Entries and edges for a path with no file node are dropped, so nothing phantom is added.
// Nodes and edges are sorted for deterministic output.
func assembleVCS(entries []types.KnowledgeVCS, fileNodePaths map[string]bool) Shard {
	s := Shard{Name: VCSShardName}
	authorFiles := map[string][]string{}
	for _, e := range entries {
		if !fileNodePaths[e.Path] {
			continue
		}
		if attrs := vcsAttrs(e); len(attrs) > 0 {
			s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: fileID(e.Path), Kind: types.KindFile, Label: e.Path, Attrs: attrs})
		}
		for _, a := range e.Authors {
			if a != "" {
				authorFiles[a] = append(authorFiles[a], e.Path)
			}
		}
	}

	// One author node per contributor. Under the fan-out cap, an `authored` edge to each
	// file they touched; over it, a files_authored count instead of a god node's worth of
	// edges. Sorted author order keeps the shard deterministic.
	for _, a := range slices.Sorted(maps.Keys(authorFiles)) {
		files := authorFiles[a]
		node := types.KnowledgeNode{ID: authorID(a), Kind: types.KindAuthor, Label: sanitize(a, maxLabelLen)}
		if len(files) > maxAuthorFanout {
			node.Attrs = map[string]string{AttrFilesAuthored: strconv.Itoa(len(files))}
			s.Nodes = append(s.Nodes, node)
			continue
		}
		s.Nodes = append(s.Nodes, node)
		for _, f := range files {
			s.Edges = append(s.Edges, extractedEdge(authorID(a), fileID(f), types.RelationAuthored, ""))
		}
	}

	slices.SortFunc(s.Nodes, func(a, b types.KnowledgeNode) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(s.Edges, func(a, b types.KnowledgeEdge) int {
		if c := cmp.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		return cmp.Compare(a.Target, b.Target)
	})
	return s
}

// vcsAttrs renders a file's history metadata as node attrs. An empty commit (a file with
// no recorded history in the scanned window) yields no attrs, so the node is skipped.
func vcsAttrs(e types.KnowledgeVCS) map[string]string {
	if e.LastCommit == "" && e.Commits == 0 {
		return nil
	}
	attrs := map[string]string{}
	if e.LastCommit != "" {
		attrs["vcs_last_commit"] = e.LastCommit
	}
	if e.LastUnix > 0 {
		attrs["vcs_last_modified"] = time.Unix(e.LastUnix, 0).UTC().Format("2006-01-02")
	}
	if e.LastAuthor != "" {
		// The EMERGENT maintainer (who last touched the file), to set against a file's
		// DECLARED CODEOWNERS owner - the owner-of-record vs who is actually editing.
		attrs["vcs_last_author"] = sanitize(e.LastAuthor, maxLabelLen)
	}
	if e.Commits > 0 {
		attrs["vcs_commits"] = strconv.Itoa(e.Commits)
	}
	return attrs
}
