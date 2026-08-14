package knowledge

import (
	"strconv"
	"time"

	"github.com/egladman/magus/types"
)

// Prose staleness levels, folded onto doc and note nodes and read by retrieval ranking.
//
// The signal is NOT calendar age. A doc written three years ago about a subsystem nobody
// has touched since is perfectly current, and decaying it by age would flag it for no
// reason - which is how a signal earns the right to be ignored. What is measured instead
// is DIVERGENCE: the prose was last touched, and then the thing it describes moved on
// without it. That is a fact about two commit dates, not a heuristic about age.
const (
	StalenessCurrent   = "current"   // the prose is at least as recent as its subject
	StalenessOutrun    = "outrun"    // the subject changed after the prose did
	StalenessPetrified = "petrified" // the subject has been moving for a long time without it
)

// AttrStaleness and AttrOutrunDays carry the divergence onto a prose node.
//
// AttrOutrunDays is the raw number so a reader can judge for themselves and a UI can say
// WHY something ranked low. The bucket exists only so ranking has something coarse to key
// on; the number is the evidence, and it is the thing to show.
const (
	AttrStaleness   = "staleness"
	AttrOutrunDays  = "outrun_days"
	petrifiedCutoff = 365 // days outrun before a doc reads as petrified rather than merely behind
)

// annotateProseStaleness folds a staleness bucket onto every doc and note node whose
// subject has moved on without it.
//
// It runs as a POST-PASS because it needs edges across shards: the prose node comes from
// @docs or @notes, its subject from @buzz or @symbols, and the git dates from the VCS
// input. Nothing is annotated when VCS history is unavailable (knowledge.vcs.enabled is
// off by default), and that silence is deliberate - inventing a staleness signal from no
// data would be worse than having none.
//
// Both directions of missing data are silence, never a verdict: prose git history absent,
// subject history absent, or no subject at all all yield no attr. A node with no staleness
// attr is not "fresh", it is "unmeasured", and ranking treats it as such.
func annotateProseStaleness(shards []Shard, vcsByPath map[string]types.KnowledgeVCS) {
	if len(vcsByPath) == 0 {
		return
	}
	// Source path per node id, so an edge target can be resolved back to a file.
	sourceOf := map[string]string{}
	for _, sh := range shards {
		for _, n := range sh.Nodes {
			if n.Source != "" {
				sourceOf[n.ID] = n.Source
			}
		}
	}
	// Subjects per prose node, from the two relations that mean "this prose is about that".
	subjects := map[string][]string{}
	for _, sh := range shards {
		for _, e := range sh.Edges {
			switch e.Relation {
			case types.RelationDocuments, types.RelationAnnotates:
				subjects[e.Source] = append(subjects[e.Source], e.Target)
			}
		}
	}

	for si := range shards {
		// Bound once and index the local slice: the nodes are mutated in place, so this
		// must stay a pointer into the shard's own backing array rather than a copy.
		nodes := shards[si].Nodes
		for ni := range nodes {
			n := &nodes[ni]
			if n.Kind != types.KindDoc && n.Kind != types.KindNote {
				continue
			}
			prose, ok := vcsByPath[n.Source]
			if !ok || prose.LastModified.IsZero() {
				continue // no history for the prose itself: unmeasured, not fresh
			}
			var newest time.Time
			for _, target := range subjects[n.ID] {
				src, ok := sourceOf[target]
				if !ok {
					continue
				}
				if sv, ok := vcsByPath[src]; ok && sv.LastModified.After(newest) {
					newest = sv.LastModified
				}
			}
			if newest.IsZero() {
				continue // nothing measurable to compare against
			}
			days := outrunDays(prose.LastModified, newest)
			if n.Attrs == nil {
				n.Attrs = map[string]string{}
			}
			switch {
			case days <= 0:
				n.Attrs[AttrStaleness] = StalenessCurrent
			case days >= petrifiedCutoff:
				n.Attrs[AttrStaleness] = StalenessPetrified
				n.Attrs[AttrOutrunDays] = strconv.Itoa(days)
			default:
				n.Attrs[AttrStaleness] = StalenessOutrun
				n.Attrs[AttrOutrunDays] = strconv.Itoa(days)
			}
		}
	}
}

// outrunDays is how far the prose is behind its subject, in CALENDAR days.
//
// Subtracting the raw instants truncates: prose at 23:00 and a subject edited 47 hours
// later reports 1, which reads as "yesterday" across two dates. Days are also the only unit
// the graph publishes (vcs_last_modified is a date), so this is what a reader can check.
func outrunDays(prose, subject time.Time) int {
	day := func(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }
	return int(day(subject).Sub(day(prose)).Hours() / 24)
}

// stalenessPenalty is what retrieval subtracts from a prose node's score.
//
// Ranking by this is a deliberate choice with a real cost: a weight moves results without
// the reader seeing it happen. That is why the penalty is coarse, bounded, and always
// accompanied by the evidence - AttrOutrunDays travels with the match so a caller can say
// "ranked down: 400 days behind its subject" rather than silently reordering. An
// unmeasured node (no attr) is never penalized; absence of evidence is not evidence.
func stalenessPenalty(attrs map[string]string) int {
	switch attrs[AttrStaleness] {
	case StalenessPetrified:
		return 30
	case StalenessOutrun:
		return 10
	default:
		return 0
	}
}

// vcsByPath indexes the VCS input for lookup by workspace-relative path.
func vcsByPath(entries []types.KnowledgeVCS) map[string]types.KnowledgeVCS {
	if len(entries) == 0 {
		return nil
	}
	byPath := make(map[string]types.KnowledgeVCS, len(entries))
	for _, e := range entries {
		byPath[e.Path] = e
	}
	return byPath
}
