package knowledge

import (
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/types"
)

// Node ID scheme: "<kind>:<qualified-name>", stable across builds and human
// readable so external consumers and agent memory can key on it. The project
// path is embedded in target/op-adjacent IDs so an edge crossing projects names
// exactly the shard to load next (the routing key). No invented
// vocabulary - kinds and separators only.

func projectID(path string) string { return types.KindProject + ":" + path }

func targetID(projectPath, name string) string {
	return types.KindTarget + ":" + projectPath + ":" + name
}

func spellID(name string) string { return types.KindSpell + ":" + name }

func opID(spell, op string) string { return types.KindOp + ":" + spell + ":" + op }

// toolID keys the workspace-scoped node for a tool - the program an op runs (argv[0]
// basename) - shared by every op and spell that runs it, so `explain tool:go` lists
// every op that runs go. A tool is an ENTITY (the program), distinct from an op (the
// operation that runs it), hence its own kind.
func toolID(tool string) string { return types.KindTool + ":" + tool }

func moduleID(name string) string { return types.KindModule + ":" + name }

func methodID(module, method string) string {
	return types.KindMethod + ":" + module + "." + method
}

func diagnosticID(code string) string { return types.KindDiagnostic + ":" + code }

func charmID(name string) string { return types.KindCharm + ":" + name }

func docID(relPath string) string { return types.KindDoc + ":" + relPath }

// docSectionID names a heading inside a doc as "<rel>#<anchor>", the same shape a link into
// that section takes, so a retrieval result IS a citable pointer to the rendered page.
func docSectionID(relPath, anchor string) string {
	return types.KindDocSection + ":" + relPath + "#" + anchor
}

func fileID(relPath string) string { return types.KindFile + ":" + relPath }

func dirID(relPath string) string { return types.KindDir + ":" + relPath }

func functionID(relPath, name string) string {
	return types.KindFunction + ":" + relPath + ":" + name
}

func importID(literal string) string { return types.KindImport + ":" + literal }

func rationaleID(relPath string, line int) string {
	return types.KindRationale + ":" + relPath + ":" + strconv.Itoa(line)
}

func ownerID(name string) string { return types.KindOwner + ":" + name }

func authorID(name string) string { return types.KindAuthor + ":" + name }

func symbolID(key string) string { return types.KindSymbol + ":" + key }

// noteID mints a note's node ID, namespaced by scope.
//
// The two stores share a name space on disk - nothing stops a private note being called
// the same thing as a shared one - so an unqualified ID would let them collide. Node
// merging is first-writer-wins, and shared is assembled first, so the collision would not
// error: the private note would silently vanish and its edges would be re-attributed to
// the team's note of the same name. The CLI already refuses that ambiguity for a reader;
// assembly must not resolve it the other way in silence.
func noteID(scope, name string) string {
	if scope == ScopePrivate {
		return types.KindNote + ":" + ScopePrivate + "/" + name
	}
	return types.KindNote + ":" + name
}

// AnchorNodeID renders one note anchor as the node ID the graph mints for it, or "" for a
// kind with no node form. scope is the scope of the ANCHORING note, because a note-to-note
// anchor names a note in the SAME store: a private note referring to "auth" means its own,
// not the team's.
//
// One home for a mapping with three callers across two process phases - assembly (which
// turns an anchor into an edge), resolution (which asks whether an anchor still names
// something live), and the console handler. Two hand-kept copies existed and had already
// diverged on exactly the case a reader is least likely to notice: the resolver's copy took
// no scope, so it looked up a private note's note-anchor in the SHARED namespace, reported
// it dangling, and told the author to re-anchor a note that was never broken - while
// assembly had minted the edge correctly all along.
func AnchorNodeID(kind, target, scope string) string {
	switch kind {
	case types.KindSymbol:
		return symbolID(target)
	case types.KindFile:
		return fileID(target)
	case types.KindProject:
		return projectID(target)
	case types.KindTarget:
		return target // already a fully-formed target id
	case types.KindNote:
		return noteID(scope, target)
	default:
		return ""
	}
}

// sanitize normalizes free-form repo text (labels, docs, provenance) before it
// enters the graph: strip
// control characters (which would corrupt MAGUS.md, MCP responses, and agent
// contexts) and cap length to keep node cards and exports bounded. Newlines and
// tabs collapse to spaces; other control runes are dropped.
func sanitize(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t' || r == '\r':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
	s = strings.TrimSpace(s)
	if limit > 0 && len(s) > limit {
		// Back up to a rune boundary so a multibyte rune (a CJK or accented char in a label
		// drawn from repo text) is not split into invalid UTF-8 that ships as mojibake.
		cut := limit
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = strings.TrimSpace(s[:cut])
	}
	return s
}

