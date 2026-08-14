package types

import (
	"strings"
	"time"
)

// Knowledge-graph schema: the deterministic, derived graph of the magus domain
// (projects, targets, spells, ops, charms, modules, methods, diagnostics, and -
// later - docs and buzz source nodes). Every node and edge is EXTRACTED or
// rubric-INFERRED from parseable workspace sources; nothing here is LLM-authored
// or otherwise unverifiable. These are pure domain types (stdlib-only leaf); the
// builder lives in internal/graph/knowledge and the CLI surface in cmd/magus.

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
// v7 changes no node or edge shape at all: it bumps because shard fingerprints are now
// computed by streaming fields into SHA256 instead of hashing marshalled JSON, so every
// shard's fingerprint VALUE differs from a v6 store's. The manifest check treats a
// version mismatch as a full rebuild, which is exactly the migration needed - without
// the bump, a v6 cache would read as current while every fingerprint disagreed, and a
// changed shard would never be rewritten.
// v8 adds symbol->symbol `calls` edges to the @symbols shards, attributed from the SCIP
// occurrence's enclosing_range (the callee is referenced from inside the caller's body).
// The relation and both node kinds already existed, so a v7 consumer parses a v8 graph
// without changing - but it would read a symbol's edge set as complete when it is not,
// and the shard fingerprints all differ, so the bump is what forces the rebuild.
// v9 adds `secret_refs` to a target node: the credential references the target names,
// alongside the `reads_secrets` flag that already recorded that it names any. The field
// is additive, so a v8 consumer parses a v9 graph unchanged - the bump is for the OTHER
// direction, a v8 store on disk. Its target shards were extracted before the field
// existed, and the magusfile they were extracted from has not changed, so nothing else
// would invalidate them: the version mismatch is what forces the rebuild that puts the
// references there.
const KnowledgeSchemaVersion = 9

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
	KindDir        = "dir"       // a directory between a project and its files; the containment tree layer
	KindFunction   = "function"  // a callable defined in a .buzz source file (Buzz-authored)
	KindImport     = "import"    // an unresolvable buzz import literal (phase 4)
	KindRationale  = "rationale" // a NOTE/WHY/HACK/TODO comment (phase 4)
	KindOwner      = "owner"     // a CODEOWNERS owner (@user, @org/team, email)
	KindSymbol     = "symbol"    // a definition ingested from a SCIP index (compiled-language source, e.g. Go)
	KindAuthor     = "author"    // a git contributor; `authored` the files they touched (emergent, vs the declared owner)
	// KindNote is the one kind that is INJECTED rather than extracted. Every other kind is a
	// projection of workspace content - a doc from markdown, a rationale from a comment, a
	// symbol from an index, an author from git - so deleting the graph and rebuilding
	// recovers all of them. A note's content originates with a person and no rebuild
	// recovers it, which is also why nothing but a person may write one.
	KindNote = "note" // a human-authored note from the declared notes store

	// KindPackage is a THIRD-PARTY dependency a project declares in its manifest, at
	// the version that manifest resolves to. It is the one node kind whose subject
	// lives outside the workspace, which is the point: a workspace that knows it is on
	// connect v1.20.0 can answer a question about connect against v1.20.0, instead of
	// against whatever the open web happens to rank first.
	//
	// Distinct from KindModule (a magus host module a magusfile calls, e.g. fs) and
	// from KindSymbol (a definition, which for an external package comes from a SCIP
	// index rather than a manifest). A package and the symbols it defines are the same
	// dependency seen from two sides: the manifest says what is INSTALLED, the SCIP
	// monikers say what is actually IMPORTED, and neither answers the other's question.
	KindPackage = "package"
)

