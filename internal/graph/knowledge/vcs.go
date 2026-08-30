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

// vcsShardName is the singleton shard holding git-history attrs and author edges.
const vcsShardName = "@vcs"

// assembleVCS folds git history metadata onto the file nodes named by fileNodePaths - a
// typed partial file node per file so the merge is order-independent - and mints an author
// node per contributor with an `authored` edge to each (node-backed) file they touched.
// Entries and edges for a path with no file node are dropped, so nothing phantom is added.
// There is no per-author cap: the history window (knowledge.vcs.max_commits) already bounds
// the scan, so the edges are proportional to recent activity, not the whole repo - a
// dominant maintainer legitimately having many is a fact to teach the agent, not a smell,
// and any single query stays bounded by its neighborhood budget. Nodes and edges are sorted
// for deterministic output.
func assembleVCS(entries []types.KnowledgeVCS, fileNodePaths map[string]bool, authorship bool) Shard {
	s := Shard{Name: vcsShardName}
	authorFiles := map[string][]string{}
	for _, e := range entries {
		if !fileNodePaths[e.Path] {
			continue
		}
		if attrs := vcsAttrs(e); len(attrs) > 0 {
			s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: fileID(e.Path), Kind: types.KindFile, Label: e.Path, Attrs: attrs})
		}
		if !authorship {
			continue
		}
		for _, a := range e.Authors {
			if a != "" {
				authorFiles[a] = append(authorFiles[a], e.Path)
			}
		}
	}

	// One author node per contributor, with an `authored` edge to each file they touched
	// (omitted entirely when authorship is off). Sorted author order keeps it deterministic.
	for _, a := range slices.Sorted(maps.Keys(authorFiles)) {
		aID := authorID(a)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: aID, Kind: types.KindAuthor, Label: sanitize(a, maxLabelLen)})
		for _, f := range authorFiles[a] {
			s.Edges = append(s.Edges, extractedEdge(aID, fileID(f), types.RelationAuthored, ""))
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

// The @vcs attr keys, named once because IsHistoryAttr has to list exactly what vcsAttrs
// writes: a drifting spelling would leave a git-derived value in a reproducible export.
const (
	attrVCSLastCommit   = "vcs_last_commit"
	attrVCSLastModified = "vcs_last_modified"
	attrVCSLastAuthor   = "vcs_last_author"
	attrVCSCommits      = "vcs_commits"
)

// vcsAttrs renders a file's history metadata as node attrs. An empty commit (a file with
// no recorded history in the scanned window) yields no attrs, so the node is skipped.
func vcsAttrs(e types.KnowledgeVCS) map[string]string {
	if e.LastCommit == "" && e.Commits == 0 {
		return nil
	}
	attrs := map[string]string{}
	if e.LastCommit != "" {
		attrs[attrVCSLastCommit] = e.LastCommit
	}
	if !e.LastModified.IsZero() {
		attrs[attrVCSLastModified] = e.LastModified.UTC().Format(time.DateOnly)
	}
	if e.LastAuthor != "" {
		// The EMERGENT maintainer (who last touched the file), to set against a file's
		// DECLARED CODEOWNERS owner - the owner-of-record vs who is actually editing.
		attrs[attrVCSLastAuthor] = sanitize(e.LastAuthor, maxLabelLen)
	}
	if e.Commits > 0 {
		attrs[attrVCSCommits] = strconv.Itoa(e.Commits)
	}
	return attrs
}
