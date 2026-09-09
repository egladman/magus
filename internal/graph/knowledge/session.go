package knowledge

import (
	"maps"
	"path"
	"slices"
	"strconv"

	"github.com/egladman/magus/types"
)

// Agent-session ingestion is OBSERVED, not extracted: `magus session load` folds an
// agent host's transcript into the per-repo session store, and this pass rolls those
// events up into per-file and per-directory contact counts on the nodes the rest of the
// graph already minted. It answers "which code do agents actually touch" without a
// second query, and it never mints a node: an event naming a path the graph does not
// model is counted and dropped, never turned into a phantom.
//
// Events themselves stay out of the graph. Merge is set-union over (source, target,
// relation), so repeated contact with one file would collapse to a single edge, losing
// the count, which is the entire signal. Aggregates on existing nodes survive that
// merge; per-event nodes cannot.

// sessionShardName is the isolated shard holding the agent-contact overlay.
//
// It is LAZY, like @coverage and for the same reason rather than by analogy: measured on
// this workspace the default graph holds 224 file nodes, every one a .buzz path, because
// Go file nodes are minted by the SCIP ingestion into the per-project @symbols shards.
// Agent contact is overwhelmingly with .go files, so a shard merged into the default
// graph would resolve almost nothing and read as "agents touch no code". Merging on the
// symbol-load path puts the overlay where its targets live.
const sessionShardName = "@session"

// isSessionShard reports whether name is the agent-contact overlay shard.
func isSessionShard(name string) bool { return name == sessionShardName }

// AgentContact is one loaded agent event reduced to what the overlay reads: which
// session touched which path, when, and whether today's guard rules deny it.
//
// It mirrors FileCoverage: the composition root decodes the session store and hands this
// package a plain slice, so internal/graph/knowledge keeps depending only on types. Read
// and Write are booleans rather than the store's event-kind string so the host-agnostic
// event vocabulary lives in exactly one package; this one only counts.
//
// Path is workspace-relative and may be empty, which is the normal case for a command.
type AgentContact struct {
	Session string
	Path    string
	Read    bool
	Write   bool
	At      int64 // unix milliseconds, when the HOST recorded the event
	Denied  bool
}

// assembleSession builds the @session overlay: a partial file node per contacted path
// carrying agent_sessions / agent_reads / agent_writes / agent_denials /
// agent_last_touched, plus the same attrs rolled up onto every ancestor directory node,
// the way @dirs rolls file aggregates up its containment chain.
//
// known is every node ID the graph has minted, and resolution against it is the safety
// property: a path with no file node contributes nothing and increments Shard.Dropped,
// and a directory node that does not exist is skipped rather than created. Without that
// a months-old transcript would repopulate the graph with paths that have since been
// deleted.
//
// DENIAL ATTRIBUTION, the rule most likely to be misread: a denial is credited only to
// an event that CARRIES A PATH. A shell.command event holds a command, and a command is
// not about a file: `cd ../other-worktree` names a directory the guard refused to run
// magus in, not a file the agent was editing. Crediting those to the session's cwd would
// put a number on a directory nobody touched, and "where do denials concentrate" would
// answer with wherever agents happen to work. Path-less events are counted in Dropped
// and land on no node; per-session denial history is `magus session show`'s to report.
//
// All nodes are partial (ID + kind + attrs), so they merge onto the real file and dir
// nodes whichever shard the loader reaches first.
func assembleSession(contacts []AgentContact, known map[string]bool) Shard {
	s := Shard{Name: sessionShardName}
	if len(contacts) == 0 {
		return s
	}

	type agg struct {
		kind     string
		sessions map[string]bool
		reads    int
		writes   int
		denials  int
		lastAt   int64
	}
	byNode := map[string]*agg{}
	tally := func(id, kind string, c AgentContact) {
		a := byNode[id]
		if a == nil {
			a = &agg{kind: kind, sessions: map[string]bool{}}
			byNode[id] = a
		}
		if c.Session != "" {
			a.sessions[c.Session] = true
		}
		if c.Read {
			a.reads++
		}
		if c.Write {
			a.writes++
		}
		if c.Denied {
			a.denials++
		}
		if c.At > a.lastAt {
			a.lastAt = c.At
		}
	}

	for _, c := range contacts {
		fID := fileID(c.Path)
		if c.Path == "" || !known[fID] {
			s.Dropped++
			continue
		}
		tally(fID, types.KindFile, c)
		// Ancestors are walked to the tree root and gated on known, rather than stopping at
		// an owning project the way assembleDirs does. The gate is the stricter of the two:
		// containsChain mints no dir node at or above a project root, so no such node is
		// ever in known, and this needs no project list to say so.
		for d := path.Dir(c.Path); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if dID := dirID(d); known[dID] {
				tally(dID, types.KindDir, c)
			}
		}
	}

	for _, id := range slices.Sorted(maps.Keys(byNode)) {
		a := byNode[id]
		attrs := map[string]string{}
		if n := len(a.sessions); n > 0 {
			attrs[AttrAgentSessions] = strconv.Itoa(n)
		}
		if a.reads > 0 {
			attrs[AttrAgentReads] = strconv.Itoa(a.reads)
		}
		if a.writes > 0 {
			attrs[AttrAgentWrites] = strconv.Itoa(a.writes)
		}
		if a.denials > 0 {
			attrs[AttrAgentDenials] = strconv.Itoa(a.denials)
		}
		if a.lastAt > 0 {
			attrs[AttrAgentLastTouched] = strconv.FormatInt(a.lastAt, 10)
		}
		if len(attrs) == 0 {
			continue
		}
		s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: id, Kind: a.kind, Attrs: attrs})
	}
	return s
}