// Knowledge edge relations. Values are stable wire strings.
const (
	RelationDependsOn    = "depends_on"    // project->project, target->target, project->package
	RelationContains     = "contains"      // project->target, spell->op, project->file/doc
	RelationUses         = "uses"          // target->op
	RelationReferences   = "references"    // charm->target/project; reused for file->symbol (SCIP)
	RelationDocuments    = "documents"     // doc->spell/diagnostic/module (phase 4)
	RelationCalls        = "calls"         // function->function (buzz); symbol->symbol, from a SCIP index (v8)
	RelationImports      = "imports"       // file->file / file->import (phase 4)
	RelationRationaleFor = "rationale_for" // rationale->function (phase 4)
	RelationEmits        = "emits"         // target->diagnostic, runtime (phase 8)
	RelationOwns         = "owns"          // owner->project/file, from CODEOWNERS
	RelationDefines      = "defines"       // file->symbol, from a SCIP index
	RelationProduces     = "produces"      // target->file/doc, from magus.outputs (v5)
	RelationConsumes     = "consumes"      // target->file/doc, from magus.inputs (v5)
	RelationAuthored     = "authored"      // author->file, from git history (v6)
	// RelationAnnotates completes the trio for the three ways knowledge attaches to code, kept
	// distinct because their provenance differs: documents is doc->entity, rationale_for is
	// an in-code marker->function, and annotates is a human note->entity. A note attaches to
	// an ENTITY and never to a position inside one, which is what lets its breakage be
	// reported rather than silently drifting.
	RelationAnnotates = "annotates" // note->symbol/file/project/target/note, from the notes store
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

// KnowledgeVCS is one file's git history metadata (an assembly input, not part of the
// exported graph), folded onto the file node as attrs in the @vcs shard. It is EXTRACTED
// from git, not inferred, and deterministic per commit: the same HEAD yields the same
// values, so the shard is remote-shareable (unlike @runtime). Path is workspace-relative
// and matches a file node's Source. Commits is the number of commits touching the file
// within the scanned window; LastCommit/LastModified/LastAuthor are the most recent such
// commit's short SHA, author time, and author name - the last is the EMERGENT maintainer,
// comparable against a file's DECLARED CODEOWNERS owner.
//
// The json tags are this type's CACHE format: the scan is expensive enough to persist
// between builds, so the names on disk are pinned here rather than left to follow Go
// identifiers.
type KnowledgeVCS struct {
	Path         string    `json:"path"`
	LastCommit   string    `json:"last_commit"`
	LastModified time.Time `json:"last_modified"`
	LastAuthor   string    `json:"last_author"`
	// Authors is the distinct set of authors who touched the file within the scanned
	// window (sorted), the source for the `author --authored--> file` edges. LastAuthor
	// is one of them (the most recent).
	Authors []string `json:"authors"`
	Commits int      `json:"commits"`
}

// KnowledgeNote is one human-authored note from the declared notes store (an assembly
// input, not a wire type). Path is workspace-relative and is what the @vcs shard joins on
// to attribute the note to whoever wrote it - the reason notes live in the checkout at all.
//
// Anchors are the entities the note attaches to, already resolved to node IDs by the
// caller: assembly emits an edge only for an anchor that resolves, because an edge to a
// node that does not exist is dangling in the graph and `magus notes verify` is the right
// place to report that instead.
type KnowledgeNote struct {
	Name    string
	Title   string
	Path    string
	Tags    []string
	Anchors []string
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
	// DefEndLine is the 1-based last line of the definition's BODY, from the SCIP
	// occurrence's enclosing range, or 0 when the indexer emits none (which is the honest
	// answer rather than a guess - see Calls, which makes the same trade).
	//
	// Source alone gives a start with no end, which is enough to point a reader at a
	// definition but not enough to fingerprint one. Pairing them bounds the exact lines a
	// symbol occupies, so a consumer can tell "this symbol still exists" from "this symbol
	// still exists and says the same thing".
	DefEndLine int
	Defs       []string
	Refs       []KnowledgeSymbolRef
	// Calls are the workspace-defined symbols referenced from inside this symbol's own
	// definition body, attributed by the SCIP occurrence's enclosing range. Collapsed per
	// (caller, callee) - the same scale decision Refs makes per (file, symbol) - so a hot
	// callee yields one entry per caller, never one per call site. Empty when the indexer
	// emits no enclosing ranges, which is the honest answer rather than a guess.
	Calls []KnowledgeSymbolCall
}

// KnowledgePackage is one third-party dependency read out of a project's manifest:
// which package manager governs it, its name in that manager's namespace, and the
// version the manifest resolves to.
//
// Manager is not decoration and not derivable from Name. It is what keeps the npm
// package `foo` and the Go module `foo` from colliding on one node - the same
// collision internal/symbols/scip.go's parseMoniker already folds the manager into
// its key to avoid, and for the same reason.
//
// Version is what the manifest RESOLVES to, never a range. Go states exact versions
// in go.mod, so the manifest is the resolved list; ecosystems whose manifest holds a
// range (npm's ^4.2.0) must read their lockfile instead, which is what
// spells.Manifest.LockCandidates leads to. A record here always carries a pin - an
// unresolvable dependency is omitted rather than recorded with a range, because a
// range presented as a version is exactly the version skew this exists to end.
type KnowledgePackage struct {
	Manager string
	Name    string
	Version string
	// Indirect marks a transitive dependency (go.mod's `// indirect`). Kept because
	// "do we depend on this directly" changes the answer to whether a version is ours
	// to bump, and because a direct dependency is the one worth reading docs about.
	Indirect bool
	// Replaced marks a dependency a replace directive redirects. Its Version is the
	// replacement's, which is what actually builds - so the node stays truthful about
	// what is on disk - and this flag is what stops a reader concluding the manifest's
	// original requirement is what shipped.
	Replaced bool
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

// KnowledgeSymbolCall is one callee reached from inside a symbol's definition body: the
// callee's version-stripped key (the same key that becomes its node ID) and how many
// occurrences were attributed. It carries no line list on purpose - the call sites are
// already recorded on the caller file's `references` edge, and duplicating them per pair
// would be pure shard weight at this edge count.
type KnowledgeSymbolCall struct {
	Key   string
	Count int
}

// KnowledgeVerdict classifies an answer that came back empty. The distinction is the
// point: `absent` is a fact magus verified, `unknown` is magus saying it could not see
// far enough to know. Without it a caller reads every empty result as proof of absence,
// which is the most expensive way for a lookup to be wrong.
//
// Naming rule for the whole family, since magus reaches verdicts in several domains: a
// VERDICT is the scalar judgment, and the thing carrying it is named for the question it
// answers. So KnowledgeAnswer holds a Verdict, spells.VersionBounds.Check returns a
// spells.Verdict, and a record of a judgment is a Result or a Plan rather than a Verdict.
// Two packages both spelling the scalar `Verdict` is not a collision - Go qualifies it,
// and both genuinely are verdicts. Prefixing this one to KnowledgeVerdictUnknown would
// only add stutter and break the tie to what the CLI prints and the JSON key says, which
// is the one-vocabulary rule the README states.
type KnowledgeVerdict string

const (
	VerdictFound   KnowledgeVerdict = "found"   // the lookup returned something
	VerdictAbsent  KnowledgeVerdict = "absent"  // nothing matched, and everything that could match was searched
	VerdictUnknown KnowledgeVerdict = "unknown" // nothing matched, but part of the workspace was not searchable
)

// KnowledgeUnknownReason says WHY an answer is unknown, because the causes have
// different fixes: one needs an index built, one needs a different query, and one means
// magus could not even establish what it had searched.
type KnowledgeUnknownReason string

const (
	// ReasonSymbolIndexMissing: a project declares a SCIP index magus could not read.
	// Fix: build it.
	ReasonSymbolIndexMissing KnowledgeUnknownReason = "symbol-index-missing"
	// ReasonSymbolsNotLoaded: this lookup never merged the symbol shards, so no code
	// symbol could have matched whatever the index holds. Fix: ask a question that seeds
	// symbols, or use the verb that always does.
	ReasonSymbolsNotLoaded KnowledgeUnknownReason = "symbols-not-loaded"
	// ReasonCoverageUnknown: the coverage probe itself failed, so magus cannot say what it
	// searched. Reporting this as `absent` would assert exactly the fact it failed to
	// establish, which is the one outcome this whole verdict exists to prevent.
	ReasonCoverageUnknown KnowledgeUnknownReason = "coverage-unknown"
)

// KnowledgeSymbolGap is one project whose declared symbol index magus could not read.
// State reuses SymbolIndexFreshness so reporting staleness later is additive rather than
// a second enum; today only SymbolIndexNotBuilt is emitted, because the read verbs
// deliberately probe with a stat rather than opening the workspace's cache.
type KnowledgeSymbolGap struct {
	Project ProjectRef           `json:"project"          yaml:"project"`
	State   SymbolIndexFreshness `json:"state"            yaml:"state"`
	Detail  string               `json:"detail,omitempty" yaml:"detail,omitempty"`
}

// Describe renders one gap as "libs/api (not-indexed)". It lives here so the CLI, the
// explain text, and the insight report cannot drift into three spellings of one fact.
func (g KnowledgeSymbolGap) Describe() string {
	detail := g.Detail
	if detail == "" {
		detail = string(g.State)
	}
	return g.Project.Display() + " (" + detail + ")"
}

// DescribeGaps renders a gap list as "libs/api (not-indexed), docs (not-indexed)".
func DescribeGaps(gaps []KnowledgeSymbolGap) string {
	parts := make([]string, len(gaps))
	for i, g := range gaps {
		parts[i] = g.Describe()
	}
	return strings.Join(parts, ", ")
}

// KnowledgeAnswer rides every graph lookup's structured output so a consumer can branch
// on the verdict instead of inferring it from an empty list. Verdict has no omitempty:
// `absent` must be positively asserted, or a reader cannot tell a verified absence from
// an older magus that had no verdict at all.
type KnowledgeAnswer struct {
	Verdict KnowledgeVerdict       `json:"verdict"             yaml:"verdict"`
	Reason  KnowledgeUnknownReason `json:"reason,omitempty"    yaml:"reason,omitempty"`
	Gaps    []KnowledgeSymbolGap   `json:"gaps,omitempty"      yaml:"gaps,omitempty"`
}

// Answer classifies a lookup's result against what magus was actually able to search.
//
// matched reports whether the lookup returned anything. reason is empty when the symbol
// layer was searched (or was irrelevant to the question); set it when the lookup could not
// consult it, or when the coverage probe itself failed. gaps are the projects whose
// declared index could not be read.
//
// A stated reason or any gap makes the answer unknown WHETHER OR NOT the lookup matched.
// That is deliberate and it is the difference from a plain emptiness check: a populated
// list drawn from a half-indexed workspace is as misleading as an empty one, because the
// projects it omits are invisible either way.
func Answer(matched bool, reason KnowledgeUnknownReason, gaps []KnowledgeSymbolGap) KnowledgeAnswer {
	switch {
	case reason != "":
		return KnowledgeAnswer{Verdict: VerdictUnknown, Reason: reason, Gaps: gaps}
	case len(gaps) > 0:
		return KnowledgeAnswer{Verdict: VerdictUnknown, Reason: ReasonSymbolIndexMissing, Gaps: gaps}
	case matched:
		return KnowledgeAnswer{Verdict: VerdictFound}
	default:
		return KnowledgeAnswer{Verdict: VerdictAbsent}
	}
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
	// Staleness and OutrunDays travel with a prose match that ranked DOWN because the
	// thing it describes moved on without it. They are the evidence for the weight, and
	// they exist so the weight is never silent: a reader can see "ranked down: 400 days
	// behind its subject" instead of wondering why a doc sank. Empty on anything that was
	// not penalized, including prose with no history to measure.
	Staleness  string `json:"staleness,omitempty"   yaml:"staleness,omitempty"`
	OutrunDays int    `json:"outrun_days,omitempty" yaml:"outrun_days,omitempty"`
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
	// Answer says whether an empty Refs list is a verified absence or a blind spot. A
	// named field rather than an embedded struct: the template renderer skips anonymous
	// fields, so `-o template` with no body would not list it, and yaml would nest what
	// json flattens.
	Answer KnowledgeAnswer `json:"answer" yaml:"answer"`
}

// KnowledgeRefSite is one file that defines or references a symbol, with the
// occurrence count and the (capped) lines where it appears.
type KnowledgeRefSite struct {
	File  string `json:"file"            yaml:"file"`
	Count int    `json:"count,omitempty" yaml:"count,omitempty"`
	Lines []int  `json:"lines,omitempty" yaml:"lines,omitempty"`
}

// SymbolOccurrenceStatus says whether one occurrence's recorded range still describes
// the file on disk. It exists because a SCIP range is only meaningful against the exact
// bytes that were indexed: an index built before an edit points at text that has since
// moved, and applying a rewrite to it would corrupt the file at a plausible-looking
// offset. Every occurrence carries one, and only SymbolOccurrenceVerified is safe to edit.
type SymbolOccurrenceStatus string

const (
	// SymbolOccurrenceVerified means magus read the range out of the current file and
	// found exactly the symbol's name there. This is the only status a mechanical
	// rewrite may act on.
	SymbolOccurrenceVerified SymbolOccurrenceStatus = "verified"
	// SymbolOccurrenceMismatch means the range resolved but holds something other than
	// the symbol's name - a stale index, or a position encoding that is not the byte
	// offsets magus assumed. Either way the range is not editable, and saying so is the
	// whole point: the alternative is a confident wrong edit.
	SymbolOccurrenceMismatch SymbolOccurrenceStatus = "mismatch"
	// SymbolOccurrenceUnreadable means the file could not be read, or the range falls
	// outside it. Distinct from mismatch because the fix differs: a deleted or moved
	// file needs a re-index, not a closer look at the line.
	SymbolOccurrenceUnreadable SymbolOccurrenceStatus = "unreadable"
)

// SymbolOccurrence is one exact source range where a symbol appears, precise enough to
// drive a mechanical edit. This is deliberately NOT KnowledgeRefSite: that type carries a
// per-file count and a list of lines capped at MaxRefLines, which is right for describing
// fan-in and wrong for rewriting, because a hot symbol's list is silently truncated and a
// line number alone does not say WHICH occurrence on the line to replace. Occurrences are
// complete and column-precise.
//
// Line and Column are 1-based, matching the file:line:col convention magus already prints
// and every editor understands. The end is EXCLUSIVE: the text to replace is the half-open
// span [Column, EndColumn), so a three-character name at column 6 has EndColumn 9.
type SymbolOccurrence struct {
	Line      int `json:"line"       yaml:"line"`
	Column    int `json:"column"     yaml:"column"`
	EndLine   int `json:"end_line"   yaml:"end_line"`
	EndColumn int `json:"end_column" yaml:"end_column"`
	// Definition marks the site that declares the symbol rather than referencing it. A
	// rename treats the two identically, but a caller that only wants call sites (or only
	// the declaration) needs them told apart, and the index already knows.
	Definition bool `json:"definition,omitempty" yaml:"definition,omitempty"`
	// Text is what magus actually read at this range. It is the evidence behind Status,
	// not decoration: on a mismatch it shows what is really there, which is what tells a
	// reader whether the index is stale or the encoding assumption was wrong.
	Text   string                 `json:"text,omitempty" yaml:"text,omitempty"`
	Status SymbolOccurrenceStatus `json:"status"         yaml:"status"`
}

// SymbolOccurrenceFile groups one file's occurrences. Occurrences are sorted by position,
// which is also the order a caller must NOT apply them in - see KnowledgeOccurrencesOutput.
type SymbolOccurrenceFile struct {
	File        string             `json:"file"        yaml:"file"`
	Occurrences []SymbolOccurrence `json:"occurrences" yaml:"occurrences"`
	// Stale means at least one of this file's occurrences did not verify, which proves
	// the file changed after it was indexed. That matters beyond the individual bad site:
	// an index whose view of a file is out of date may also be MISSING occurrences added
	// since, and no per-site check can see a site that is not in the list. So this marks
	// the file as one where a rewrite cannot be assumed complete, even for the sites that
	// did verify. Re-index before trusting it.
	//
	// The converse does not hold, and the gap is worth stating plainly: an edit that
	// disturbed no existing range - appending a new use at the end of the file - leaves
	// every occurrence verifying while still adding a site the index never saw. Nothing
	// magus can compute from the index alone detects that. Completeness rests on the
	// index being current; `magus status` reports which indexes are.
	Stale bool `json:"stale,omitempty" yaml:"stale,omitempty"`
}

// KnowledgeOccurrencesOutput is the result of `magus refs <symbol> --occurrences`: every
// site the symbol appears, with ranges verified against the working tree.
//
// It is an ANSWER, not an action. magus reports where the symbol is and whether each site
// is safe to touch; applying the edit is the caller's job, the same division `magus
// affected` keeps between naming what a change reaches and doing anything about it.
//
// Applying edits within a file requires walking occurrences BACK TO FRONT: replacing a
// name with one of a different length shifts every later column on the same line, so
// front-to-back application corrupts each subsequent site. Files are independent.
type KnowledgeOccurrencesOutput struct {
	Definition    string `json:"definition"     yaml:"definition"`
	SchemaVersion int    `json:"schema_version" yaml:"schema_version"`
	Symbol        string `json:"symbol"         yaml:"symbol"`
	Label         string `json:"label"          yaml:"label"`
	// Name is the identifier a rename would replace: the first of Names. It is derived from
	// the symbol's own descriptor, not from the index's display name - for a package those
	// differ, and the descriptor's last segment is what call sites write.
	Name string `json:"name" yaml:"name"`
	// Names is every spelling an occurrence was allowed to hold, in the order they were
	// tried. A symbol can legitimately be written more than one way - a package's import
	// statement holds its full path while its call sites hold the bare identifier - so a
	// site is checked against this whole set, not against Name alone. It is surfaced so a
	// consumer can reproduce the verdict instead of having to trust it.
	Names           []string `json:"names,omitempty"  yaml:"names,omitempty"`
	FileCount       int      `json:"file_count"       yaml:"file_count"`
	OccurrenceCount int      `json:"occurrence_count" yaml:"occurrence_count"`
	// VerifiedCount is how many of OccurrenceCount are safe to edit. A caller comparing
	// the two learns, in one subtraction, whether a rewrite would be complete - which is
	// the question that decides whether to proceed at all.
	VerifiedCount int `json:"verified_count"   yaml:"verified_count"`
	// StaleFiles is how many of Files carry a stale marker. Non-zero means a rewrite would
	// be acting on an index that no longer matches the tree, and the honest move is to
	// re-index rather than to edit around the bad sites.
	StaleFiles int                    `json:"stale_files,omitempty" yaml:"stale_files,omitempty"`
	Files      []SymbolOccurrenceFile `json:"files,omitempty"       yaml:"files,omitempty"`
	Answer     KnowledgeAnswer        `json:"answer"                yaml:"answer"`
}

// KnowledgeOccurrencesDefinition is the human-readable description of the occurrence view.
const KnowledgeOccurrencesDefinition = "refs --occurrences lists every exact source range " +
	"where a symbol appears, complete and column-precise, with each range verified against " +
	"the file on disk. It is the view a mechanical rewrite needs; the default file:line " +
	"view caps its line list and cannot say which occurrence on a line to replace."

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
	// Answer says whether MatchCount 0 is a verified absence or a blind spot. Most
	// queries never seed the symbol shards, so a bare term matching nothing says nothing
	// about whether a code symbol by that name exists - this is where that is stated.
	Answer KnowledgeAnswer `json:"answer" yaml:"answer"`
}

// EdgeDirection says which end of an edge the focus node sits on. Distinct from
// types.Direction, the iota used for graph RENDERING order: this one is serialized,
// so it is string-backed and its values are what a reader sees.
type EdgeDirection string

const (
	// EdgeOut is the focus node as the edge's source; EdgeIn as its target.
	EdgeOut EdgeDirection = "out"
	EdgeIn  EdgeDirection = "in"
)

// KnowledgeEdgeRef is one edge seen from a focus node: the relation, the node on
// the other end (with kind + label for readability), the direction relative to
// the focus, and the edge's provenance.
type KnowledgeEdgeRef struct {
	Relation   string        `json:"relation"             yaml:"relation"`
	Direction  EdgeDirection `json:"direction"            yaml:"direction"`
	Other      string        `json:"other"                yaml:"other"`
	OtherKind  string        `json:"other_kind"           yaml:"other_kind"`
	OtherLabel string        `json:"other_label"          yaml:"other_label"`
	Provenance string        `json:"provenance,omitempty" yaml:"provenance,omitempty"`
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
	// Connectivity is the data-quality lens: how fragmented the graph is. A high isolated count or many
	// weakly-connected components means the builder has not linked everything it could, which hurts
	// discoverability. IsolatedCount is every node with no edge at all (Orphans lists a capped sample by
	// kind); ComponentCount is the number of weakly-connected components (1 = fully reachable);
	// LargestComponentSize is the biggest component's node count (the "main" graph most nodes should
	// belong to).
	IsolatedCount        int `json:"isolated_count"         yaml:"isolated_count"`
	ComponentCount       int `json:"component_count"        yaml:"component_count"`
	LargestComponentSize int `json:"largest_component_size" yaml:"largest_component_size"`
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
	"connected spells, modules, and targets - the structural risk), connectivity (how " +
	"fragmented the graph is - components and isolated nodes the builder never linked), " +
	"orphans (isolated nodes and spells nothing uses), and doc coverage (the share of " +
	"diagnostics, spells, and modules that have a doc). It is the structural companion to insight's " +
	"git-history lenses."

// KnowledgeRouting is the compact "query first" summary rendered into MAGUS.md's
// header: per-kind and per-project entry points so a reader's (human or agent)
// next action is a magus query, not a grep. It routes - counts, the field to
// query, and a few high-degree anchor nodes - and never dumps graph data, so it
// stays diff-stable across routine edits.
type KnowledgeRouting struct {
	SchemaVersion int `json:"schema_version" yaml:"schema_version"`
	// CatalogFingerprint identifies the binary that rendered this index; see the field of
	// the same name on KnowledgeGraphOutput. Carried here so MAGUS.md can show it without
	// the renderer taking another parameter. Empty when no graph was available.
	CatalogFingerprint string `json:"catalog_fingerprint,omitempty" yaml:"catalog_fingerprint,omitempty"`
	NodeCount          int    `json:"node_count"     yaml:"node_count"`
	// EdgeCount counts the edges this summary ranked on, so it excludes runtime edges and
	// is NOT KnowledgeGraphOutput's or KnowledgeStats' count. Unrendered (writeRouting
	// drops totals), kept so a renderer that adds one cannot reintroduce the dependence.
	EdgeCount int                       `json:"edge_count"     yaml:"edge_count"`
	Kinds     []KnowledgeRoutingKind    `json:"kinds"          yaml:"kinds"`
	Projects  []KnowledgeRoutingProject `json:"projects"       yaml:"projects"`
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
	SourceBaseURL string `json:"source_base,omitempty" yaml:"source_base,omitempty"`
	// CatalogFingerprint identifies the binary that produced this export. Generated output
	// depends on the binary's catalogs as well as the tree, but only the tree shows in a
	// diff, so regenerating with a foreign build reads as ordinary drift (MGS4005).
	// Additive and omitted when empty, so it never bumps the schema version.
	CatalogFingerprint string          `json:"catalog_fingerprint,omitempty" yaml:"catalog_fingerprint,omitempty"`
	Nodes              []KnowledgeNode `json:"nodes"         yaml:"nodes"`
	Links              []KnowledgeEdge `json:"links"         yaml:"links"`
}
