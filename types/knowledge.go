package types

// Knowledge-graph schema: the deterministic, derived graph of the magus domain
// (projects, targets, spells, ops, charms, modules, methods, diagnostics, and -
// later - docs and buzz source nodes). Every node and edge is EXTRACTED or
// rubric-INFERRED from parseable workspace sources; nothing here is LLM-authored
// or otherwise unverifiable. These are pure domain types (stdlib-only leaf); the
// builder lives in internal/knowledge and the CLI surface in cmd/magus.

// KnowledgeSchemaVersion is stamped into every exported graph, shard, and manifest.
// External consumers - agent skills, MCP tools, other tools reading the node-link
// JSON - check it; a bump is a changelog event. Increment when the node/edge shape
// or ID scheme changes in a way that would break a consumer that parsed the old form.
// v2 added a "command" kind; v3 a "tool" kind coupled to it. v4 retires "command"
// (its rendered argv was always identical to the op's static base command, so it was
// a redundant copy of the op) and moves the model onto the op: an op carries an `argv`
// attr and `uses` the tool (argv[0]) it runs, so `explain tool:go` reaches every op
// that runs go and a target reaches its tool via target->op->tool. v2/v3 were unreleased.
// v5 adds the build I/O layer: `produces`/`consumes` edges from a target's declared
// magus.outputs/inputs to the file and doc nodes they match, so a generated file is
// self-labeled by its producing target; plus workspace-wide authored-markdown doc nodes
// carrying a `role` attr (readme/agent/changelog/...) and a `documents` edge to their project.
// v6 adds the "author" kind: a git contributor, with `authored` edges to the files they
// touched (the EMERGENT maintainer, to set against a file's DECLARED CODEOWNERS owner).
const KnowledgeSchemaVersion = 6

// KnowledgeGraphDefinition is the human-readable description printed by
// "magus graph export".
const KnowledgeGraphDefinition = "The knowledge graph is a deterministic, " +
	"cache-backed graph of the magus domain: projects, targets, spells, ops, charms, " +
	"modules, methods, and diagnostics, connected by verified relations (depends_on, " +
	"contains, uses, references, documents). Every node and edge is extracted or " +
	"rubric-inferred from workspace sources - no LLM pass - so it is safe to rebuild " +
	"implicitly and query instead of grepping."

// Knowledge node kinds. The universe is the magus domain, not general source code.
// Values are stable wire strings and the <kind> segment of a node ID.
const (
	KindProject = "project"
	KindTarget  = "target"
	KindSpell   = "spell"
	KindOp      = "op"
	KindTool    = "tool" // the program an op runs (argv[0]); ops and spells `use` it
	KindCharm   = "charm"
	KindModule  = "module"
	// method, function, and symbol are all "a callable definition", kept distinct by
	// PROVENANCE (which layer produced them), not by an accident of naming: a method is
	// bound to a host module, a function is authored in Buzz, a symbol comes from SCIP.
	// They never overlap (SCIP does not index .buzz), so a definition lands in exactly one.
	KindMethod     = "method" // a callable bound to a host module (fs.stat) - magus's built-in API surface
	KindDiagnostic = "diagnostic"
	KindDoc        = "doc"       // markdown doc page (phase 4)
	KindFile       = "file"      // a .buzz source file (phase 4)
	KindFunction   = "function"  // a callable defined in a .buzz source file (Buzz-authored)
	KindImport     = "import"    // an unresolvable buzz import literal (phase 4)
	KindRationale  = "rationale" // a NOTE/WHY/HACK/TODO comment (phase 4)
	KindOwner      = "owner"     // a CODEOWNERS owner (@user, @org/team, email)
	KindSymbol     = "symbol"    // a definition ingested from a SCIP index (compiled-language source, e.g. Go)
	KindAuthor     = "author"    // a git contributor; `authored` the files they touched (emergent, vs the declared owner)
)

