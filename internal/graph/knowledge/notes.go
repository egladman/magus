package knowledge

import (
	"strings"

	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
)

// sharedNotesShardName is the singleton shard holding human-authored note nodes and the
// annotates edges tying each to what it is about.
//
// Unlike @memory and @runtime this shard is WORKSPACE-DERIVED, and the difference is the
// whole point of keeping notes in the checkout. Its content is committed, so it is
// deterministic, shared by everyone who clones the repo, and remote-exportable like any
// other extracted shard. There is deliberately no local-shard exclusion here: the hazard
// that justifies one for @memory - leaking a developer's private journal into a shared
// cache - cannot arise for content that is already in the repository.
const sharedNotesShardName = "@notes/shared"

// privateNotesShardName holds the reader's OWN notes, which may live anywhere on disk.
//
// It is a separate shard from @notes for one non-negotiable reason: it is never exported
// to the remote cache. @notes is safe to share because its content is already committed to
// the repository everyone clones; personal notes are not in the repo at all, so pushing
// that shard would leak private content into a shared cache - the same hazard @memory's
// exclusion exists to prevent, and the reason a looser knowledge.notes.path would have
// been the wrong way to support this.
const privateNotesShardName = "@notes/private"

// isLocalShard reports whether a shard holds machine-local content that must never reach
// the remote cache. Kept as one predicate so a new local shard is added in a single place
// rather than at every push site.
func isLocalShard(name string) bool {
	return isRuntimeShard(name) || isCoverageShard(name) || name == privateNotesShardName
}

// attrScope distinguishes a note the team committed from one only this machine has, so
// the difference is visible wherever a note surfaces rather than inferred from a shard
// name a reader never sees.
const attrScope = "scope"

// Derived from the store's own scopes rather than restated, so the shard names, node
// IDs, and the config keys a diagnostic prints can never disagree about what a scope is
// called.
const (
	ScopeShared  = string(notes.ScopeShared)
	ScopePrivate = string(notes.ScopePrivate)
)

// assembleNotes emits one node per note plus an annotates edge to each anchor that
// RESOLVES against known, the set of node IDs the rest of the graph actually minted.
//
// An anchor naming a node that does not exist emits NO edge. A dangling edge would put a
// phantom in the graph and make every consumer defend against it, when the honest report
// belongs to `magus notes verify`, which can name the note, the anchor, and what to do.
// This mirrors assembleVCS dropping history for files the graph does not model.
//
// Source is the note's file path, which is what makes attribution free: the @vcs shard
// mints an author node per contributor and an authored edge to every file node it can see,
// so "who wrote this note" is answerable the day this shard lands, with no code here.
func assembleNotes(notes []types.KnowledgeNote, known map[string]bool, shard, scope string) Shard {
	s := Shard{Name: shard}
	for _, n := range notes {
		if n.Name == "" {
			continue
		}
		id := noteID(scope, n.Name)
		attrs := map[string]string{attrScope: scope}
		if len(n.Tags) != 0 {
			attrs[attrTags] = strings.Join(n.Tags, ",")
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:     id,
			Kind:   types.KindNote,
			Label:  sanitize(n.Title, maxLabelLen),
			Source: n.Path,
			Attrs:  attrs,
		})
		for _, target := range n.Anchors {
			if strings.TrimSpace(target) == "" || !known[target] {
				continue
			}
			s.Edges = append(s.Edges, extractedEdge(id, target, types.RelationAnnotates, n.Path))
		}
	}
	return s
}

// knownNodeIDs collects every node ID minted so far, plus the project and target IDs the
// target graph guarantees, so a note's anchors can be checked before they become edges.
//
// Notes are assembled last precisely so this set is complete for the kinds a note may
// anchor to: files and docs come from the passes above, projects and targets from the
// graph itself.
func knownNodeIDs(shards []Shard, graph types.TargetGraphOutput) map[string]bool {
	known := map[string]bool{}
	for _, sh := range shards {
		for _, n := range sh.Nodes {
			known[n.ID] = true
		}
	}
	for _, p := range graph.Projects {
		known[projectID(p.Path)] = true
		for _, n := range p.Nodes {
			known[targetID(p.Path, n.Name)] = true
		}
	}
	return known
}
