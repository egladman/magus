package knowledge

import (
	"cmp"
	"slices"
	"strconv"
	"time"

	"github.com/egladman/magus/types"
)

// Git history is EXTRACTED, never inferred: the @vcs shard folds a file's last-commit
// SHA and time and its commit count onto the file node as attrs. Ownership-by-blame and
// author-churn analytics stay out (that is insight's job, not a verifiable graph fact).
// The values are deterministic per commit, so - unlike @runtime - the shard is
// remote-shareable. The scan itself lives in the composition root (git is not a
// dependency of this package); here we only shape the gathered metadata into a shard.

// VCSShardName is the singleton shard holding git-history attrs on file nodes.
const VCSShardName = "@vcs"

// assembleVCS folds git history metadata onto the file nodes named by fileNodePaths,
// emitting a typed partial file node per file so the merge is order-independent
// (whichever shard loads first, the buzz shard fills Label/Source and these attrs merge
// in). Entries for a path with no file node are dropped, so metadata for a file the
// graph does not model adds no phantom. Nodes are sorted by ID for deterministic output.
func assembleVCS(entries []types.KnowledgeVCS, fileNodePaths map[string]bool) Shard {
	s := Shard{Name: VCSShardName}
	for _, e := range entries {
		if !fileNodePaths[e.Path] {
			continue
		}
		attrs := vcsAttrs(e)
		if len(attrs) == 0 {
			continue
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    fileID(e.Path),
			Kind:  types.KindFile,
			Label: e.Path,
			Attrs: attrs,
		})
	}
	slices.SortFunc(s.Nodes, func(a, b types.KnowledgeNode) int { return cmp.Compare(a.ID, b.ID) })
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
	if e.Commits > 0 {
		attrs["vcs_commits"] = strconv.Itoa(e.Commits)
	}
	return attrs
}