// Knowledge edge relations. Values are stable wire strings.
const (
	RelationDependsOn    = "depends_on"    // project->project, target->target
	RelationContains     = "contains"      // project->target, spell->op, project->file/doc
	RelationUses         = "uses"          // target->op
	RelationReferences   = "references"    // charm->target/project; reused for file->symbol (SCIP)
	RelationDocuments    = "documents"     // doc->spell/diagnostic/module (phase 4)
	RelationCalls        = "calls"         // function->function (phase 4)
	RelationImports      = "imports"       // file->file / file->import (phase 4)
	RelationRationaleFor = "rationale_for" // rationale->function (phase 4)
	RelationEmits        = "emits"         // target->diagnostic, runtime (phase 8)
	RelationOwns         = "owns"          // owner->project/file, from CODEOWNERS
	RelationDefines      = "defines"       // file->symbol, from a SCIP index
	RelationProduces     = "produces"      // target->file/doc, from magus.outputs (v5)
	RelationConsumes     = "consumes"      // target->file/doc, from magus.inputs (v5)
	RelationAuthored     = "authored"      // author->file, from git history (v6)
)

// Edge confidence. Extracted edges are read directly off a parsed source (score
// 1.0); inferred edges come from a documented rubric (fuzzy doc mentions, etc.)
// and carry a sub-1.0 score. Phase 1 emits only extracted edges.
const (
	ConfidenceExtracted = "extracted"
	ConfidenceInferred  = "inferred"
)

// KnowledgeTiming is one target's observed run cost, gathered from the local
// timing history and folded onto the target node in the isolated @runtime shard
// (observed, non-deterministic, never remote-shared). It is an assembly input, not
// a wire type: Project and Target name the node, the rest annotate it. Samples is
// the duration-percentile sample count; HitRateSamples is the hit-rate denominator
// (hits + misses), so a consumer can tell a cold rate from a settled one.
type KnowledgeTiming struct {
	Project        string
	Target         string
	P75Ms          int64
	Samples        int
	HitRate        float64
	HitRateSamples int
}

// KnowledgeOutputRef is one target's most recent captured-output reference, gathered
// from the local output store (see internal/cache OutputStore) and folded onto the target
// node as observed attrs in the @runtime shard (non-deterministic, never remote-shared).
// Like KnowledgeTiming it is an assembly input, not a wire type: Project and Target name
// the node, Ref is the output reference id (the "ref1a2b3c" token) minted for that run,
// and OK is whether that run succeeded. It lets an agent go query -> target node -> the
// last captured output in two hops. The forecast history the timing attrs ride does not
// (and by its cache-safety lock must not) record refs, so the output store is the source.
type KnowledgeOutputRef struct {
	Project string
	Target  string
	Ref     string
	OK      bool
}

// KnowledgeVCS is one file's git history metadata (an assembly input, not a wire type),
// folded onto the file node as attrs in the @vcs shard. It is EXTRACTED from git, not
// inferred, and deterministic per commit: the same HEAD yields the same values, so the
// shard is remote-shareable (unlike @runtime). Path is workspace-relative and matches a
// file node's Source. Commits is the number of commits touching the file within the
// scanned window; LastCommit/LastUnix/LastAuthor are the most recent such commit's short
// SHA, author time, and author name - the last is the EMERGENT maintainer, comparable
// against a file's DECLARED CODEOWNERS owner.
type KnowledgeVCS struct {
	Path       string
	LastCommit string
	LastUnix   int64
	LastAuthor string
	// Authors is the distinct set of authors who touched the file within the scanned
	// window (sorted), the source for the `author --authored--> file` edges. LastAuthor
	// is one of them (the most recent).
	Authors []string
	Commits int
}

// KnowledgeSymbol is one code symbol ingested from a SCIP index (an assembly input,
// not a wire type). magus never parses source; a per-language indexer emits the
// index file and this is the language-agnostic shape the reader distills it to. Key
// is the version-stripped, stable moniker key (it becomes the node ID via symbolID);
// Moniker is the original. SymbolKind is the SCIP classifier (function/type/...),
// distinct from the node's own kind (always "symbol"). Defs are the files that define
// the symbol (usually one); Refs are the files that use it, one entry per file (never
// per occurrence, the scale decision) with a count and a capped line list.
type KnowledgeSymbol struct {
	Key        string
	Moniker    string
	Label      string
	Language   string
	SymbolKind string
	// Source is "<path>:<line>" of the definition, or empty when only references
	// were seen (the definition lives in another index).
	Source string
	Defs   []string
	Refs   []KnowledgeSymbolRef
}

