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
const KnowledgeSchemaVersion = 1

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
	KindProject    = "project"
	KindTarget     = "target"
	KindSpell      = "spell"
	KindOp         = "op"
	KindCharm      = "charm"
	KindModule     = "module"
	KindMethod     = "method"
	KindDiagnostic = "diagnostic"
	KindDoc        = "doc"       // markdown doc page (phase 4)
	KindFile       = "file"      // a .buzz source file (phase 4)
	KindFunction   = "function"  // a function in a .buzz file (phase 4)
	KindImport     = "import"    // an unresolvable buzz import literal (phase 4)
	KindRationale  = "rationale" // a NOTE/WHY/HACK/TODO comment (phase 4)
)

// Knowledge edge relations. Values are stable wire strings.
const (
	RelationDependsOn    = "depends_on"    // project->project, target->target
	RelationContains     = "contains"      // project->target, spell->op
	RelationUses         = "uses"          // target->op
	RelationReferences   = "references"    // charm->target/project
	RelationDocuments    = "documents"     // doc->spell/diagnostic/module (phase 4)
	RelationCalls        = "calls"         // function->function (phase 4)
	RelationImports      = "imports"       // file->file / file->import (phase 4)
	RelationRationaleFor = "rationale_for" // rationale->function (phase 4)
	RelationEmits        = "emits"         // target->diagnostic, runtime (phase 8)
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
// the duration-percentile sample count; HitSamples is the hit-rate denominator
// (hits + misses), so a consumer can tell a cold rate from a settled one.
type KnowledgeTiming struct {
	Project    string
	Target     string
	P75Ms      int64
	Samples    int
	HitRate    float64
	HitSamples int
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

// Retrieval-verb definitions (query/explain/path). These complement describe
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
	// 0 (omitted) for an unpaged query or the first page.
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
