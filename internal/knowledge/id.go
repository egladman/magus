package knowledge

import (
	"strconv"
	"strings"

	"github.com/egladman/magus/types"
)

// Node ID scheme: "<kind>:<qualified-name>", stable across builds and human
// readable so external consumers and agent memory can key on it. The project
// path is embedded in target/op-adjacent IDs so an edge crossing projects names
// exactly the shard to load next (the routing key, per the plan). No invented
// vocabulary - kinds and separators only.

func projectID(path string) string { return types.KindProject + ":" + path }

func targetID(projectPath, name string) string {
	return types.KindTarget + ":" + projectPath + ":" + name
}

func spellID(name string) string { return types.KindSpell + ":" + name }

func opID(spell, op string) string { return types.KindOp + ":" + spell + ":" + op }

// commandID keys a command node by its owning target and the spell that contributes
// it: one node per (project, target, spell). The rendered argv is a node attr, never
// part of the ID, so a target's command node keeps a stable identity across argv edits
// (a changed flag re-attributes the same node rather than orphaning the old one).
func commandID(projectPath, target, spell string) string {
	return types.KindCommand + ":" + projectPath + ":" + target + ":" + spell
}

// baseCommandID keys the workspace-scoped grouping node for a tool (argv[0] basename),
// shared by every concrete command that runs it, so `explain command:tool:go` lists all
// go commands. The "tool:" infix keeps it clear of a concrete command's project segment.
func baseCommandID(tool string) string { return types.KindCommand + ":tool:" + tool }

func moduleID(name string) string { return types.KindModule + ":" + name }

func methodID(module, method string) string {
	return types.KindMethod + ":" + module + "." + method
}

func diagnosticID(code string) string { return types.KindDiagnostic + ":" + code }

func charmID(name string) string { return types.KindCharm + ":" + name }

func docID(relPath string) string { return types.KindDoc + ":" + relPath }

func fileID(relPath string) string { return types.KindFile + ":" + relPath }

func functionID(relPath, name string) string {
	return types.KindFunction + ":" + relPath + ":" + name
}

func importID(literal string) string { return types.KindImport + ":" + literal }

func rationaleID(relPath string, line int) string {
	return types.KindRationale + ":" + relPath + ":" + strconv.Itoa(line)
}

func ownerID(name string) string { return types.KindOwner + ":" + name }

func symbolID(key string) string { return types.KindSymbol + ":" + key }

// sanitize normalizes free-form repo text (labels, docs, provenance) before it
// enters the graph, per the plan's ingest-sanitization requirement: strip
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
		s = strings.TrimSpace(s[:limit])
	}
	return s
}

// Sanitization caps. Labels are short identifiers; docs are one-line summaries.
const (
	maxLabelLen = 256
	maxDocLen   = 512
	maxSrcLen   = 512
)

// AttrDiagnostic is the node-attribute key under which an extractor records the
// MGS#### code for an ambiguity it found on that node (an unresolvable import,
// a dangling doc reference), so the ambiguity is queryable via `magus explain`
// rather than logged and lost. Silent metadata, not a warning: implicit graph
// rebuilds stay quiet.
const AttrDiagnostic = "diagnostic"

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
	// AttrTitle is a doc page's frontmatter title (its human name, distinct from the
	// relative path that is the node label).
	AttrTitle = "title"
	// AttrTags is a doc page's frontmatter tags, comma-joined.
	AttrTags = "tags"
	// AttrArgv is a command node's rendered argv, space-joined - the concrete command
	// line a target's spell would run. It rides an attr, never the node ID, so a changed
	// flag re-attributes the same node instead of orphaning it.
	AttrArgv = "argv"
	// AttrTool is a command node's tool - argv element 0, the executable the command runs.
	AttrTool = "tool"
)

// Runtime-performance attribute keys. Unlike the static keys above these are
// OBSERVED (from local run history, not workspace sources), so they ride the
// isolated @runtime shard: an agent planning work sees a target's cost without a
// separate history query, and the observed/derived split stays clean. Absent when
// no history backs the target.
const (
	// AttrDurationP75Ms is a target's p75 run duration in milliseconds.
	AttrDurationP75Ms = "duration_p75_ms"
	// AttrCacheHitRate is a target's rolling cache hit rate, formatted "0.NN".
	AttrCacheHitRate = "cache_hit_rate"
	// AttrRunSamples is how many timed runs back the duration percentile - the
	// confidence behind duration_p75_ms.
	AttrRunSamples = "run_samples"
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

// Coverage attribute keys. Like the runtime keys these are OBSERVED - parsed from the
// local Go coverage profile magus produces (`magus run coverage`), not from workspace
// sources - so they ride an isolated, lazily-loaded @coverage shard that folds onto the
// file and symbol nodes SCIP already minted. They answer "which code lacks coverage"
// straight off a node. Absent when no profile covers the file/symbol.
const (
	// AttrCoverage is the covered-statement ratio, formatted "0.NN" (0.00 = fully
	// uncovered, 1.00 = fully covered). The headline "which code lacks coverage" signal.
	AttrCoverage = "coverage"
	// AttrCoveredStmts is how many statements the profile recorded at least one hit for.
	AttrCoveredStmts = "covered_stmts"
	// AttrTotalStmts is the instrumented statement count backing the ratio - the
	// denominator, so a 0/0 file is distinguishable from a small sample.
	AttrTotalStmts = "total_stmts"
)

// AttrTestRefs is a symbol's count of referencing files whose path ends in _test.go -
// the cheap "tested-by" lens derived from the SCIP reference edges already in the
// @symbols shard (no new data source). A zero count is omitted, so its presence means
// "some test references this symbol"; absence means none do (a coverage-independent
// signal, since a symbol can be exercised transitively without a direct test reference).
const AttrTestRefs = "test_refs"