// SymbolIndexFreshness is the state of a project's cached SCIP index relative to its
// current sources, reported by `magus status` and the dashboard.
type SymbolIndexFreshness string

const (
	SymbolIndexFresh    SymbolIndexFreshness = "up-to-date"  // index reflects current sources
	SymbolIndexStale    SymbolIndexFreshness = "out-of-date" // sources changed since the index was built
	SymbolIndexNotBuilt SymbolIndexFreshness = "not-indexed" // no index has been produced yet
)

// SymbolIndexStatus is one symbol-capable project's index freshness, for status output.
// Project carries both the machine path and the human name so the workspace-root project
// renders as its repo name, not the bare ".".
type SymbolIndexStatus struct {
	Project   ProjectRef           `json:"project"`
	Language  string               `json:"language,omitempty"`
	Freshness SymbolIndexFreshness `json:"freshness"`
}

// KnowledgeSymbolRef is one referencing file: its path, how many times the symbol
// appears, and a capped list of the first occurrence lines (bounded so a hot symbol
// cannot blow up the edge's provenance).
type KnowledgeSymbolRef struct {
	Path  string
	Count int
	Lines []int
}

// KnowledgeNode is one vertex: a magus-domain entity with stable identity and
// provenance. ID is "<kind>:<qualified-name>" (e.g. "target:pkg/foo:build"),
// stable across builds so external consumers and agent memory can key on it.
type KnowledgeNode struct {
	ID     string            `json:"id"               yaml:"id"`
	Kind   string            `json:"kind"             yaml:"kind"`
	Label  string            `json:"label"            yaml:"label"`
	Doc    string            `json:"doc,omitempty"    yaml:"doc,omitempty"`
	Source string            `json:"source,omitempty" yaml:"source,omitempty"` // path or path:line provenance
	Attrs  map[string]string `json:"attrs,omitempty"  yaml:"attrs,omitempty"`  // kind-specific (charm pointer, MGS URL, ...)
}

// KnowledgeEdge is one directed relation with provenance. Source and Target are
// node IDs. The JSON keys (source/target) match the node-link convention that
// external graph tools consume, so an exported graph opens in Gephi/yEd/etc.
type KnowledgeEdge struct {
	Source     string  `json:"source"               yaml:"source"`
	Target     string  `json:"target"               yaml:"target"`
	Relation   string  `json:"relation"             yaml:"relation"`
	Confidence string  `json:"confidence"           yaml:"confidence"`
	Score      float64 `json:"score"                yaml:"score"`
	Provenance string  `json:"provenance,omitempty" yaml:"provenance,omitempty"`
}

// Retrieval-subcommand definitions (query/explain/path). These complement describe
// ("what is declared"): explain answers "how is it connected".
const (
	KnowledgeQueryDefinition = "query resolves search terms to knowledge-graph " +
		"nodes and returns the ranked matches plus the surrounding neighborhood " +
		"(the induced subgraph, collected up to a node budget)."
	KnowledgeExplainDefinition = "explain shows one node's context: its data, its " +
		"incoming and outgoing edges with provenance, and how many nodes reach it."
	KnowledgePathDefinition = "path connects two nodes: the shortest chain of edges " +
		"between them (edges walked in either direction), with each hop's relation."
)

// KnowledgeMatch is one ranked node from a query.
type KnowledgeMatch struct {
	ID    string `json:"id"    yaml:"id"`
	Kind  string `json:"kind"  yaml:"kind"`
	Label string `json:"label" yaml:"label"`
	Score int    `json:"score" yaml:"score"`
}

// KnowledgeGraphDiffDefinition is the human-readable description of `magus graph diff`.
const KnowledgeGraphDiffDefinition = "graph diff reports how the knowledge graph " +
	"changed between a base revision and the working tree: the nodes and edges added, " +
	"removed, or (for nodes) changed. It is the PR-review blast-radius artifact - what " +
	"a change did to the domain's shape - emitted as json or markdown, never rendered. " +
	"Edge diffs are structural: an edge is identified by (source, target, relation), so " +
	"a re-scored or re-provenanced edge that keeps those three is not reported as changed."