// Sanitization caps. Labels are short identifiers; docs are one-line summaries.
const (
	maxLabelLen = 256
	maxDocLen   = 512
	maxSrcLen   = 512
)

// attrDiagnostic is the node-attribute key under which an extractor records the
// MGS#### code for an ambiguity it found on that node (an unresolvable import,
// a dangling doc reference), so the ambiguity is queryable via `magus explain`
// rather than logged and lost. Silent metadata, not a warning: implicit graph
// rebuilds stay quiet.
const attrDiagnostic = "diagnostic"

// Static-metadata attribute keys. These surface data the extractors already parse
// (the engine a project runs, its target count, a doc's frontmatter) directly onto
// nodes, so `magus explain` answers "what toolchain / how big / what is this doc"
// without a second describe or a cross-reference. Additive: absent when unknown.
const (
	// AttrEngine is the engine (toolchain runtime) a project runs, mirrored onto
	// each of its targets so a target card names its engine without walking to the
	// project node.
	AttrEngine = "engine"
	// AttrTargetCount is a project's target count - its size at a glance, without
	// counting contains edges.
	AttrTargetCount = "target_count"
	// attrTitle is a doc page's frontmatter title (its human name, distinct from the
	// relative path that is the node label).
	attrTitle = "title"
	// attrTags is a doc page's frontmatter tags, comma-joined.
	attrTags = "tags"
	// attrArgv is an op node's base argv, space-joined - the command line the op runs
	// with an empty charm set. It rides an attr, so a target reaches "what it runs" via
	// target->op without a second describe. Absent for a function-op (no static argv).
	attrArgv = "argv"
	// attrTool is an op node's tool - argv element 0, the executable the op runs.
	attrTool = "tool"
	// attrDeclared marks a spell node a workspace project declares in its `spells:` list
	// (value "true"), distinct from a compiled-in builtin that is merely available. The
	// orphan lens flags only declared-but-unused spells as dead.
	attrDeclared = "declared"
	// attrDeclaredAs is a target's raw, as-written name when the normalizer rewrote it
	// (node "go-build" declared_as "goBuild"). The node ID/label stay the normalized
	// name - the identity edges and lookups key on - so this just conveys the author's
	// spelling on the card, making the normalization visible rather than hidden. Absent
	// when the declared name already equals the normalized one.
	attrDeclaredAs = "declared_as"
	// attrRole classifies a doc node by what the markdown file IS, from a universal
	// filename convention (readme, agent, changelog, contributing, license), or "doc"
	// for anything else. It is workspace-agnostic - no magus-specific filenames - so
	// `query "kind:doc role:agent"` finds the agent-instruction files in any repo.
	attrRole = "role"
	// attrSection is a doc page's top-level section under docs/ (guides, concepts,
	// reference, ...), derived from its path so a page is queryable by where it lives
	// (`query "kind:doc section:guides"`) without hand-tagging every page. Absent for docs
	// outside a docs/ tree and for top-level docs (docs/glossary.md) that name no section.
	attrSection = "section"
	// attrAnchor is a doc-section node's goldmark auto-heading-id: the fragment a link into
	// the section carries (concepts/review.md#manual-on-purpose). attrLevel is the heading
	// depth (1 for #, 2 for ##, ...), so a reader can reconstruct the outline.
	attrAnchor = "anchor"
	attrLevel  = "level"
)

// Runtime-performance attribute keys. Unlike the static keys above these are
// OBSERVED (from local run history, not workspace sources), so they ride the
// isolated @runtime shard: an agent planning work sees a target's cost without a
// separate history query, and the observed/derived split stays clean. Absent when
// no history backs the target.
const (
	// AttrDurationP75Ms is a target's p75 run duration in milliseconds.
	AttrDurationP75Ms = "duration_p75_ms"
	// attrCacheHitRate is a target's rolling cache hit rate, formatted "0.NN".
	attrCacheHitRate = "cache_hit_rate"
	// attrRunSamples is how many timed runs back the duration percentile - the
	// confidence behind duration_p75_ms.
	attrRunSamples = "run_samples"
	// AttrLastOutputRef is the output reference id (the "out1a2b3c" token) of the
	// target's most recent captured execution, so an agent can jump from a target node
	// straight to its last output with `magus query output <ref>` - the query -> target
	// -> output two-hop. Sourced from the output store (the timing history carries no
	// refs); absent when the store holds no execution for the target.
	AttrLastOutputRef = "last_output_ref"
	// AttrLastRunOK is whether that most recent execution succeeded ("true"/"false"), so
	// the ref's outcome is legible from the node without fetching the output.
	AttrLastRunOK = "last_run_ok"
)