// KnowledgeGraphDiff is the result of `magus graph diff`: the node/edge deltas between
// a base graph and the current one. Slices are sorted (by node ID, then edge key) so
// the diff is deterministic and reviewable.
type KnowledgeGraphDiff struct {
	Definition    string                `json:"definition"     yaml:"definition"`
	SchemaVersion int                   `json:"schema_version" yaml:"schema_version"`
	Base          string                `json:"base"           yaml:"base"` // the base revision or baseline label
	NodesAdded    []KnowledgeNode       `json:"nodes_added,omitempty"    yaml:"nodes_added,omitempty"`
	NodesRemoved  []KnowledgeNode       `json:"nodes_removed,omitempty"  yaml:"nodes_removed,omitempty"`
	NodesChanged  []KnowledgeNodeChange `json:"nodes_changed,omitempty"  yaml:"nodes_changed,omitempty"`
	EdgesAdded    []KnowledgeEdge       `json:"edges_added,omitempty"    yaml:"edges_added,omitempty"`
	EdgesRemoved  []KnowledgeEdge       `json:"edges_removed,omitempty"  yaml:"edges_removed,omitempty"`
}

// KnowledgeNodeChange is one node present in both graphs whose data differs: the
// before/after nodes plus the names of the fields that changed.
type KnowledgeNodeChange struct {
	ID     string        `json:"id"     yaml:"id"`
	Fields []string      `json:"fields" yaml:"fields"` // kind|label|doc|source|attrs
	Before KnowledgeNode `json:"before" yaml:"before"`
	After  KnowledgeNode `json:"after"  yaml:"after"`
}

// KnowledgeRefsDefinition is the human-readable description of `magus refs`.
const KnowledgeRefsDefinition = "refs lists where an ingested code symbol is " +
	"defined and every file that references it, as file:line rows drawn from the " +
	"SCIP index. It is the occurrence-shaped view (a flat list) that a symbol's fan-in " +
	"needs, which query's node-link neighborhood renders poorly."

// KnowledgeRefsOutput is the result of `magus refs <symbol>`: the resolved symbol,
// its definition site(s), and every referencing file with the per-file occurrence
// count and (capped) line list. Occurrence-shaped, not node-link.
type KnowledgeRefsOutput struct {
	Definition    string             `json:"definition"     yaml:"definition"`
	SchemaVersion int                `json:"schema_version" yaml:"schema_version"`
	Symbol        string             `json:"symbol"         yaml:"symbol"`
	Label         string             `json:"label"          yaml:"label"`
	FileCount     int                `json:"file_count"     yaml:"file_count"`
	RefCount      int                `json:"ref_count"      yaml:"ref_count"`
	Defs          []KnowledgeRefSite `json:"defs,omitempty" yaml:"defs,omitempty"`
	Refs          []KnowledgeRefSite `json:"refs,omitempty" yaml:"refs,omitempty"`
}

// KnowledgeRefSite is one file that defines or references a symbol, with the
// occurrence count and the (capped) lines where it appears.
type KnowledgeRefSite struct {
	File  string `json:"file"            yaml:"file"`
	Count int    `json:"count,omitempty" yaml:"count,omitempty"`
	Lines []int  `json:"lines,omitempty" yaml:"lines,omitempty"`
}

// KnowledgeQueryOutput is the result of `magus query`: the ranked seed matches
// plus the induced subgraph (neighborhood) collected up to the node budget. The
// Nodes/Links carry the node-link keys so the subgraph is itself a valid export.
// MatchCount is the TOTAL matches; when a page is requested (Offset > 0 or a
// smaller Matches slice than MatchCount) the caller pages via Offset + len(Matches).
type KnowledgeQueryOutput struct {
	Definition    string `json:"definition"     yaml:"definition"`
	SchemaVersion int    `json:"schema_version" yaml:"schema_version"`
	Query         string `json:"query"          yaml:"query"`
	Budget        int    `json:"budget"         yaml:"budget"`
	MatchCount    int    `json:"match_count"    yaml:"match_count"`
	// Offset is the index of the first returned match within the full ranked list;
	// 0 (omitted) for an unpaged query or the first page. Offset alone does not
	// signal paging - page 0 of a paged query and an unpaged query look the same
	// here; the MCP layer's next_cursor is what signals more pages remain.
	Offset  int              `json:"offset,omitempty" yaml:"offset,omitempty"`
	Matches []KnowledgeMatch `json:"matches"        yaml:"matches"`
	Nodes   []KnowledgeNode  `json:"nodes"          yaml:"nodes"`
	Links   []KnowledgeEdge  `json:"links"          yaml:"links"`
}

// KnowledgeEdgeRef is one edge seen from a focus node: the relation, the node on
// the other end (with kind + label for readability), the direction relative to
// the focus, and the edge's provenance.
type KnowledgeEdgeRef struct {
	Relation   string `json:"relation"             yaml:"relation"`
	Direction  string `json:"direction"            yaml:"direction"` // "out" (focus is source) | "in" (focus is target)
	Other      string `json:"other"                yaml:"other"`
	OtherKind  string `json:"other_kind"           yaml:"other_kind"`
	OtherLabel string `json:"other_label"          yaml:"other_label"`
	Provenance string `json:"provenance,omitempty" yaml:"provenance,omitempty"`
}

// KnowledgeExplainOutput is a single node's context card: its data, grouped
// out/in edges with provenance, and a blast-radius count (how many nodes can
// transitively reach it).
type KnowledgeExplainOutput struct {
	Definition    string             `json:"definition"     yaml:"definition"`
	SchemaVersion int                `json:"schema_version" yaml:"schema_version"`
	Node          KnowledgeNode      `json:"node"           yaml:"node"`
	BlastRadius   int                `json:"blast_radius"   yaml:"blast_radius"`
	Out           []KnowledgeEdgeRef `json:"out,omitempty"  yaml:"out,omitempty"`
	In            []KnowledgeEdgeRef `json:"in,omitempty"   yaml:"in,omitempty"`
}

// KnowledgePathStep is one hop along a path, oriented as walked (From -> To).
// Forward reports whether the underlying edge's own direction is From -> To
// (false means the path traversed the edge against its direction).
type KnowledgePathStep struct {
	From     string `json:"from"     yaml:"from"`
	To       string `json:"to"       yaml:"to"`
	Relation string `json:"relation" yaml:"relation"`
	Forward  bool   `json:"forward"  yaml:"forward"`
}

// KnowledgePathOutput is the result of `magus path a b`: the resolved endpoints
// and the shortest connecting path (edges treated as bidirectional), if any.
type KnowledgePathOutput struct {
	Definition    string              `json:"definition"     yaml:"definition"`
	SchemaVersion int                 `json:"schema_version" yaml:"schema_version"`
	From          string              `json:"from"           yaml:"from"`
	To            string              `json:"to"             yaml:"to"`
	Found         bool                `json:"found"          yaml:"found"`
	Steps         []KnowledgePathStep `json:"steps,omitempty" yaml:"steps,omitempty"`
}

// KnowledgeStats is the knowledge-graph analytics behind `magus graph stats`:
// where the workspace concentrates (god nodes), where it neglects (orphans),
// and where docs are missing (coverage). It is the structural analogue of
// insight's git-history lenses (insight report embeds it), derived purely from
// the graph (degree and reachability), so it is deterministic and LLM-free.
type KnowledgeStats struct {
	Definition string                 `json:"definition"          yaml:"definition"`
	NodeCount  int                    `json:"node_count"          yaml:"node_count"`
	EdgeCount  int                    `json:"edge_count"          yaml:"edge_count"`
	Gods       []KnowledgeGodNode     `json:"gods"                yaml:"gods"`
	Orphans    []KnowledgeOrphan      `json:"orphans,omitempty"   yaml:"orphans,omitempty"`
	Coverage   []KnowledgeDocCoverage `json:"coverage,omitempty"  yaml:"coverage,omitempty"`
}

// KnowledgeGodNode is a highly-connected node - where structural risk concentrates.
type KnowledgeGodNode struct {
	ID     string `json:"id"     yaml:"id"`
	Kind   string `json:"kind"   yaml:"kind"`
	Label  string `json:"label"  yaml:"label"`
	Degree int    `json:"degree" yaml:"degree"` // in + out
	In     int    `json:"in"     yaml:"in"`
	Out    int    `json:"out"    yaml:"out"`
}