// runtimeAttrs enumerates the const block above. It is a second place to edit, kept
// honest by TestRuntimeAttrsCoversAssembled, which derives the set from what
// assembleRuntime emits. The @coverage overlay is also observed and is NOT in here.
var runtimeAttrs = []string{
	AttrDurationP75Ms, attrCacheHitRate, attrRunSamples, AttrLastOutputRef, AttrLastRunOK,
}

// IsRuntimeAttr reports whether an attr key holds observed run history, so a reproducible
// export can drop it. A func, not an exported slice: an importer could write to the slice
// and silently un-strip a key. Mirrors isRuntimeShard.
func IsRuntimeAttr(key string) bool { return slices.Contains(runtimeAttrs, key) }

// historyAttrs enumerates every attr whose value is a function of the COMMIT GRAPH rather
// than of the tree: the per-file git metadata, the directory churn roll-up, and the prose
// staleness derived from both. Kept honest by TestHistoryAttrsCoverWhatGitDerives.
var historyAttrs = []string{
	attrVCSLastCommit, attrVCSLastModified, attrVCSLastAuthor, attrVCSCommits,
	AttrDirCommits, AttrStaleness, AttrOutrunDays,
}

// IsHistoryAttr reports whether an attr key is derived from git history, so a reproducible
// export can drop it.
//
// Distinct from IsRuntimeAttr because the failure is different. An observed attr varies by
// MACHINE; these vary by COMMIT, which is worse for a checked-in artifact: committing
// anything moves the churn, so the file invalidates itself and the drift gate fires on the
// very commit that regenerated it.
func IsHistoryAttr(key string) bool { return slices.Contains(historyAttrs, key) }

// Directory aggregate keys. These roll up from a directory's files (transitively) so a
// dir node reads as a subsystem summary - the granularity agent memory anchors to and
// dir-level coupling/churn queries read against. All are deterministic and OS-agnostic
// (git commit counts, extension-derived languages, slash-relative paths), so the
// @dirs shard is remote-shareable like @registry and @vcs.
const (
	// AttrDirFiles is how many path-bearing files/docs the directory holds transitively.
	AttrDirFiles = "dir_files"
	// AttrDirCommits is the summed git churn (commit counts) across those files - where a
	// subsystem's change activity concentrates.
	AttrDirCommits = "dir_commits"
	// AttrDirLanguages is the sorted, comma-joined set of languages present under the
	// directory, derived from file extensions. Distinct from a file node's single-valued
	// "language" attr - a directory spans languages, so this is a dir-scoped set.
	AttrDirLanguages = "dir_languages"
)

// Coverage attribute keys. Like the runtime keys these are OBSERVED - parsed from the
// local Go coverage profile magus produces (`magus run coverage`), not from workspace
// sources - so they ride an isolated, lazily-loaded @coverage shard that folds onto the
// file and symbol nodes SCIP already minted. They answer "which code lacks coverage"
// straight off a node. Absent when no profile covers the file/symbol.
const (
	// attrCoverage is the covered-statement ratio, formatted "0.NN" (0.00 = fully
	// uncovered, 1.00 = fully covered). The headline "which code lacks coverage" signal.
	attrCoverage = "coverage"
	// AttrCoveredStmts is how many statements the profile recorded at least one hit for.
	AttrCoveredStmts = "covered_stmts"
	// AttrTotalStmts is the instrumented statement count backing the ratio - the
	// denominator, so a 0/0 file is distinguishable from a small sample.
	AttrTotalStmts = "total_stmts"
)

// attrTestRefs is a symbol's count of referencing files whose path ends in _test.go -
// the cheap "tested-by" lens derived from the SCIP reference edges already in the
// @symbols shard (no new data source). A zero count is omitted, so its presence means
// "some test references this symbol"; absence means none do (a coverage-independent
// signal, since a symbol can be exercised transitively without a direct test reference).
//
// Write-only: nothing reads it back yet (no query filter, no console column). It rides
// on the node for when a consumer wants it.
const attrTestRefs = "test_refs"

// attrDefEndLine is the 1-based last line of a symbol's definition body, pairing with the
// node's Source ("<path>:<line>") to bound the exact lines the symbol occupies.
//
// Source alone points a reader at a definition; the pair lets a consumer FINGERPRINT one,
// which is what separates "this symbol still exists" from "this symbol still exists and
// still says what a note about it claims". Omitted when the indexer emits no enclosing
// range, which is the honest answer rather than a guessed extent.
const attrDefEndLine = "def_end_line"

// attrLanguage and attrSymbolKind are the attrs a symbol (and, for language, a file) node
// carries from its index. Named because they are read from four places across two files
// and a mistyped literal would silently match nothing rather than fail.
const (
	attrLanguage   = "language"
	attrSymbolKind = "symbol_kind"
)