// KnowledgeOrphan is a node missing the connection its kind implies (a doc that
// documents nothing, a spell no target uses), with a plain-English reason.
type KnowledgeOrphan struct {
	ID     string `json:"id"     yaml:"id"`
	Kind   string `json:"kind"   yaml:"kind"`
	Label  string `json:"label"  yaml:"label"`
	Reason string `json:"reason" yaml:"reason"`
}

// KnowledgeDocCoverage is doc coverage for one documentable kind: how many of its
// nodes have a doc pointing at them.
type KnowledgeDocCoverage struct {
	Kind         string   `json:"kind"                   yaml:"kind"`
	Total        int      `json:"total"                  yaml:"total"`
	Documented   int      `json:"documented"             yaml:"documented"`
	Percent      int      `json:"percent"                yaml:"percent"`
	Undocumented []string `json:"undocumented,omitempty" yaml:"undocumented,omitempty"`
}

// KnowledgeStatsDefinition is the human-readable description of `magus graph stats`.
const KnowledgeStatsDefinition = "Graph stats reads the knowledge graph to show " +
	"where the workspace concentrates and where it is neglected: god nodes (the most " +
	"connected spells, modules, and targets - the structural risk), orphans (docs that " +
	"document nothing, spells nothing uses), and doc coverage (the share of diagnostics, " +
	"spells, and modules that have a doc). It is the structural companion to insight's " +
	"git-history lenses."

// KnowledgeRouting is the compact "query first" summary rendered into MAGUS.md's
// header: per-kind and per-project entry points so a reader's (human or agent)
// next action is a magus query, not a grep. It routes - counts, the field to
// query, and a few high-degree anchor nodes - and never dumps graph data, so it
// stays diff-stable across routine edits.
type KnowledgeRouting struct {
	SchemaVersion int                       `json:"schema_version" yaml:"schema_version"`
	NodeCount     int                       `json:"node_count"     yaml:"node_count"`
	EdgeCount     int                       `json:"edge_count"     yaml:"edge_count"`
	Kinds         []KnowledgeRoutingKind    `json:"kinds"          yaml:"kinds"`
	Projects      []KnowledgeRoutingProject `json:"projects"       yaml:"projects"`
}

// KnowledgeRoutingKind is one row of the domain routing table: a node kind, how
// many exist, and up to a few highest-degree "anchor" nodes to start from.
type KnowledgeRoutingKind struct {
	Kind    string   `json:"kind"              yaml:"kind"`
	Count   int      `json:"count"             yaml:"count"`
	Anchors []string `json:"anchors,omitempty" yaml:"anchors,omitempty"`
}

// KnowledgeRoutingProject is one per-project routing row: its path, target count,
// and a few key (highest-degree) targets.
type KnowledgeRoutingProject struct {
	Path        string   `json:"path"                  yaml:"path"`
	TargetCount int      `json:"target_count"          yaml:"target_count"`
	KeyTargets  []string `json:"key_targets,omitempty" yaml:"key_targets,omitempty"`
}

// KnowledgeGraphOutput is the merged node-link export produced by
// "magus graph export -o json". It is node-link compatible (nodes have an
// "id"; links have "source"/"target"), so external graph UIs read it directly;
// the extra magus fields (definition, schema_version, counts) are additive and
// ignored by strict node-link readers. Directed and non-multigraph by construction.
type KnowledgeGraphOutput struct {
	Definition    string `json:"definition"    yaml:"definition"`
	SchemaVersion int    `json:"schema_version" yaml:"schema_version"`
	Directed      bool   `json:"directed"      yaml:"directed"`
	Multigraph    bool   `json:"multigraph"    yaml:"multigraph"`
	NodeCount     int    `json:"node_count"    yaml:"node_count"`
	EdgeCount     int    `json:"edge_count"    yaml:"edge_count"`
	// SourceBaseURL is the workspace's repo blob base (e.g.
	// "https://github.com/owner/repo/blob/main"), derived from the VCS remote, so a
	// viewer can turn a node's relative `source` path into a link to the RIGHT repo.
	// Empty when there is no remote or the forge is unrecognized. Additive; omitted
	// when empty, so it never bumps the schema version.
	SourceBaseURL string          `json:"source_base,omitempty" yaml:"source_base,omitempty"`
	Nodes         []KnowledgeNode `json:"nodes"         yaml:"nodes"`
	Links         []KnowledgeEdge `json:"links"         yaml:"links"`
}
