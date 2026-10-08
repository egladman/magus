//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/queue"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/diagnostics"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

//go:generate go run ../cmd/magus-utils bindings -module magus -lang buzz -out ../internal/interp/bindings/gen/magus.go

func init() { Register(Magus) }

// Magus declares the host-declarable subset of the magus module. The remaining
// methods (target, dispatch, deps, pry, register) and the PROVIDER NAMESPACES
// (magus\cache, magus\ci, magus\secret) are VM-infrastructure:
// they manipulate the per-VM target registry and store/invoke VM-side
// function values, so they cannot share a Go Impl across backends and remain
// as hand-written trampolines in bindings/magus.go.
var Magus = Module{
	Name: "magus",
	// The first paragraph stays one short line with no ". " in it: cmd/magus-docs
	// derives the page's frontmatter description from the doc's first sentence, and a
	// second sentence up here would drag a paragraph break into the YAML.
	Doc: "Magus core primitives.\n\n" +
		"Provider namespaces are wired by the runtime rather than declared here, so " +
		"they do not appear in the method list below: `magus\\cache.remote(<spell>)` selects " +
		"a remote cache provider, `magus\\ci.provider(<spell>)` a CI provider, " +
		"`magus\\secret.provider(<spell>)` / `magus\\secret.read(<ref>)` a secret provider and " +
		"the credentials read through it, `magus\\harness.provider(<spell>)` an agent-host harness " +
		"(many hosts; like workspace.provider, unlike cache.remote's one), and `magus\\guard.shell(<rule>)` an additive " +
		"shell-guard rule (strengthen-only), and `magus\\guard.spawn(<fun>)` the one " +
		"function the agent guard calls on every spawn and continuation (see [magus\\guard.spawn](../guard-spawn.md)), and `magus\\guard.command(<fun>)` the one " +
		"function it calls on every agent shell command (see [magus\\guard.command](../guard-command.md)), and `magus\\guard.write(<fun>)` the one " +
		"function it calls on every agent file write. Each provider takes an imported spell handle. " +
		"`magus\\secret.endpoint(<grant>)` serves the case `read` cannot: it returns a loopback " +
		"base URL a CHILD PROCESS is pointed at instead of the real API, so magus attaches the " +
		"credential on the way upstream and the child never holds it. It takes an object with " +
		"ref/host/header/prefix fields, declared in your own magusfile. For your own code, " +
		"`read` is the ordinary choice. See " +
		"[Secrets](../../concepts/secrets.md), [Remote cache](../../concepts/cache/remote.md) " +
		"and [CI integration](../../guides/integrations/ci.md).\n\n" +
		"`import \"magus\"` is how you reach any of this. The namespace is an ordinary host " +
		"module, like `fs` or `vcs`: without the import line `magus` is undefined, and the " +
		"import is what attaches these signatures to your call sites. It resolves in a " +
		"`magus buzz` script as well as in a magusfile, and a " +
		"script run inside a workspace reads that workspace: `describe.project`, `affected`, `projectGraph`, " +
		"`where`, `insight`, the knowledge-graph reads (`query`, `explain`, `path`, `refs`, `stats`, `importGraph`, " +
		"`dir`, `dirs`, `layer`, `neighborhood`) " +
		"and `output` all answer in-process, and so does `magus\\job` (list, put, " +
		"register, exit, wait, clear): the job store an orchestrating agent declares about work it handed " +
		"out (see types.Job). The `magus job` CLI subcommand is a third write door onto " +
		"the same rows: ls and describe read, fork declares a row, exec records a worker's " +
		"landed base, and wait blocks on a dependency. Only the members that DECLARE into " +
		"the workspace being loaded (`magus\\project`, the provider selections above) raise " +
		"[MGS1022](../codes/magusfile/MGS1022.md) in a script: there is nothing for them to " +
		"declare into. Run a script outside any workspace and the reading members raise it too, " +
		"since there is no workspace to read. The nested-command methods (`cmd`, `run`, " +
		"`doctor`, and the `magus\\describe` methods that fork) work there either way and discover " +
		"the workspace themselves.",
	Methods: []Method{
		{
			Name: "cmd",
			Doc:  "Escape hatch: run `magus <sub> <args>` for a subcommand with no dedicated method (status, affected, agent, graph, ...). Its signature is the typed methods' signature with the subcommand pushed in front: magus\\cmd(sub, args, [opts]) beside magus\\run(args, [opts]), same argv, same opts, same ExecResult. The SUBCOMMAND is a typed argument rather than args[0] because it is the part of the invocation magus can reason about - it stays readable in the signature and greppable in the source, while the remaining argv stays free-form. Prefer the dedicated methods (run, doctor, the magus\\describe methods) when one exists - magus\\cmd warns when sub, or a describe noun, names one that has; a describe noun with no method (job), and any noun's text in an explicit -o format, is reached here without a warning. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input, which is how a credential reaches a subcommand (`graph push`) without passing through a process listing or a run log.",
			Args: []Arg{
				{Name: "sub", Type: TypeString},
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusCmd,
		},
		{
			Name: "affected",
			Doc:  "Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Affected"}},
			Raises:  true,
			Impl:    MagusAffected,
		},
		{
			Name:    "project_graph",
			Doc:     "The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "Graph"}},
			Raises:  true,
			Impl:    MagusGraph,
		},
		{
			Name: "where",
			Doc:  "Return the project path containing dir, or null when dir is inside no project. Served in-process from the workspace on the context - no subprocess.",
			Args: []Arg{
				{Name: "dir", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeString}},
			Raises:  true,
			Impl:    MagusWhere,
		},
		{
			Name: "raise",
			Doc:  "Fail with a CODED diagnostic instead of a bare string, so a caller can branch on the code: `catch (e) { if (e.code == \"ACME1001\") ... }`. code is yours to define and namespace - anything but the MGS prefix, which is magus's own. opts.cause is the error being wrapped, usually the value from an inner catch; it is appended to the message the way Go's %w renders one, and the failure it came from stays reachable underneath. opts.url is the page documenting the code, rendered as the `see:` line the CLI prints under its own diagnostics.",
			Args: []Arg{
				{Name: "code", Type: TypeString},
				{Name: "message", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: nil,
			Raises:  true,
			Impl:    MagusRaise,
		},
		{
			Name: "run",
			Doc:  "Run `magus run <args>` recursively in the target's project directory and capture its output. Child invocations share the parent's concurrency budget over the local socket. Returns {stdout, stderr, code, ok}; raises on non-zero exit unless opts.allow_failure is true. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\\exec); opts.quiet captures the output without echoing it to the console; opts.stdin feeds the child's standard input.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExecResult"}},
			Raises:  true,
			Impl:    MagusRun,
		},
		{
			Name: "insight",
			Doc:  "Every VCS-history lens as one typed report: {hotspots, affinity, ownership, trend, volatility, unreferenced}. Annotate the result `> InsightReport` for compile-checked field access - `r.ownership.projects` gives each project's primary author and bus-factor flag, `r.hotspots.files` the churn-by-complexity ranking, `r.volatility` the targets that flapped. Takes the window as `{commits, since}` and renders nothing - presentation is the caller's job. Read straight off the workspace already open on the context - no subprocess, no second workspace load, no JSON round-trip. Works from a magusfile target and from a `magus buzz` script run inside a workspace; raises MGS1022 only when there is no workspace to read.",
			Args: []Arg{
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "InsightReport"}},
			Raises:  true,
			Impl:    MagusInsight,
		},
		{
			Name: "query",
			Doc:  "Search the knowledge graph: {definition, schemaVersion, query, budget, matchCount, offset, matches, nodes, links, answer}. Annotate the result `> QueryResult`. query is free text plus field matchers (kind=spell, project=pkg/foo, relation=uses, kind!=op, id=~build$). A query that seeds code symbols (kind=symbol) reads the symbol shards too; every other query reads the domain graph. opts.budget caps the neighborhood (default 50); opts.limit and opts.offset window the matches while matchCount stays the total. Read answer.verdict before trusting zero matches: `unknown` means part of the workspace had no symbol index. An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "query", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "QueryResult"}},
			Raises:  true,
			Impl:    MagusQuery,
		},
		{
			Name: "explain",
			Doc:  "One knowledge-graph node's context card: {definition, schemaVersion, node, blastRadius, out, in, docsURL, resolution}. Annotate the result `> ExplainResult`. node is a node ID (target:pkg/foo:build), a workspace path (internal/httpx), or a name that resolves to one. A path resolves to its dir or file node exactly before any ranked match; resolution says which happened (id, path or fuzzy), and a caller anchoring by path refuses fuzzy. A path between two nodes is magus\\path. Raises when node resolves to nothing, naming magus\\refs when the name could be a code symbol this graph does not load. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "node", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "ExplainResult"}},
			Raises:  true,
			Impl:    MagusExplain,
		},
		{
			Name: "path",
			Doc:  "The shortest chain of edges between two knowledge-graph nodes: {definition, schemaVersion, from, to, found, steps}. Annotate the result `> PathResult`. Edges are walked in both directions. opts.relations limits the hops to those relations, so found false under it means no path of those relations. A resolved pair with no connection returns found false; an endpoint that resolves to nothing, an unknown option, and an unknown relation raise. The endpoints are `node` and `to` rather than from and to because `from` is a reserved Buzz word. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "node", Type: TypeString},
				{Name: "to", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "PathOptions"},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "PathResult"}},
			Raises:  true,
			Impl:    MagusPath,
		},
		{
			Name: "refs",
			Doc:  "Where a code symbol is defined and every file that references it: {definition, schemaVersion, symbol, label, fileCount, refCount, defs, refs, answer}. Annotate the result `> RefsResult`. symbol is a symbol node ID or a name that resolves to one, drawn from the workspace's declared SCIP indexes. A symbol nothing defines is an answer, not a raise: answer.verdict says whether that is a verified absence or a blind spot. opts.limit and opts.offset window refs while fileCount and refCount stay the totals. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "symbol", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "RefsResult"}},
			Raises:  true,
			Impl:    MagusRefs,
		},
		{
			Name: "stats",
			Doc:  "The knowledge graph's shape: {definition, nodeCount, edgeCount, gods, orphans, coverage, isolatedCount, componentCount, largestComponentSize}. Annotate the result `> KnowledgeStats`. gods are the most connected nodes, where structural risk concentrates; orphans are docs that document nothing and spells no target uses. kind scopes every section to one node kind (spell, target, doc, ...); omit it for the whole graph. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "kind", Type: TypeString, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "KnowledgeStats"}},
			Raises:  true,
			Impl:    MagusStats,
		},
		{
			Name:    "import_graph",
			Doc:     "The workspace's package import graph: {indexed, packages}, packages mapping each package directory (\".\" for the root) to the sorted package directories it imports, read off the workspace's declared SCIP indexes. Annotate the result `> ImportGraph`. Test files, packages outside the workspace, and self-imports are left out. indexed is false when no symbol index was ingested, and packages is then empty because nobody looked, not because nothing imports anything: a caller checking drift refuses on it rather than reporting none. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "ImportGraph"}},
			Raises:  true,
			Impl:    MagusImportGraph,
		},
		{
			Name:    "symbol_index_digest",
			Doc:     "A digest of the symbol index the graph members load: {digest, indexed, projects, gaps}. Annotate the result `> SymbolIndexDigest`. digest is a hex SHA-256 over each loaded project's symbol shard fingerprint in project path order, so it moves exactly when magus\\importGraph could answer differently; a target writes it as a declared output that a reader of the index keys its cache on, instead of skip_cache. The knowledge graph is built first, so the digest names the index files on disk now. indexed is false and digest empty when no index was ingested. gaps are the projects declaring an index magus could not read: a digest with gaps is stable but partial. Read in-process from the workspace on the context; raises MGS1022 outside one, and raises when the gap probe cannot run.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap, Object: "SymbolIndexDigest"}},
			Raises:  true,
			Impl:    MagusSymbolIndexDigest,
		},
		{
			Name:    "precedents",
			Doc:     "The precedents the workspace's merged symbol indexes establish, the rows `Graph.Precedents` mines: {precedents, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each precedent is {family, scope, key, follow, cohort, share, established, cited, departures}: follow of cohort cases share one shape, established when the cohort and share clear the conformance gate, and departures are the cases that do not. indexes is each declared symbol index as `magus status` judges it ({project, op, language, freshness, detail}), judged just before the graph is read: an index not `up-to-date` gave the rows nothing or something old, so a gate on the rows checks indexes first and fails rather than reading silence as agreement. Declared outputs are never counted. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap}},
			Raises:  true,
			Impl:    MagusPrecedents,
		},
		{
			Name:    "symbols",
			Doc:     "Every non-test, non-generated symbol's declaration and doc comment in the workspace's merged symbol indexes, whatever language indexed them, as data: {symbols, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each symbol is {node, source, language, name, kind, owner, doc}: source is the declaration's path and line, since an index records no position inside a doc; kind is function, method, type, interface, struct or value, or empty when the naming index read no shape; owner is the enclosing type of a member; doc is the whole comment with its lines kept. Nothing here judges the text: a magusfile rule reads it and decides. indexes is each declared symbol index as `magus status` judges it, judged just before the graph is read, as magus\\precedents reports it: an index not `up-to-date` gave the symbols nothing or something old, so a gate on them checks indexes first. Test files and declared outputs are never listed. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args:    nil,
			Returns: []Ret{{Type: TypeAnyMap}},
			Raises:  true,
			Impl:    MagusSymbols,
		},
		{
			Name: "dir",
			Doc:  "One workspace directory as the knowledge graph holds it: {path, id, layer, language, imports, importedBy, importsIndexed, calls, calledBy, children, files}. Annotate the result `> Dir`. path is workspace-relative (internal/httpx). imports and importedBy are the package directories it imports and that import it; importsIndexed false means no symbol index read this directory, so empty lists there say nothing. calls and calledBy are DirCall records, one per `magus:calls` marker, with the transport it declares. layer is what magus\\project's \"layers\" declares for it. Raises MGS7005 when the graph holds no dir node for path, naming the nearest one when a typo is likely, so a figure never draws a box for a directory that is not there. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "path", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Dir"}},
			Raises:  true,
			Impl:    MagusDir,
		},
		{
			Name: "dirs",
			Doc:  "Every directory whose workspace path matches glob, as Dir records sorted by path. Annotate the result `> [Dir]`. glob is a doublestar pattern (internal/**). opts is a DirsOptions: layer keeps one declared layer, and a layer nothing declares raises MGS7006 rather than matching nothing; language keeps one package language; depth bounds how many segments below the glob's literal prefix a match may sit (0 is unbounded). An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "glob", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "DirsOptions"},
			},
			Returns: []Ret{{Type: TypeAny, Object: "[Dir]"}},
			Raises:  true,
			Impl:    MagusDirs,
		},
		{
			Name: "layer",
			Doc:  "One layer magus\\project's \"layers\" key declares: {name, declared, dirs}. Annotate the result `> Layer`. declared are the directories and globs declared for it; dirs are the Dir records it covers. Raises MGS7006 on a name no declaration uses, listing the declared ones. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "name", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Layer"}},
			Raises:  true,
			Impl:    MagusLayer,
		},
		{
			Name: "neighborhood",
			Doc:  "The knowledge subgraph around one focus node: {definition, schemaVersion, focus, resolution, options, nodes, links, folds, answer}. Annotate the result `> NeighborhoodResult`. focus is a node ID, a workspace path, or a name; resolution says how it was reached. opts is a NeighborhoodOptions: depth is the most hops (0 means 1); relations are the only relations walked; direction is out, in, or empty for both; collapse folds every source node under each workspace path prefix into that prefix's dir node, longest prefix winning, and folds lists what each absorbed. Read answer.verdict before trusting a thin result: an imports walk with no symbol index is unknown, not absent. An unknown option, relation or direction raises, as does a focus that resolves to nothing. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "focus", Type: TypeString},
				{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "NeighborhoodOptions"},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "NeighborhoodResult"}},
			Raises:  true,
			Impl:    MagusNeighborhood,
		},
		{
			Name: "output",
			Doc:  "One target run's captured output by its ref: {ref, project, target, failed, durationMs, output}. Annotate the result `> OutputRecord`. ref is an output ref (out1a2b3c) or a unique prefix of one. Raises on a value that is not a ref, a prefix that matches several, and a ref this checkout's output store does not hold: output lives in the checkout that ran the target. Read in-process from the workspace on the context; raises MGS1022 outside one.",
			Args: []Arg{
				{Name: "ref", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "OutputRecord"}},
			Raises:  true,
			Impl:    MagusOutput,
		},
		{
			Name: "impact",
			Doc:  "The blast radius of a changeset: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}. Each affected project carries whether it was a seed and, for a seed, the changed files in it. Annotate the result `> Impact`. This is `magus affected --impact`: the report, with no target run. opts.commits caps the commits scanned; opts.since bounds the window (90d, 12w, 6mo, 1y).",
			Args: []Arg{
				{Name: "base", Type: TypeString, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Impact"}},
			Raises:  true,
			Impl:    MagusImpact,
		},
		{
			Name: "diff",
			Doc:  "Read the working tree's uncommitted changes, annotated and ordered by what they can break: for each file the owning project, whether it is a declared `output` (generated - the source edit is the review), how widely its changed symbols are referenced (`reach`), whether another project can see them (`visibility`), observed `coverage`, how often it has been changing (`churn`), and which agent sessions wrote it (`touches`). Files come back in the order magus recommends READING them - generated last whatever its reach, then widest reach first - so a caller renders the list as given rather than sorting it again. Returns a typed Diff envelope; branch on `role` and `visibility` rather than grepping text. opts.rev reviews a committed range written base...head instead of the working tree, which is what a caller running where the tree is clean (a CI checkout) has to pass to see anything at all. opts.patch reviews a unified diff given as text instead, the way `magus diff --patch -` reads one: a pull request's patch, for files that may not match the tree. opts.baseline is a `magus graph export --symbols -o json` of the base: with it every changed symbol carries what the change did to it (`change` is added, removed, signature, or body) and `api` carries the semver bump that proves, a floor and never a ceiling. Each symbol the change adds, renames or re-signs carries `checks`: what the conformance checks found against how the rest of the workspace declares the same kind of thing, with opts.minCohort and opts.minShare as their silence gates (default 5 and 0.8). opts.from reads a review an earlier `magus diff -o json` saved instead of computing it again, so several readers of one change pay for one diff. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.",
			Args: []Arg{
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "Diff"}},
			Raises:  true,
			Impl:    MagusDiff,
		},
		{
			Name: "doctor",
			Doc:  "Validate the workspace and return what every check found: {workspace, checks, summary}, each check {name, status, message, details} with status `ok`, `fail`, or `advice` (advice is worth knowing and never a gate). Annotate the result `> DoctorReport` for compile-checked field access. A caller branches on a check's status rather than grepping console text for the word fail. It does NOT raise when a check fails: doctor exits non-zero precisely when it has something to report, and raising would discard the report. Gate on `summary.fail` instead, which says more than an exit code does. It DOES raise when the underlying `magus doctor` subprocess itself cannot be launched or its output cannot be decoded - an infrastructure failure, not a check result. opts.root sets the global --root workspace; opts.dir runs it in another directory (relative to the target's, like proc\\exec).",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "DoctorReport"}},
			Raises:  true,
			Impl:    MagusDoctor,
		},
		{
			Name: "clean",
			Doc:  "Remove the declared outputs of the selected projects: {removed, tracked, dryRun}. Arguments are `magus clean`'s: project paths, `--cache`, `--dry-run`. With no projects, the cwd project is selected, or the whole workspace from the root. Tracked outputs stay, because they are committed. Annotate the result `> CleanReport`. opts.root and opts.dir as on doctor.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "CleanReport"}},
			Raises:  true,
			Impl:    MagusClean,
		},
		{
			Name: "attention",
			Doc:  "List the OPEN attention requests of this repository's session store: {requests, store}, each request {id, outcome, source, where, lease, message, ...} as `magus session attention -o json` reports them. Read-only by design: a magusfile may refuse to proceed while a request is open, but disposing one is a human act (see the workspace doctrine's Manual-on-purpose table), so no method here closes anything - the person runs `magus session dispose <id> -reason <text>`. Runs a nested magus, so it works from a `magus buzz` script as well as a magusfile; opts.root and opts.dir as on doctor. Raises only when the subprocess cannot run or its output cannot decode.",
			Args: []Arg{
				{Name: "args", Type: TypeStringSlice},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Raises:  true,
			Impl:    MagusAttention,
		},
		{
			Name: "diagnose_drift",
			Doc:  "Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs\\status; does not replace it.",
			Args: []Arg{
				{Name: "outputs", Type: TypeStringSlice},
				{Name: "inputs", Type: TypeStringSlice, Optional: true},
			},
			Returns: []Ret{{Type: TypeAny, Object: "DriftResult"}},
			Raises:  true,
			Impl:    MagusDiagnoseDrift,
		},
		{
			Name: "bust_cache",
			Doc:  "Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.",
			Args: []Arg{
				{Name: "project_path", Type: TypeString, Optional: true},
			},
			Returns: nil,
			Raises:  true,
			Impl:    MagusBustCache,
		},
		{
			Name: "has_charm",
			Doc:  "True when execution charm `name` is active, letting a target body branch on a charm carried in context (e.g. has_charm(\"rw\")).",
			Args: []Arg{
				{Name: "name", Type: TypeString},
			},
			Returns: []Ret{{Type: TypeBool}},
			Impl:    MagusHasCharm,
		},

		// Everything below is Extern: DECLARED here, BOUND by
		// internal/interp/bindings/buzz.go via MapSet onto the magus namespace at run
		// time. They are here so the checker knows they exist: without a declaration a
		// namespace member is unknown, and an unknown member used to type-check as
		// `any` rather than being reported. Keep this set in step with buildMagus;
		// TestMagusExternsMatchBindings holds the two together.
		{
			Name: "project",
			Doc:  "Declare this directory's project: its spell, sources, outputs, and options. A magusfile calls it once at top level. Raises MGS1022 in a `magus buzz` script, which has no workspace to declare into.",
			// `any` rather than a shape: the binding accepts BOTH project(config) and
			// project(path, config), which one Buzz signature cannot express, and the
			// config map's keys are validated by the loader rather than the checker.
			Args: []Arg{{Name: "config", Type: TypeAny}, {Name: "opts", Type: TypeAny, Optional: true}},
			// NOT Raises, though the script-mode binding does fail: a magusfile calls
			// this at TOP LEVEL, where there is no enclosing function to declare !> and
			// nothing to catch with. Declaring it raising makes the one mandatory call in
			// every magusfile unwritable.
			Extern: true,
		},
		{
			Name: "skills",
			Doc: "Every skill this workspace offers an agent, sorted by name then form: each {name, description, source, form, body, current}. " +
				"source is `shipped` for magus's own catalog, which yields a `short` and a `full` entry per skill, or `local` for a hand-authored " +
				"skill found in an installed skills directory (a wired harness's skill paths, where `magus agent install` writes), which yields one entry with form `full`. " +
				"body is exactly what an agent loads: the SKILL.md text with its frontmatter excluded, the generated footer kept. " +
				"current is true when every installed copy of that entry is byte-equal to what this magus would install, false when one differs or none is installed, and always true for a local skill. " +
				"opts.name selects one skill and raises on a name nothing offers, naming the near matches; opts.form (`short`, `full`, or `both`, the default) narrows the shipped entries and leaves local ones alone. " +
				"An unknown option raises. Pair it with magus\\job.put and magus\\cmd(\"describe\", [\"job\", id]) to hand a worker its brief and its skills from magus itself rather than from pasted files. " +
				"Read from the workspace on the context; raises MGS1022 in a script run outside one.",
			// Extern because the catalog lives in internal/agent, which imports std.
			Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
			Returns: []Ret{{Type: TypeAny, Object: "[Skill]"}},
			Raises:  true,
			Extern:  true,
		},
		{
			Name:    "canonical_name",
			Doc:     "The canonical form of a magus entity name - a target, charm, or spell op. `build2` gains a '-' you did not type; `HTTPServer` breaks before its last letter. Returns the NAME, never a spell handle: a handle can only come from a literal import, because the target graph is built by reading imports statically.",
			Args:    []Arg{{Name: "name", Type: TypeString}},
			Returns: []Ret{{Type: TypeString}},
			// NOT Raises. The binding's only failure is a non-string argument, and the
			// declared `str` parameter now rejects that statically, so the raise is
			// unreachable for any call the checker admits. Declaring it would force a
			// try/catch around a pure string transform, including in the `magus buzz -e`
			// one-liners where there is no enclosing function to propagate from.
			Extern: true,
		},
		{
			Name: "fatal",
			Doc:  "Log at error level, then abort the run with exit status 1.",
			Args: []Arg{{Name: "msg", Type: TypeString, Optional: true}},
			// NOT Raises: it aborts by design. Requiring every call to be caught would
			// ask callers to handle the thing they invoked to be unhandleable.
			Extern: true,
		},
		{
			Name:   "pry",
			Doc:    "Drop into an interactive REPL at this point, with the calling scope in hand. A no-op while the magusfile is only being parsed.",
			Extern: true,
		},
	},
	// Each namespace below is reached THROUGH rather than called
	// (`magus\cache.remote(<spell>)`), so it is declared as an object with static
	// extern methods; see std.Namespace for why that, and not a nested module.
	//
	// log/cache/ci/secret/workspace are PROVIDER namespaces: a magusfile selects or
	// reads through them, and none of those calls is Raises. Every one is made at the
	// TOP LEVEL of a magusfile, where there is no enclosing function to declare !> and
	// nothing to catch with, so declaring them raising would make them unwritable,
	// the same reason magus\project is not Raises. job is the one exception: its
	// methods are ordinary calls made from inside a target or script, not top-level
	// declarations, so they ARE Raises, the same reason magus\secret.read is too.
	Namespaces: []Namespace{
		{
			Name: "log",
			Doc:  "Emitting a message without changing control flow: the four levels, plus hint. magus\\fatal and magus\\raise are deliberately NOT here - they END the run rather than report on it, and grouping them by how they look rather than what they do is what made this module hard to read.",
			Methods: []Method{
				{
					Name:   "debug",
					Doc:    "Log at debug level. See magus\\log.info.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "error",
					Doc:    "Log at error level. See magus\\log.info. Logging an error does not abort; magus\\fatal does.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "hint",
					Doc:    "Emit an advisory nudge: non-fatal, deduped, and suppressed when hints are toggled off.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}},
					Extern: true,
				},
				{
					Name:   "info",
					Doc:    "Log at info level. The only way to log from a magusfile; there is no separate log module.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
				{
					Name:   "warn",
					Doc:    "Log at warn level. See magus\\log.info.",
					Args:   []Arg{{Name: "msg", Type: TypeString, Optional: true}, {Name: "fields", Type: TypeStringMap, Optional: true}},
					Extern: true,
				},
			},
		},
		{
			Name: "cache",
			Doc:  "Remote cache provider selection.",
			Methods: []Method{{
				Name:   "remote",
				Doc:    "Select the remote cache provider, given an imported spell handle. Declared at the top level of the root magusfile.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "ci",
			Doc:  "CI provider selection.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the CI provider, given an imported spell handle.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "guard",
			Doc:  "Agent-guard rules for this workspace: additive shell, spawn, command and write rules, and the decision each compiled built-in takes.",
			Methods: []Method{
				{
					Name: "builtins",
					Doc: "Set compiled guard rules by name: each value is \"off\", \"advise\" or \"deny\", or " +
						"{\"decision\": ..., \"lines\": int} where the rule takes lines (read-navigation alone). " +
						"A rule left out keeps the decision `magus describe rules` lists. Declared at the top " +
						"level of the root magusfile, once. When the magusfile is tracked, each rule takes the " +
						"stricter of the committed and the working-tree setting, so a loosening applies only " +
						"once committed. An unknown rule name, an unknown decision, lines on a rule that takes " +
						"none, declaring twice or from another project is MGS1045.",
					Args:   []Arg{{Name: "rules", Type: TypeAnyMap}},
					Extern: true,
				},
				{
					Name:   "shell",
					Doc:    "Declare one additive shell rule matched on a resolved program name and optional arg subset. Declared at the top level of the root magusfile. decision is deny or advise; optional dialect selects the parser (posix, bash, mksh, zsh, bats).",
					Args:   []Arg{{Name: "rule", Type: TypeAnyMap}},
					Extern: true,
				},
				{
					Name: "spawn",
					Doc: "Register the one function the agent guard calls on every agent spawn and every " +
						"continuation of an existing subagent: fun(req: SpawnRequest) > GuardVerdict. " +
						"Declared at the top level of the root magusfile, once. Strengthen only: its deny " +
						"blocks, its advise fills silence, and nothing it returns lifts a built-in deny. A rule " +
						"that raises or returns something other than a verdict fails open with an advisory " +
						"naming the failure. When the magusfile is tracked, the committed and the " +
						"working-tree rule both run and the stricter answer stands. Registering twice, from " +
						"another project, or with a non-function is MGS1045. magus ships no rule.",
					Args:   []Arg{{Name: "rule", Type: TypeFunc, Func: "fun (req: SpawnRequest) > GuardVerdict !> any"}},
					Extern: true,
				},
				{
					Name: "command",
					Doc: "Register the one function the agent guard calls on every shell command an agent " +
						"is about to run: fun(req: CommandRequest) > GuardVerdict. The function form of " +
						"guard.shell, for a decision a program-and-args match cannot express. Declared at the " +
						"top level of the root magusfile, once. Strengthen only, fails open, and is evaluated " +
						"from both the committed and the working-tree sources, exactly as guard.spawn is. " +
						"Registering twice, from another project, or with a non-function is MGS1045. magus " +
						"ships no rule.",
					Args:   []Arg{{Name: "rule", Type: TypeFunc, Func: "fun (req: CommandRequest) > GuardVerdict !> any"}},
					Extern: true,
				},
				{
					Name: "write",
					Doc: "Register the one function the agent guard calls on every file an agent writes " +
						"through its host's edit tools: fun(req: WriteRequest) > GuardVerdict. Declared at the " +
						"top level of the root magusfile, once. Strengthen only, fails open, and is evaluated " +
						"from both the committed and the working-tree sources, exactly as guard.command is. " +
						"Registering twice, from another project, or with a non-function is MGS1045. magus " +
						"ships no rule.",
					Args:   []Arg{{Name: "rule", Type: TypeFunc, Func: "fun (req: WriteRequest) > GuardVerdict !> any"}},
					Extern: true,
				},
				{
					Name:    "allow",
					Doc:     "The verdict a spawn, command or write rule returns to add nothing.",
					Returns: []Ret{{Type: TypeAnyMap, Object: "GuardVerdict"}},
					Extern:  true,
				},
				{
					Name:    "advise",
					Doc:     "The verdict a spawn, command or write rule returns to let the call through with text for the agent.",
					Args:    []Arg{{Name: "text", Type: TypeString}},
					Returns: []Ret{{Type: TypeAnyMap, Object: "GuardVerdict"}},
					Extern:  true,
				},
				{
					Name:    "deny",
					Doc:     "The verdict a spawn, command or write rule returns to block the call, with text saying why.",
					Args:    []Arg{{Name: "text", Type: TypeString}},
					Returns: []Ret{{Type: TypeAnyMap, Object: "GuardVerdict"}},
					Extern:  true,
				},
				{
					Name: "once",
					Doc: "True the first time key is asked in the calling agent's session, false after. " +
						"Only callable inside a spawn, command or write rule while the guard runs it.",
					Args:    []Arg{{Name: "key", Type: TypeString}},
					Returns: []Ret{{Type: TypeBool}},
					Extern:  true,
				},
				{
					Name: "count",
					Doc: "Adds one to key's tally in the calling agent's session and returns the new " +
						"total, starting at 1. Only callable inside a spawn, command or write rule while the guard runs it.",
					Args:    []Arg{{Name: "key", Type: TypeString}},
					Returns: []Ret{{Type: TypeInt}},
					Extern:  true,
				},
				{
					Name: "binary",
					Doc: "The magus binary answering the guard: {path, stamp}. path is the running executable " +
						"with symlinks resolved; stamp is the text its build passed through the linker, empty " +
						"when it passed none, so a rule can tell which sources that build came from.",
					Returns: []Ret{{Type: TypeAnyMap, Object: "GuardBinary"}},
					Extern:  true,
				},
			},
			Objects: []string{"SpawnRequest", "CommandRequest", "WriteRequest", "GuardBinary"},
		},
		{
			Name: "harness",
			Doc:  "Agent-host harness selection. A harness is a spell that exports the harness_config / harness_skills / harness_entries contract, the same family as cache.remote and ci.provider.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Wire an imported harness spell for this workspace. A workspace may use several (one per agent host), same append-on-repeat shape as workspace.provider. Declared at the top level of the root magusfile.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "secret",
			Doc:  "Secret provider selection, and the credentials read through it.",
			Methods: []Method{
				{
					Name:   "provider",
					Doc:    "Select the secret provider, given an imported spell handle.",
					Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
					Extern: true,
				},
				{
					Name: "read",
					Doc:  "Read a credential by reference through the selected provider. Unlike the selections, this is called from inside a target, so its failure IS something a caller can handle.",
					Args: []Arg{{Name: "ref", Type: TypeString}},
					// A magus-resolved value rather than a bare str, which is what lets
					// magus recognize it and keep it out of logs and cache keys.
					Returns: []Ret{{Type: TypeString}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "endpoint",
					Doc:  "Open a loopback base URL carrying the credential a grant names, for a CHILD PROCESS to be pointed at instead of the real API. magus attaches the credential upstream, so the child never holds it.",
					// TypeAny, not TypeAnyMap: the grant is an object INSTANCE the
					// magusfile declares itself (ref/host/header/prefix), and an object
					// does not satisfy a {str: any} annotation. The shape is checked at
					// the call, by secretGrantArg.
					Args:    []Arg{{Name: "grant", Type: TypeAny}},
					Returns: []Ret{{Type: TypeString}},
					Raises:  true,
					Extern:  true,
				},
			},
		},
		{
			Name: "review",
			Doc: "Where this workspace's changes are discussed. One member: a magusfile says " +
				"WHERE reviews live and does not conduct one, so opening a review, reading its " +
				"threads and publishing drafts are reserved names on the provider spell instead " +
				"(see spells/review.go). Wiring no provider is the ordinary state and never an " +
				"error: the workspace reviews locally.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the review provider, given an imported spell handle.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "lifecycle",
			Doc: "Where this workspace learns when its tools' release cycles reach end of life. " +
				"The provider spell exports list_lifecycles (see spells/lifecycle.go) and answers " +
				"for the products the spells' tools name (Tool.lifecycle); magus\\describe.tool() and " +
				"`magus describe tools` carry the answer, and nothing it says fails a build. One " +
				"provider per workspace. Wiring none is the ordinary state: the lifecycle columns read -.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the lifecycle provider, given an imported spell handle. Declared at the top level of the root magusfile; wiring a second, different spell is an error.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "workspace",
			Doc:  "Workspace-level declarations made from the root magusfile.",
			Methods: []Method{{
				Name:   "provider",
				Doc:    "Select the workspace provider, given an imported spell handle.",
				Args:   []Arg{{Name: "spell", Type: TypeAnyMap}},
				Extern: true,
			}},
		},
		{
			Name: "job",
			Doc: "The declared job store: what an orchestrating agent said about work it " +
				"handed out, recorded so a human can see the plan the agents are running. Rows are " +
				"DECLARATIONS: this store gates no run and blocks no write to the tree; the agent " +
				"guard is what reads them to grade a write, and register's verdict is a fact it " +
				"returns rather than a gate. The one thing the store DOES refuse is a write to a " +
				"row the caller does not own. See the field docs on " +
				"types.Job. This namespace, the client MCP tool (magus\\job) and `magus job` " +
				"(fork, exec, exit, wait) are the three WRITE doors onto it. Bound by " +
				"hand in internal/interp/bindings (buildJob), not generated: a Namespace's " +
				"methods are Extern by construction (see std.Namespace), so there is no Impl for " +
				"codegen to reflect a trampoline from, the same reason magus\\secret.read is hand-bound.",
			Methods: []Method{
				{
					Name: "list",
					Doc: "Every row as one typed report: {jobs, overlaps}. jobs are in the order " +
						"they were declared; overlaps are derived on this read - every pair of live " +
						"(non-terminal) jobs whose declared write_paths intersect: the same " +
						"derivation magus\\job.list and the console's " +
						"JobService.ListJobs use, so the three cannot disagree about a collision. " +
						"Annotate the result `> JobList` for compile-checked field access. " +
						"Read straight off the workspace already open on the context - no subprocess. " +
						"Works from a magusfile target and from a `magus buzz` script run inside a " +
						"workspace; raises MGS1022 only when there is no workspace to read. " +
						"Inside a magus\\guard.spawn, command or write rule it answers from the rows the guard read for " +
						"that call, and every other job member raises there: the store is read-only " +
						"to a rule.",
					Returns: []Ret{{Type: TypeAnyMap, Object: "JobList"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "put",
					Doc: "Record or advance one row, merging only the fields opts names: parent, " +
						"criteria, checkpoint, write_paths, deny_paths, read_paths, depends_on, model, " +
						"check (`<target> <project> [-- args]`, or the rendered `magus run` line as " +
						"validation, never both), state (declared, running, exited, pass, fail, no_return), read_only. A " +
						"key opts omits is left untouched, so a later put in a job's lifecycle (e.g. " +
						"{state = \"running\"}) advances it without erasing what an earlier put " +
						"declared; a key present with an empty value is an explicit clear. id is the " +
						"row's identity to upsert on - the value an orchestrator should also put in " +
						"the worker's prompt, so the console can join activity to the row. " +
						"created/updated/releases/write_proof are stamped by the store and cannot be set " +
						"here. A put that CREATES a row raises when its write_paths cover a file the " +
						"workspace has to load (a magusfile, a magus.yaml, an imported spell source) " +
						"while another live job with write paths is bound to this checkout: that job " +
						"needs a worktree of its own, since a half-saved one of those stops the " +
						"workspace loading for every job here at once. " +
						"Returns the stored row. Read straight off the workspace already open on the " +
						"context - no subprocess. Works from a magusfile target and from a `magus " +
						"buzz` script run inside a workspace; raises MGS1022 only when there is no " +
						"workspace to read.",
					Args: []Arg{
						{Name: "id", Type: TypeString},
						{Name: "opts", Type: TypeAnyMap, Optional: true},
					},
					Returns: []Ret{{Type: TypeAnyMap, Object: "Job"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "register",
					Doc: "Report the base a worker actually landed on, and learn how it compares " +
						"with the checkpoint the job was handed. reported_base is a checkpoint token " +
						"in the form `magus vcs checkpoint -o name` prints: `<rev>`, or `<rev>+<digest>` " +
						"when the tree is dirty. Returns {job, advice}: the stored row, and a " +
						"sentence naming both revisions and what to do next. The row carries " +
						"reported_base, registered, and base_verdict - one of match (same token), " +
						"revision-match (same revision, different uncommitted patch), diverged " +
						"(different revision), or unknown (the job was declared without a " +
						"checkpoint, so there is nothing to compare against). The verdict is a FACT " +
						"returned and recorded, NEVER a refusal: a diverged registration succeeds " +
						"like any other, and what to do about it is the caller's and the " +
						"orchestrator's call. The one write here that does not create the row it " +
						"names - an id nothing declared means the worker was handed the wrong id, so " +
						"it raises rather than inventing a row. Read straight off the workspace " +
						"already open on the context - no subprocess. Works from a magusfile target " +
						"and from a `magus buzz` script run inside a workspace; raises MGS1022 only " +
						"when there is no workspace to read.",
					Args: []Arg{
						{Name: "id", Type: TypeString},
						{Name: "reported_base", Type: TypeString},
					},
					Returns: []Ret{{Type: TypeAnyMap}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "clear",
					Doc: "Drop every row, which is how a fresh plan starts. Returns how many rows " +
						"it dropped - a fresh or already-empty store clears 0, which is not an " +
						"error. Clearing is also how one orchestrator can silently erase another's " +
						"plan, so a caller unsure whether it owns the whole store should list() " +
						"first. Read straight off the workspace already open on the context - no " +
						"subprocess. Works from a magusfile target and from a `magus buzz` script " +
						"run inside a workspace; raises MGS1022 only when there is no workspace to " +
						"read.",
					Returns: []Ret{{Type: TypeInt}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "exit",
					Doc: "File a worker's result and the recorded attempt behind its output_ref, " +
						"then move the job to exited. With no result it records no_return instead: " +
						"the worker did not return evidence, which is distinct from a result that " +
						"fails verification. A result is decoded with the same strict, versioned " +
						"contract as `magus job exit --stdin`; keys use that JSON spelling " +
						"(schema_version, changed_paths, output_ref). The output ref is resolved " +
						"from this checkout before it is filed, so a verifier in another worktree " +
						"can read the evidence without this cache. Returns the stored row.",
					Args: []Arg{
						{Name: "id", Type: TypeString},
						{Name: "result", Type: TypeAnyMap, Optional: true},
					},
					Returns: []Ret{{Type: TypeAnyMap, Object: "Job"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "wait",
					Doc: "Mechanically verify a returned result against the current job terms and " +
						"record pass only when every check holds. A rejected result returns JobStatus " +
						"with every violation and leaves the row's state unchanged; it does not raise. " +
						"With no result it uses the result and attempt filed by exit. With one, it " +
						"decodes that strict, versioned JSON-shaped map and resolves its output_ref " +
						"from this checkout. A holder cannot wait on its own job.",
					Args: []Arg{
						{Name: "id", Type: TypeString},
						{Name: "result", Type: TypeAnyMap, Optional: true},
					},
					Returns: []Ret{{Type: TypeAnyMap, Object: "JobStatus"}},
					Raises:  true,
					Extern:  true,
				},
			},
		},
		{
			Name: "describe",
			Doc: "Typed reads of what `magus describe <noun>` prints, one method per noun: " +
				"`magus\\describe.spell(\"go\")` returns the Spell records `magus describe spell go -o json` " +
				"carries. A method whose noun takes a name takes it as an optional SELECTOR and returns " +
				"a collection either way, so detailing one reads `[0]`. A report that carries more than " +
				"its collection (a file classification's overlaps, the tools' lifecycle state) comes " +
				"back whole. The methods that fork a nested magus work from a `magus buzz` script with " +
				"no workspace loaded; opts.root and opts.dir are as for magus\\run. Bound by hand in " +
				"internal/interp/bindings (buildDescribe): a Namespace's methods are Extern by " +
				"construction. A noun with no method here is reached through magus\\cmd(\"describe\", [...]).",
			Methods: []Method{
				{
					Name:    "file",
					Doc:     "Classify paths against the workspace's declared globs: for each, the owning project and whether it is a declared `output` (generated - regenerate it, never hand-edit), a declared `source` (it feeds cache keys and the affected set), `maintained` (magus writes it outside any target - commit it, never ignore it), or `unclaimed`. Returns a typed DoctorReport-style envelope {definition, count, files, overlaps}, not text to re-parse: this is the question \"can I disregard this changed file\", and a caller branches on `role` rather than grepping. Runs a nested magus.",
					Args:    []Arg{{Name: "paths", Type: TypeStringSlice}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAnyMap, Object: "FileReport"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "module",
					Doc:     "The host modules magus exposes, with their fields, methods and rendered Buzz signatures - the records `magus describe module` prints. Omit `name` for every module; an unknown name raises. Read in-process from the host module registry.",
					Args:    []Arg{{Name: "name", Type: TypeString, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[Module]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "spell",
					Doc:     "The registered spells, sorted by name: each Spell carries its targets, the language it adapts and the rest of its mgs_getLanguage record (extensions, and syntax with its comments and stubs), so a script reads a language's comment and stub syntax as typed fields. Omit `name` for every spell; an unknown name raises. Runs a nested magus.",
					Args:    []Arg{{Name: "name", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[Spell]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "charm",
					Doc:     "The charms in the workspace's inverse index: each {name, builtin, default, doc, declarations}, declarations naming every target that patches its argv for that charm. Omit `name` for every charm; an unknown name raises. Runs a nested magus.",
					Args:    []Arg{{Name: "name", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[CharmEntry]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "target",
					Doc:     "The target catalog: each target NAME once, {name, kind, spells, projects}, across every project that declares it. evaluatedTarget details one project's target. Runs a nested magus.",
					Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[TargetEntry]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "evaluated_target",
					Doc:     "The targets `ref` names (path:target, or a bare target name for every project declaring it), as magus would run them: sources, outputs, the dependency chain, the charms and each spell's rendered command. Raises on a ref nothing matches. Runs a nested magus.",
					Args:    []Arg{{Name: "ref", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[EvaluatedTarget]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "project",
					Doc:     "The workspace's projects: {workspace, count, projects}, each project {path, dir, spell, spells, sources, outputs, dependsOn, exclusive}. Read in-process from the workspace on the context; raises MGS1022 outside one.",
					Returns: []Ret{{Type: TypeAnyMap, Object: "Projects"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "evaluated_project",
					Doc:     "The projects with their spells resolved: each project's fields plus resolvedSpells and targetPolicies, as `magus describe project --evaluated` prints them. Omit `path` for every project; an unknown path raises. Runs a nested magus.",
					Args:    []Arg{{Name: "path", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[EvaluatedProject]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "graph",
					Doc:     "The TARGET dependency graph of every project: {projects}, each project {path, name, engine, nodes, cycle, dependsOn} and each node {name, declared, doc, dependencies, charms, spells, crossDependencies, inputs, outputs}. This is the per-project view magus\\projectGraph() does not carry. Read statically from the magusfile source, so it never runs a target body. Served in-process from the workspace on the context when there is one, and through a nested magus when there is not.",
					Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAnyMap, Object: "TargetGraph"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "graph_markdown",
					Doc:     "The routing index `magus describe graph -o markdown` renders (MAGUS.md), as text. `projects` scopes it to those project paths and leaves out the workspace-wide tables; omit it for the whole index. Runs a nested magus.",
					Args:    []Arg{{Name: "projects", Type: TypeStringSlice, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeString}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "workspace",
					Doc:     "The workspace this call runs in: {root, vcsBaseRef, cacheDir, concurrency, projectCount}, as a one-element collection. Runs a nested magus.",
					Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[WorkspaceEntry]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "tool",
					Doc:     "Every project's tools: {workspace, count, lifecycle, tools}, each tool {project, bin, spell, installedVersion, probeError, spellBounds, workspaceBounds, effective, verdict, diagnosticCode, lifecycle, cycle, eol, support}. Probes every tool's version, and asks the wired lifecycle provider (magus\\lifecycle.provider) when each installed cycle reaches its end of life; lifecycle.state says whether that answer is live, cached, offline, unreached or unwired, and support reads unknown for a row it could not answer. Served in-process from the workspace on the context when there is one, and through a nested magus when there is not.",
					Returns: []Ret{{Type: TypeAnyMap, Object: "ToolReport"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "rule",
					Doc:     "The guard rules this workspace enforces: each {name, decision, catches, why}, decision `deny` or `advise`. Omit `name` for every rule; an unknown name raises. Runs a nested magus.",
					Args:    []Arg{{Name: "name", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[Rule]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "harness",
					Doc:     "What each wired harness's host config needs merged: {id, files, merge, mcpHint, wired} per harness, files keyed by workspace path. magus never writes host config; the person runs merge. Omit `id` for every wired harness; an unknown id raises. Runs a nested magus.",
					Args:    []Arg{{Name: "id", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[HarnessPlan]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "mcp_tool",
					Doc:     "The MCP tools the magus server exposes: each {name, description, params}. Omit `name` for every tool; an unknown name raises. Runs a nested magus.",
					Args:    []Arg{{Name: "name", Type: TypeString, Optional: true}, {Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "[MCPTool]"}},
					Raises:  true,
					Extern:  true,
				},
			},
		},
		{
			Name: "service",
			Doc: "Shared services a `magus buzz` script holds, the way a target holds one through " +
				"ctx.needs: routed to the broker when one is up, so the service stays warm past the " +
				"script, and hosted in-process otherwise. Every lease a script still holds is released " +
				"when it ends, however it ends. Raises MGS1022 outside a workspace, and from a magusfile, " +
				"where ctx.needs is the way to hold one. Bound by hand in internal/interp/bindings " +
				"(buildService), for the reason magus\\job is.",
			Methods: []Method{
				{
					Name: "acquire",
					Doc: "Start, or reuse, the service a built-in spell's service op declares, and hold it " +
						"until release or the script's end. Returns once it is ready. A start service " +
						"already running is adopted: owned is false and magus never stops it. Raises " +
						"when the spell or op is unknown, the op is not a service, or the service never " +
						"becomes ready.",
					Args: []Arg{
						{Name: "spell", Type: TypeString},
						{Name: "op", Type: TypeString},
					},
					Returns: []Ret{{Type: TypeAnyMap, Object: "ServiceLease"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "release",
					Doc: "Drop a lease acquire returned, before the script ends. The service stays up " +
						"while another lease holds it; the broker keeps an owned one warm for its idle " +
						"window, and an in-process one stops. Each call drops one reference to the " +
						"lease's service, so release a lease once.",
					Args:   []Arg{{Name: "lease", Type: TypeAnyMap, Object: "ServiceLease"}},
					Raises: true,
					Extern: true,
				},
			},
		},
		{
			Name: "vcs",
			Doc: "Facts about the workspace's version control that only magus computes. The " +
				"`vcs` host module is the repository itself; this namespace is magus's reading " +
				"of it. Bound by hand in internal/interp/bindings (buildVCS).",
			Methods: []Method{{
				Name: "checkpoint",
				Doc: "The identity of the working state: revision, branch, dirtiness and the " +
					"uncommitted patch's digest, the record `magus vcs checkpoint -o json` prints. " +
					"It never preserves: minting an object in the repository is the CLI's " +
					"--preserve. Raises MGS1022 outside a workspace.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "VCSCheckpoint"}},
				Raises:  true,
				Extern:  true,
			}},
		},
		{
			Name: "trail",
			Doc: "A session's guard record, read from the activity trail, and the verdicts a person " +
				"gives its rows. Bound by hand in internal/interp/bindings (buildTrail).",
			Methods: []Method{
				{
					Name: "read",
					Doc: "One session's guard record inside a window: {session, host, since, until, checkouts, observations, spawns}. " +
						"opts.session picks the session; omitted, it is the session with the newest observation in the window. " +
						"opts.since is a duration back from opts.until (`6h`) or an RFC3339 time, default 24h; opts.until is an RFC3339 time, default now. " +
						"Reads the trail of every checkout of this repository that changed inside the window, because a session's hooks record into the checkout they ran from. " +
						"An unknown option or an unreadable time raises, and MGS1022 outside a workspace.",
					Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true}},
					Returns: []Ret{{Type: TypeAny, Object: "FeedbackTrail"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name:    "shape",
					Doc:     "A shell line with its paths, patterns and literals normalized away, so calls differing only in what they name read alike: `grep -rn foo src` and `grep -rn bar lib` are both `grep -rn <arg>`. Programs, flags, operators and redirections stay. \"\" for a line the shell parser cannot read.",
					Args:    []Arg{{Name: "command", Type: TypeString}},
					Returns: []Ret{{Type: TypeString}},
					Extern:  true,
				},
				{
					Name:    "shapes",
					Doc:     "The shape of each program a shell line runs, in order, each with its own redirections: `cd x && grep -rn foo src | head -5` is [`cd <path>`, `grep -rn <arg>`, `head -<n>`]. Programs inside a loop or a command substitution count. Empty for a line the shell parser cannot read.",
					Args:    []Arg{{Name: "command", Type: TypeString}},
					Returns: []Ret{{Type: TypeStringSlice}},
					Extern:  true,
				},
				{
					Name:    "marks",
					Doc:     "Every verdict a person recorded on a trail row in this repository, oldest first; a later mark on an id supersedes an earlier one. Kept per repository identity, so every checkout reads the same marks. Raises on a store line that does not decode, and MGS1022 outside a workspace.",
					Returns: []Ret{{Type: TypeAny, Object: "[FeedbackMark]"}},
					Raises:  true,
					Extern:  true,
				},
				{
					Name: "mark",
					Doc: "Record a person's verdict on one trail row and return it as stored, its time stamped. " +
						"id is the row's stable id (fb and 12 hex digits), section one of refused, advised, unguarded, next-not-taken, verdict one of should-deny, should-advise, wrong-deny, fine; key names the rule or shape the row groups by. " +
						"Appends to the per-repository store and rewrites nothing. Raises on a malformed mark, an unwritable store, and MGS1022 outside a workspace.",
					Args:    []Arg{{Name: "mark", Type: TypeAnyMap, Object: "FeedbackMark"}},
					Returns: []Ret{{Type: TypeAny, Object: "FeedbackMark"}},
					Raises:  true,
					Extern:  true,
				},
			},
		},
	},
	MCPTools: magusMCPTools,
}

// magusMCPTools is the magus module's MCP tool list, the source the generated MCP
// registry (internal/handler/mcp/gen) is emitted from. Kept beside the Magus
// literal rather than inside it because the descriptions are agent-facing prose and
// dwarf the declarations they sit next to.
//
// client exposes the magus\ API: a Buzz program calls the typed members and
// returns a value. magus\cmd is not part of it. The tools beside client
// are the operations no member covers.
var magusMCPTools = []MCPTool{
	{
		Name: hint.ToolClient.String(),
		Doc: "Run a Buzz program against the magus client and return its value. " +
			"Define `main(args: [str])` and return a JSON-encodable value; the result is under `json`, and std\\print text under `stdout`. " +
			"`import \"magus\"` is required to call the workspace. Also importable: std, math, crypto, serialize, buffer, and the pure host modules (those marked WASM) except env. " +
			"File imports, native FFI, and fs, proc, http, os, net, vcs, and env are refused. " +
			"Call `magus\\describe.module(\"magus\")` for the signatures. Strict mode: label arguments after the first, and annotate every parameter. " +
			"For a subsystem, `magus\\dir(path)`, `magus\\dirs(glob)`, `magus\\layer(name)` and `magus\\neighborhood(focus)` return typed records (Dir, [Dir], Layer, NeighborhoodResult) with imports, declared calls and layers; a directory the graph lacks raises MGS7005 and an undeclared layer MGS7006. " +
			"Variables do not persist between calls; filter a large result in the script before returning it. " +
			"Each call runs in a separate process. Bounded at 10 minutes when called directly; a host that supports MCP tasks can run it as a task without that bound. " +
			"Example: `import \"magus\"; fun main(args: [str]) > any !> str { return magus\\query(\"kind=spell\"); }`",
		Params: []MCPParam{
			{Name: "script", Type: TypeString, Doc: "Inline Buzz source. Exactly one of script or path."},
			{Name: "path", Type: TypeString, Doc: "A .buzz file inside the workspace, relative to its root. Exactly one of script or path."},
			{Name: "args", Type: TypeStringSlice, Doc: "Array of strings passed to main."},
		},
	},
	{
		// No Member: MCP Buzz transforms supplied data in a forked interpreter.
		// Workspace queries and effects go through the client tool.
		Name: hint.ToolBuzz.String(),
		Doc: "Transform a supplied JSON object with Buzz. Define `transform(input: any, args: [str])` and return a JSON-encodable value; the result is returned under `json`, and std\\print text under `stdout`. " +
			"Only std, math, crypto, serialize and buffer are importable. File imports, native FFI and workspace-changing host modules are unavailable. " +
			"For workspace queries or actions, use the client tool. `import \"magus\"` returns a diagnostic explaining this boundary. " +
			"Use the regular `magus buzz` CLI when a script needs the full set of host modules. Each call runs in a separate process, bounded by the workspace's target_timeout (30 seconds when unset).",
		Params: []MCPParam{
			{Name: "script", Type: TypeString, Doc: "Inline Buzz source. Exactly one of script or path."},
			{Name: "path", Type: TypeString, Doc: "A .buzz file inside the workspace, relative to its root. Exactly one of script or path."},
			{Name: "args", Type: TypeStringSlice, Doc: "Array of strings passed to transform as its second argument."},
			{Name: "input", Type: TypeAnyMap, Doc: "JSON object passed to transform as its first argument, typically the result of another MCP tool."},
		},
	},
	{
		Name: hint.ToolStatus.String(),
		Doc:  "Report the workspace's configured telemetry, cache settings, and live proc-server pool state (when a parent magus is running).",
	},
	{
		Name: hint.ToolConsole.String(),
		Doc:  "Return a tokenless link to a local magus console app, plus `open`: a shell command that opens it signed in by minting the token in the person's own shell. Hand the person `open` to run rather than the bare link, which opens an unauthenticated page. Use this only when a person asked to see the dashboard or related status; this tool never opens a browser or changes console state.",
		Params: []MCPParam{
			{Name: "app", Type: TypeString, Doc: "Console app to show: dashboard (default), activity, logs, graph, notes, diff, plan, or runs."},
			{Name: "reason", Type: TypeString, Doc: "Optional brief text a compatible MCP client may show with the link."},
		},
	},
	{
		Name: hint.ToolConfig.String(),
		Doc:  "Return the resolved workspace configuration as JSON. Read-only - use the magus CLI to edit config.",
	},
	{
		// No Member: magus\diff reads the WORKING TREE's uncommitted changes. This
		// joins a live review session the server holds, which no Buzz member reaches.
		Name: hint.ToolDiff.String(),
		Doc: "Join the review session a person already has open and pair with them on it. " +
			"op=state returns the whole session: every changed file annotated with its role (generated output vs source), " +
			"how widely its changed symbols are referenced, whether it is part of the public API, observed coverage, " +
			"plus where the person is looking and what they have already read. " +
			"op=state also returns `patch` (the unified diff) and `hunks` (per file, each hunk's 0-based index and its content digest), " +
			"which are the coordinates comment and suggest take - so read state first and cite an index from it rather than guessing one. " +
			"It recomputes when the working tree has moved since the session was attached, and sets `recomputed` when it did. " +
			"op=comment attaches a remark to a hunk. op=suggest asks for their attention somewhere, with a reason. " +
			"op=resolve closes a comment. Both writing ops REFUSE a path that is not in the change and a hunk index that does not exist. " +
			"You CANNOT move their cursor or mark a hunk read: suggest, and they accept with one key. " +
			"Read state before commenting - `viewed` holds the same hunk digests, so you can skip what they have already seen.",
		Params: []MCPParam{
			{Name: "op", Type: TypeString, Doc: "One of: state (default), comment, suggest, resolve."},
			{Name: "projection", Type: TypeString, Doc: "Shapes op=state's response only - comment, suggest, and resolve ignore it and always return the full session. One of: full (default; today's whole session), summary (id/base/as_of/recomputed/cursor plus counts of files, hunks, comments, suggestions, and viewed - no bodies), conversation (cursor, viewed, comments, suggestions, id/base/as_of - no diff, patch, or hunks), patch (id/base/as_of/recomputed plus patch and hunks - no diff, comments, or suggestions)."},
			{Name: "path", Type: TypeString, Doc: "Workspace-relative file the comment or suggestion is about (comment, suggest)."},
			{Name: "hunk", Type: TypeInt, Doc: "0-based hunk index within the file, as reported by op=state's `hunks`; omit for the file as a whole. An index the file does not have is refused."},
			{Name: "body", Type: TypeString, Doc: "The remark (comment)."},
			{Name: "reason", Type: TypeString, Doc: "Why this is worth their attention - required, because a suggestion is an interruption (suggest)."},
			{Name: "id", Type: TypeString, Doc: "Comment id (resolve)."},
			{Name: "agent_name", Type: TypeString, Doc: "Optional label for which agent is speaking; attribution only."},
		},
	},
}

// MagusHasCharm reports whether the execution charm name is active in ctx. It
// backs magus.has_charm, the read side of the charm system: a function target
// can publish conditionally on has_charm("rw") or branch on a custom charm.
func MagusHasCharm(ctx context.Context, name string) (bool, error) {
	return types.HasCharm(ctx, name), nil
}

// MagusBustCache invalidates cached build entries. When projectPath is empty
// all manifests are cleared; otherwise only entries for that project are removed.
// A structured warning is always emitted: this is an escape hatch, not routine.
func MagusBustCache(ctx context.Context, projectPath string) error {
	slog.WarnContext(ctx, "magus.bust_cache called - consider modeling the missing input as a Source instead",
		"project_path", projectPath)
	c := cache.FromContext(ctx)
	if c == nil {
		return nil // no cache in context (parse mode, tests)
	}
	if projectPath == "" {
		return c.Delete(ctx)
	}
	return c.Delete(ctx, projectPath)
}

// typedMagusSubcommands are the magus subcommands that have a dedicated,
// typed magus.<name>(...) method. magus.cmd warns when its first arg names one,
// nudging authors toward the clearer, signature-stable wrapper.
var typedMagusSubcommands = map[string]bool{
	"run": true, "doctor": true, "clean": true,
}

// typedDescribeNouns maps every `magus describe` noun spelling that has a
// magus\describe method to that method's Buzz name. A noun missing here has no
// typed method, so magus.cmd("describe", [noun, ...]) is the way to reach it and
// draws no warning.
var typedDescribeNouns = map[string]string{
	"file": "file", "files": "file",
	"module": "module", "modules": "module",
	"spell": "spell", "spells": "spell",
	"charm": "charm", "charms": "charm",
	"target": "target", "targets": "target",
	"project": "project", "projects": "project",
	"graph": "graph", "graphs": "graph",
	"workspace": "workspace", "workspaces": "workspace",
	"tool": "tool", "tools": "tool",
	"rule": "rule", "rules": "rule",
	"harness": "harness", "harnesses": "harness",
	"mcp-tool": "mcpTool", "mcp-tools": "mcpTool",
}

// errNoWorkspace is the MGS1022 error a magus.* member raises when it is called
// without the loaded workspace it reads from. Every such member serves its answer
// IN-PROCESS off types.WorkspaceFromContext, which magus.Open puts there for a
// magusfile target; a `magus buzz` script has no workspace open, and a member that
// forks a nested magus (magus.cmd, most magus\describe methods) is what it reaches
// for instead. Coded so the constraint is greppable and linkable rather than one
// more bare sentence.
func errNoWorkspace(member string) error {
	return types.DiagnosticErrorf(types.MagusfileOnlyMember,
		"magus\\%s: no workspace on the context - it is callable from a magusfile target, not from a spell or a `magus buzz` script; reach for magus\\cmd or a magus\\describe method that forks a nested magus, which discovers the workspace itself", member)
}

// MagusDescribeProject implements magus\describe.project: the workspace's projects,
// from the workspace already open on ctx.
//
// The first of the read-only verbs served IN-PROCESS. The typed methods below it fork a
// full magus subprocess via runMagus (a spawn, a second workspace load, a JSON encode
// and a Buzz-side parse) to answer a question this process already holds.
//
// The workspace reaches ctx via types.WithWorkspace in magus.Open's load, so it is
// present for every magusfile target. A bare Buzz script has none, hence the guard.
func MagusDescribeProject(ctx context.Context) (types.ProjectsOutput, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.ProjectsOutput{}, errNoWorkspace("describe.project")
	}
	return ws.ListProjects(ctx)
}

// MagusDescribeGraph implements magus\describe.graph: every project's target graph.
// TargetGraph reads the magusfile statically, so this is side-effect free.
//
// It serves the workspace on the context when there is one and forks a nested magus
// when there is not, because the caller asking for the target graph is asking the same
// question either way; which magusfile it came from is magus's problem, not a name the
// caller should have to choose between.
func MagusDescribeGraph(ctx context.Context, opts map[string]any) (types.TargetGraphOutput, error) {
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		return ws.TargetGraph(ctx)
	}
	return runMagusJSON[types.TargetGraphOutput](ctx, "describe", []string{"graph"}, opts)
}

// MagusDescribeGraphMarkdown implements magus\describe.graphMarkdown: the MAGUS.md
// routing index as text, which no record type carries.
func MagusDescribeGraphMarkdown(ctx context.Context, projects []string, opts map[string]any) (string, error) {
	quiet := map[string]any{"quiet": true}
	for k, v := range opts {
		quiet[k] = v
	}
	args := append([]string{"graph", "-o", "markdown"}, projects...)
	res, err := runMagusSub(ctx, "describe", args, quiet)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// MagusDescribeTool implements magus\describe.tool: every project's tools, versions,
// windows and lifecycle columns. Like MagusDescribeGraph it serves the workspace on the
// context when there is one and forks a nested `magus describe tools` when there is not.
func MagusDescribeTool(ctx context.Context) (types.ToolReport, error) {
	type toolReporter interface {
		Tools(ctx context.Context, projects ...string) (types.ToolReport, error)
	}
	if ws, ok := types.WorkspaceFromContext(ctx).(toolReporter); ok {
		return ws.Tools(ctx)
	}
	return runMagusJSON[types.ToolReport](ctx, "describe", []string{"tools"}, nil)
}

// describeArgs is the argv for `magus describe <noun> [<name>]`: an empty name is
// the bare noun, which lists every entity.
func describeArgs(noun, name string) []string {
	if name == "" {
		return []string{noun}
	}
	return []string{noun, name}
}

// MagusDescribeCharm implements magus\describe.charm.
func MagusDescribeCharm(ctx context.Context, name string, opts map[string]any) ([]types.CharmEntry, error) {
	report, err := runMagusJSON[types.CharmReport](ctx, "describe", describeArgs("charm", name), opts)
	return report.Charms, err
}

// MagusDescribeTarget implements magus\describe.target: the target catalog.
func MagusDescribeTarget(ctx context.Context, opts map[string]any) ([]types.TargetEntry, error) {
	report, err := runMagusJSON[types.TargetReport](ctx, "describe", []string{"target"}, opts)
	return report.Targets, err
}

// MagusDescribeEvaluatedTarget implements magus\describe.evaluatedTarget.
func MagusDescribeEvaluatedTarget(ctx context.Context, ref string, opts map[string]any) ([]types.EvaluatedTarget, error) {
	report, err := runMagusJSON[types.EvaluatedTargetReport](ctx, "describe", []string{"target", ref}, opts)
	return report.Targets, err
}

// MagusDescribeEvaluatedProject implements magus\describe.evaluatedProject.
func MagusDescribeEvaluatedProject(ctx context.Context, path string, opts map[string]any) ([]types.EvaluatedProject, error) {
	args := append(describeArgs("project", path), "--evaluated")
	report, err := runMagusJSON[types.EvaluatedProjectsOutput](ctx, "describe", args, opts)
	return report.Projects, err
}

// MagusDescribeWorkspace implements magus\describe.workspace.
func MagusDescribeWorkspace(ctx context.Context, opts map[string]any) ([]types.WorkspaceEntry, error) {
	report, err := runMagusJSON[types.WorkspaceReport](ctx, "describe", []string{"workspace"}, opts)
	return report.Workspaces, err
}

// MagusDescribeRule implements magus\describe.rule. `describe rule <name>` prints
// the one rule bare, so a named read is wrapped to keep the collection shape.
func MagusDescribeRule(ctx context.Context, name string, opts map[string]any) ([]types.RuleDoc, error) {
	if name == "" {
		return runMagusJSON[[]types.RuleDoc](ctx, "describe", []string{"rule"}, opts)
	}
	rule, err := runMagusJSON[types.RuleDoc](ctx, "describe", []string{"rule", name}, opts)
	if err != nil {
		return nil, err
	}
	return []types.RuleDoc{rule}, nil
}

// MagusDescribeHarness implements magus\describe.harness. Like rule, a named read
// prints one plan bare, so it is wrapped to keep the collection shape.
func MagusDescribeHarness(ctx context.Context, id string, opts map[string]any) ([]types.HarnessPlan, error) {
	if id == "" {
		return runMagusJSON[[]types.HarnessPlan](ctx, "describe", []string{"harness"}, opts)
	}
	plan, err := runMagusJSON[types.HarnessPlan](ctx, "describe", []string{"harness", id}, opts)
	if err != nil {
		return nil, err
	}
	return []types.HarnessPlan{plan}, nil
}

// MagusDescribeMCPTool implements magus\describe.mcpTool.
func MagusDescribeMCPTool(ctx context.Context, name string, opts map[string]any) ([]types.MCPTool, error) {
	report, err := runMagusJSON[types.MCPToolReport](ctx, "describe", describeArgs("mcp-tool", name), opts)
	return report.MCPTools, err
}

// MagusAffected computes the affected project set in-process. See MagusDescribeProject
// for why the read-only verbs do not fork.
//
// It deliberately does NOT swallow ErrAffectedFallback. When the VCS cannot produce a
// diff, magus selects every project as a safety net (MGS1010); a magusfile branching on
// this result needs to know that happened, because "nothing changed" and "we could not
// tell what changed" call for opposite decisions.
func MagusAffected(ctx context.Context, base string) (types.AffectedResult, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.AffectedResult{}, errNoWorkspace("affected")
	}
	res, err := ws.Affected(ctx, base)
	if err != nil {
		return types.AffectedResult{}, err
	}
	return *res, nil
}

// MagusWhere returns the project path containing dir, or "" when dir is inside none.
// "" rather than an error: asking whether a path is inside a project is a question, and
// "no" is a valid answer a magusfile branches on.
func MagusWhere(ctx context.Context, dir string) (string, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return "", errNoWorkspace("where")
	}
	p, ok := ws.Where(dir)
	if !ok {
		return "", nil
	}
	return p.Path, nil
}

// MagusVCSCheckpoint backs magus\vcs.checkpoint: the identity of the workspace's working
// state (revision, branch, dirtiness, uncommitted-patch digest), the record `magus vcs
// checkpoint -o json` prints. It never preserves: minting an object in the repository is
// the CLI's --preserve, asked for rather than done on a read.
func MagusVCSCheckpoint(ctx context.Context) (types.VCSCheckpoint, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.VCSCheckpoint{}, errNoWorkspace("vcs.checkpoint")
	}
	res, err := vcs.Resolve(ctx, ws.Root(), "", ws.VCSOptions())
	if err != nil {
		return types.VCSCheckpoint{}, err
	}
	return vcs.Checkpoint(ctx, ws.Root(), res, false)
}

// MagusRaise fails with a caller-defined coded diagnostic.
//
// magus already gives a Buzz `catch` the code, message and url of its OWN failures, so a
// magusfile can branch on MGS2001 without matching prose. Authoring one was the missing
// half: a magusfile could only `throw` a string, leaving its callers doing the substring
// matching that mechanism exists to avoid.
//
// The MGS prefix is REFUSED, not discouraged: MGS codes are a closed catalog that
// `magus explain`, the knowledge graph and the docs URL map resolve against, so a
// workspace minting MGS9999 would render like magus's own and document nothing.
//
// `raise` rather than `throw` because throw is a reserved Buzz keyword; error and fatal
// are taken by the logging members above.
//
// cause and url live in an opts map: the generated trampoline binds by index, so a fourth
// positional url was unreachable without also passing a cause.
func MagusRaise(_ context.Context, code, message string, opts map[string]any) error {
	if code == "" {
		return errors.New(`magus\raise: needs a code, e.g. "ACME1001" - it is the stable identifier a caller branches on`)
	}
	if message == "" {
		return fmt.Errorf(`magus\raise: %s needs a message; a code is an identifier, not a sentence`, code)
	}
	if strings.HasPrefix(strings.ToUpper(code), "MGS") {
		return fmt.Errorf(`magus\raise: %q is in magus's own MGS namespace, which is a closed catalog; pick a prefix for this workspace instead`, code)
	}
	// A per-call domain is how a caller-supplied url reaches the rendered error: Error's
	// url field is captured at construction from the domain's function, never set later.
	url, _ := opts["url"].(string)
	d := diagnostics.New(func(diagnostics.Code) string { return url })
	summary, c := buzzCause(opts["cause"])
	if c == nil {
		return d.Errorf(diagnostics.Code(code), "%s", message)
	}
	// Wrapf keeps the cause reachable through Unwrap but deliberately does not render it,
	// so the summary is spliced in here to match what Go's %w prints. A cause nobody can
	// see is the failure the author was trying to report.
	return d.Wrapf(diagnostics.Code(code), c, "%s: %s", message, summary)
}

// buzzCause converts a caught Buzz value into the one-line summary to splice into the
// wrapper's message, plus the Go error to keep reachable underneath it.
//
// A structured catch arrives as the map BuzzError produced, so a coded cause is rebuilt as
// a coded error and errors.Is keeps matching it underneath the new code. The summary is
// the cause's message rather than its rendered form, which would drag a second "see:" line
// into the middle of the wrapper's.
func buzzCause(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "", nil
	case string:
		if t == "" {
			return "", nil
		}
		return t, errors.New(t)
	case map[string]any:
		msg, _ := t["message"].(string)
		code, _ := t["code"].(string)
		switch {
		case code != "":
			u, _ := t["url"].(string)
			e := diagnostics.New(func(diagnostics.Code) string { return u }).
				Errorf(diagnostics.Code(code), "%s", msg)
			return "[" + code + "] " + msg, e
		case msg != "":
			return msg, errors.New(msg)
		}
		return "", nil
	default:
		s := fmt.Sprintf("%v", t)
		return s, errors.New(s)
	}
}

// MagusGraph returns the project dependency graph as a flat object. See
// MagusDescribeProject for why the read-only verbs are served in-process.
func MagusGraph(ctx context.Context) (types.GraphView, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.GraphView{}, errNoWorkspace("projectGraph")
	}
	g, err := ws.Graph()
	if err != nil {
		return types.GraphView{}, err
	}
	return g.View(), nil
}

// MagusCmd is the escape hatch: it runs `magus <sub> <args>` for a subcommand with no
// dedicated wrapper (status, affected, agent, ...). The subcommand is a parameter of its
// own rather than args[0], which is what lets the warning below be exact instead of a
// guess at a list's first element, and what keeps the invocation readable to anything
// reading the source. Like the typed methods it runs in the contextual project dir
// unless opts.dir says otherwise; see runMagus.
func MagusCmd(ctx context.Context, sub string, args []string, opts map[string]any) (types.ExecResult, error) {
	warnIfTypedSubcommand(ctx, sub, args)
	return runMagusSub(ctx, sub, args, opts)
}

// warnIfTypedSubcommand warns when sub (and, for describe, its noun) names a
// command with a dedicated typed method, nudging authors off the escape hatch.
// A describe noun with no method draws nothing: magus.cmd is the way to reach it.
// Neither does one asked for in an explicit -o format, since a typed record is not
// the CLI's bytes (a file written for another reader wants those).
// It is the pure decision half of MagusCmd, split out so it can be tested without
// the nested exec.
func warnIfTypedSubcommand(ctx context.Context, sub string, args []string) {
	var hint string
	switch {
	case typedMagusSubcommands[sub]:
		hint = fmt.Sprintf("use magus\\%s([...]) instead of magus\\cmd(%q, [...])", sub, sub)
	case sub == "describe" && len(args) > 0 && typedDescribeNouns[args[0]] != "" && !asksForFormat(args):
		hint = fmt.Sprintf("use magus\\describe.%s(...) instead of magus\\cmd(\"describe\", [%q, ...])", typedDescribeNouns[args[0]], args[0])
	default:
		return
	}
	slog.WarnContext(ctx, "magus\\cmd called for a subcommand with a dedicated method; prefer it for clarity and a stable signature",
		"subcommand", sub, "hint", hint)
}

// asksForFormat reports whether args carry the global -o/--output flag.
func asksForFormat(args []string) bool {
	return slices.ContainsFunc(args, func(a string) bool {
		return a == "-o" || a == "--output" || strings.HasPrefix(a, "-o=") || strings.HasPrefix(a, "--output=")
	})
}

// MagusRun runs `magus run <args>` recursively; see runMagus.
func MagusRun(ctx context.Context, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagusSub(ctx, "run", args, opts)
}

// MagusAttention lists the open attention requests through a nested magus. Listing only:
// disposal is deliberately absent from this API, because a script that closes
// requests is the auto-disposition the doctrine's Manual-on-purpose table rules out.
func MagusAttention(ctx context.Context, args []string, opts map[string]any) (map[string]any, error) {
	return runMagusJSON[map[string]any](ctx, "session", append([]string{"attention"}, args...), opts)
}

// MagusDoctor validates the workspace and returns the typed report.
//
// It is the shape every method here should have and most do not yet: the child already
// knows how to say this in JSON, so the answer crosses the boundary as the domain type
// rather than as console text a caller has to parse back out of a string.
func MagusDoctor(ctx context.Context, args []string, opts map[string]any) (types.DoctorReport, error) {
	return runMagusJSON[types.DoctorReport](ctx, "doctor", args, opts)
}

// MagusClean forks `magus clean` and returns the paths it removed and the tracked
// outputs it kept.
func MagusClean(ctx context.Context, args []string, opts map[string]any) (types.CleanReport, error) {
	return runMagusJSON[types.CleanReport](ctx, "clean", args, opts)
}

// MagusImpact forks `magus affected --impact` and returns the blast radius.
func MagusImpact(ctx context.Context, base string, opts map[string]any) (types.ImpactResult, error) {
	args := []string{"--impact"}
	if base != "" {
		args = append(args, "--base", base)
	}
	return runMagusJSON[types.ImpactResult](ctx, "affected", args, opts)
}

// MagusInsight returns every insight lens as one typed report, read straight off
// the loaded workspace. An agent asks for one lens by returning that field from
// the client tool.
//
// This is the one magus\* method that does NOT fork a nested magus, and the difference is
// the point: analytics are a pure read of a workspace magus has already loaded, so paying
// a process, a second workspace load, and a JSON round trip to ask it a question about
// itself was cost with nothing bought. See std/workspace.go for how it reaches the API
// across an import cycle that used to make forking the only option.
func MagusInsight(ctx context.Context, opts map[string]any) (types.InsightReport, error) {
	a, err := insightAnalyzer(ctx, "insight")
	if err != nil {
		return types.InsightReport{}, err
	}
	iopts, err := insightOptions(opts)
	if err != nil {
		return types.InsightReport{}, err
	}
	return buildInsightReport(ctx, a, iopts)
}

// insightAnalyzer resolves the workspace that will answer the lenses.
//
// It ERRORS rather than forking. The `magus insight` subcommand it used to fall back
// to is gone: the analysis was always the workspace's own, the CLI only rendered it,
// and keeping a subcommand alive as an implementation detail of a host method is the
// sprawl this removal is about. The cost is that a bare `magus buzz` script with no
// workspace cannot ask, the same limit every other in-process verb here has.
//
// member names the caller so MGS1022 points at the method the author actually wrote.
//
// AnalyzerFromContext first: it is the seam a `magus buzz` script run inside a
// workspace arrives through, so checking only the workspace value would raise
// MGS1022 on a script that does have one to read.
func insightAnalyzer(ctx context.Context, member string) (types.InsightAnalyzer, error) {
	if a, ok := AnalyzerFromContext(ctx); ok {
		return a, nil
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, errNoWorkspace(member)
	}
	a, ok := ws.(types.InsightAnalyzer)
	if !ok {
		return nil, errors.New("insight: this workspace cannot analyze history")
	}
	return a, nil
}

// insightOptions decodes the window a Buzz caller asked for.
//
// An unknown key is an ERROR, not a default: dropping `{commits = 50}` would silently
// scan 500 commits and answer a different question than the one asked, with nothing
// to tell the author their typo did not take.
func insightOptions(opts map[string]any) (types.InsightOptions, error) {
	// The window the CLI defaulted to, so a report asked for with no options is the
	// report that command produced.
	out := types.InsightOptions{Commits: 500, Files: true}
	for k, v := range opts {
		switch k {
		case "commits":
			// A Buzz integer arrives as int64 (Value.AsInt) and a Buzz float as float64;
			// int is what a Go caller passes. Missing int64 rejected `{commits = 50}`,
			// which is the documented call.
			switch n := v.(type) {
			case int64:
				out.Commits = int(n)
			case float64:
				out.Commits = int(n)
			case int:
				out.Commits = n
			default:
				return types.InsightOptions{}, fmt.Errorf("insight: commits must be a number, got %T", v)
			}
		case "since":
			s, ok := v.(string)
			if !ok {
				return types.InsightOptions{}, fmt.Errorf("insight: since must be a string like \"90d\", got %T", v)
			}
			out.Since = s
		default:
			return types.InsightOptions{}, fmt.Errorf("insight: unknown option %q (want commits, since)", k)
		}
	}
	return out, nil
}

// buildInsightReport assembles the whole report from the six lenses.
//
// The four VCS lenses are required; volatility and unreferenced are best-effort: a
// history read that fails should omit a section rather than fail the whole report,
// and a workspace with no symbol index yields an empty list carrying an unknown
// verdict, which is a useful section rather than an error.
func buildInsightReport(ctx context.Context, a types.InsightAnalyzer, iopts types.InsightOptions) (types.InsightReport, error) {
	hot, err := a.Hotspots(ctx, iopts)
	if err != nil {
		return types.InsightReport{}, err
	}
	aff, err := a.Affinity(ctx, iopts)
	if err != nil {
		return types.InsightReport{}, err
	}
	own, err := a.Ownership(ctx, iopts)
	if err != nil {
		return types.InsightReport{}, err
	}
	tr, err := a.Trend(ctx, iopts)
	if err != nil {
		return types.InsightReport{}, err
	}
	report := types.InsightReport{Hotspots: hot, Affinity: aff, Ownership: own, Trend: tr}
	if vr, verr := a.Volatility(ctx); verr == nil {
		report.Volatility = vr
	}
	if ur, uerr := a.Unreferenced(ctx); uerr == nil {
		report.Unreferenced = ur
	}
	if dr, derr := a.Duplication(ctx); derr == nil {
		report.Duplication = dr
	}
	return report, nil
}

// workspaceCacheDir is the structural seam for the one capability
// types.WorkspaceRepository does not carry that opening a job Store needs: the
// workspace's cache directory, which the store adopts its pre-move rows from. Satisfied
// by the real *magus.Magus the same way std.Analyzer is, recovered by assertion so std
// and root magus name neither each other.
type workspaceCacheDir interface {
	CacheDir() string
}

// workspaceJobLimits is the magus.yaml jobs section, recovered the same way.
type workspaceJobLimits interface {
	JobLimits() config.Jobs
}

// jobStoreFromContext opens the job store for the workspace already on ctx. A
// fresh Store per call is deliberate and matches job.Store's own documented contract:
// it holds no state beyond its path and lock, and a cross-process race over the file is
// already accepted for v1 (see internal/job/store.go): a magusfile target is just
// another such process.
func jobStoreFromContext(ctx context.Context, member string) (*job.Store, error) {
	if job.HasSnapshot(ctx) {
		return nil, fmt.Errorf("magus\\%s: the job store is read-only here: a guard rule reads the rows the guard already read, through magus\\job.list, and cannot change them", member)
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		// NOT errNoWorkspace: that message ends by pointing at magus\describe/magus\cmd,
		// which fork a nested magus and rediscover the root. Neither stands in here, and
		// nor does `magus job`, which needs a workspace for the same reason this does.
		// Same code, because the constraint is the same one.
		return nil, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\%s: no workspace on the context: the job store is read from the workspace magus already has open, so this is callable from a magusfile target or a `magus buzz` script run INSIDE a workspace, not from a spell or a script outside one. Run from inside the workspace instead",
			member)
	}
	cd, ok := ws.(workspaceCacheDir)
	if !ok {
		return nil, fmt.Errorf("%s: this workspace has no cache directory", member)
	}
	loc := job.Location{CacheDir: cd.CacheDir(), Root: ws.Root()}
	// A Buzz process keeps the lease it was launched under in context. Do not
	// rediscover it from the mutable environment at each job-store write: a script
	// could otherwise unset BAGGAGE and become an apparent orchestrator mid-run. It is
	// the claim, so the checkout's record still outranks it.
	if claim := proc.LeaseFromContext(ctx); claim != "" {
		lease, _ := job.ActingLease(loc.CacheDir, claim)
		loc.Actor = &job.Actor{Lease: lease}
	}
	return job.NewStore(loc), nil
}

// MagusListJob backs magus\job.list (hand-bound in
// internal/interp/bindings/job_ns.go, since a Namespace method has no Impl for
// codegen to reflect a trampoline from; see std.Namespace). It answers with one typed
// report; types.NewJobList is the same constructor the console's
// JobService.ListJobs calls, so the doors cannot
// disagree about the rows or the overlaps derived from them.
//
// Inside a guard rule it answers from the rows the guard pinned, so the rule and the
// verdict it adds to read one store. That path leaves footprints unmeasured: it is the
// guard's hot path, and measuring reads the version control.
func MagusListJob(ctx context.Context) (types.JobList, error) {
	if snap, pinned := job.SnapshotFromContext(ctx); pinned {
		if snap.Err != nil {
			return types.JobList{}, snap.Err
		}
		return types.NewJobList(snap.Clone().Rows), nil
	}
	store, err := jobStoreFromContext(ctx, "job.list")
	if err != nil {
		return types.JobList{}, err
	}
	jobs, err := store.List()
	if err != nil {
		return types.JobList{}, err
	}
	// The same footprints and join `magus ls jobs` makes, the join from the snapshot
	// `magus queue ls` keeps; it never fetches.
	root := types.WorkspaceFromContext(ctx).Root()
	list := types.NewJobList(jobs)
	list.Overlaps = job.MeasureOverlaps(ctx, root, list.Jobs, list.Overlaps)
	return queue.JoinInflight(ctx, root, list, job.Identity{Lease: store.Actor().Lease})
}

// MagusPutJob backs magus\job.put. The field merge is decoded by
// internal/job.ParseMerge, the same decoder `magus job fork` calls, so
// a caller of either accepts the same fields and rejects the same mistakes.
func MagusPutJob(ctx context.Context, id string, opts map[string]any) (types.Job, error) {
	store, err := jobStoreFromContext(ctx, "job.put")
	if err != nil {
		return types.Job{}, err
	}
	merge, err := job.ParseMerge(opts)
	if err != nil {
		return types.Job{}, err
	}
	var limits config.Jobs
	if l, ok := types.WorkspaceFromContext(ctx).(workspaceJobLimits); ok {
		limits = l.JobLimits()
	}
	// No symbol reader: a magusfile target holds no loaded graph, so only the ambiguity
	// check is skipped here, and the gate still refuses to certify an unresolved name.
	return job.ForkMerge(ctx, store, strings.TrimSpace(id), merge, limits, nil)
}

// MagusRegisterJob backs magus\job.register: a worker reports the base it actually
// landed on, and learns how that compares with the checkpoint its job was handed. It
// returns the stored row and internal/job.BaseAdvice's reading of the verdict, the
// same pair `magus job exec` answers with.
//
// The verdict is a FACT, never a refusal: a diverged registration is recorded and
// reported like any other. See types.Job.
func MagusRegisterJob(ctx context.Context, id, base string) (types.Job, string, error) {
	store, err := jobStoreFromContext(ctx, "job.register")
	if err != nil {
		return types.Job{}, "", err
	}
	row, err := store.Exec(ctx, strings.TrimSpace(id), base)
	if err != nil {
		return types.Job{}, "", err
	}
	return row, job.BaseAdvice(row), nil
}

// MagusClearJob backs magus\job.clear: it reports how many rows it dropped rather than nothing, since a
// destructive op should say what it destroyed.
func MagusClearJob(ctx context.Context) (int, error) {
	store, err := jobStoreFromContext(ctx, "job.clear")
	if err != nil {
		return 0, err
	}
	return store.Clear(ctx)
}

// jobResultFromMap applies the exact same versioned, strict decoder a JSON result uses.
// Buzz's untyped map is deliberately treated as the JSON-shaped input contract rather
// than as a partial Go struct: an unknown field must be rejected here too, or a result
// an agent sends through a Buzz target could claim a field no verifier actually reads.
func jobResultFromMap(result map[string]any) (types.JobResult, error) {
	raw, err := json.Marshal(result)
	if err != nil {
		return types.JobResult{}, fmt.Errorf("magus\\job: encode result: %w", err)
	}
	return job.DecodeResult(strings.NewReader(string(raw)))
}

// jobAttemptFromContext reads the output descriptor from the workspace already on ctx.
// A job plan is repository-wide but output records are checkout-local, so Exit resolves
// the worker's record once and files its portable summary on the job for a later Wait.
func jobAttemptFromContext(ctx context.Context, ref string) (types.JobAttempt, error) {
	if strings.TrimSpace(ref) == "" {
		return types.JobAttempt{}, nil
	}
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.JobAttempt{}, types.DiagnosticErrorf(types.MagusfileOnlyMember,
			"magus\\job: no workspace on the context: output evidence is read from the workspace magus already has open")
	}
	cd, ok := ws.(workspaceCacheDir)
	if !ok {
		return types.JobAttempt{}, fmt.Errorf("magus\\job: this workspace has no cache directory")
	}
	desc, err := cache.NewOutputStore(cd.CacheDir()).DescriptorByRef(ref)
	switch {
	case err == nil:
		return types.JobAttempt{Found: true, Ref: ref, Project: desc.Project, Target: desc.Target, Spell: desc.Spell, Failed: desc.Failed, TimestampMs: desc.TimestampMs}, nil
	case errors.Is(err, fs.ErrNotExist):
		return types.JobAttempt{}, nil
	default:
		return types.JobAttempt{}, err
	}
}

// MagusExitJob backs magus\job.exit with a result map. The result map follows the
// versioned JSON result contract; see jobResultFromMap.
func MagusExitJob(ctx context.Context, id string, result map[string]any) (types.Job, error) {
	store, err := jobStoreFromContext(ctx, "job.exit")
	if err != nil {
		return types.Job{}, err
	}
	report, err := jobResultFromMap(result)
	if err != nil {
		return types.Job{}, err
	}
	return job.Exit(ctx, store, strings.TrimSpace(id), &report, jobAttemptFromContext)
}

// MagusAbandonJob backs magus\job.exit without a result.
func MagusAbandonJob(ctx context.Context, id string) (types.Job, error) {
	store, err := jobStoreFromContext(ctx, "job.exit")
	if err != nil {
		return types.Job{}, err
	}
	return job.Exit(ctx, store, strings.TrimSpace(id), nil, nil)
}

// MagusWaitJob backs magus\job.wait. A nil result selects the evidence Exit filed;
// otherwise the supplied JSON-shaped map is decoded and resolved locally.
func MagusWaitJob(ctx context.Context, id string, result map[string]any) (types.JobStatus, error) {
	store, err := jobStoreFromContext(ctx, "job.wait")
	if err != nil {
		return types.JobStatus{}, err
	}
	// No symbol reader on this path: the workspace on the context carries the PROJECT
	// graph, and a symbol gate needs the knowledge graph, which a magusfile has no
	// in-process handle on. A symbol gate verified from Buzz therefore REFUSES, naming
	// the graph it could not read, rather than passing on an observation nobody made.
	// The CLI and the MCP tool both hold one and wire it.
	observe := job.CheckpointObserver(types.WorkspaceFromContext(ctx).Root(), nil)
	if result == nil {
		return job.Wait(ctx, store, strings.TrimSpace(id), nil, nil, observe)
	}
	report, err := jobResultFromMap(result)
	if err != nil {
		return types.JobStatus{}, err
	}
	return job.Wait(ctx, store, strings.TrimSpace(id), &report, jobAttemptFromContext, observe)
}

// MagusDescribeFile classifies paths as generated output, declared source, or
// unclaimed. See runMagusJSON for why it forks rather than reading the workspace on
// the context: this answer is wanted precisely where there is no workspace loaded,
// a `magus buzz` script deciding whether a changed file is worth a human's attention.
func MagusDescribeFile(ctx context.Context, paths []string, opts map[string]any) (types.FileReport, error) {
	return runMagusJSON[types.FileReport](ctx, "describe", append([]string{"file"}, paths...), opts)
}

// MagusDescribeSpell implements magus\describe.spell: the spell inventory, or the
// one spell name selects. It forks for the reason MagusDescribeFile does, and
// returns the list rather than the SpellReport envelope because the envelope's
// other fields are a definition string and a count.
func MagusDescribeSpell(ctx context.Context, name string, opts map[string]any) ([]types.Spell, error) {
	report, err := runMagusJSON[types.SpellReport](ctx, "describe", describeArgs("spell", name), opts)
	return report.Spells, err
}

// MagusDiff implements magus\diff: the annotated changeset, in reading order.
//
// It shells out to `magus diff` rather than reimplementing the join, which is the whole
// point of exposing it here: a Buzz advisor writing a pull-request comment and the console
// then rank files by the SAME definition (types.Diff.SortForReading), and a change to
// that order reaches both without either being edited.
//
// --generated is passed so the caller receives every file and decides what to fold. A CI
// comment and a terminal reader want different things from the generated set, and a host
// module that pre-filtered would make the wider answer unreachable.
//
// opts.from reads a review an earlier `magus diff -o json` saved instead of computing one, so a
// set of advisors reading the same change computes it once.
func MagusDiff(ctx context.Context, opts map[string]any) (types.Diff, error) {
	if from, ok := opts["from"].(string); ok && from != "" {
		var saved types.Diff
		raw, err := os.ReadFile(from)
		if err != nil {
			return types.Diff{}, fmt.Errorf(`magus\diff: read opts.from: %w`, err)
		}
		if err := json.Unmarshal(raw, &saved); err != nil {
			return types.Diff{}, fmt.Errorf("magus\\diff: decode opts.from %s (expected `magus diff -o json` output): %w", from, err)
		}
		return saved, nil
	}
	args := []string{"--generated"}
	// Flags rather than opts to forward, because the nested magus is the one that knows how
	// to read a baseline and a range; these just name them for it.
	if baseline, ok := opts["baseline"].(string); ok && baseline != "" {
		args = append(args, "--baseline", baseline)
	}
	if rev, ok := opts["rev"].(string); ok && rev != "" {
		args = append(args, "--rev", rev)
	}
	// The patch rides stdin rather than a temp file, the way `magus diff --patch -` reads one.
	if patch, ok := opts["patch"].(string); ok && patch != "" {
		args = append(args, "--patch", "-")
		opts = maps.Clone(opts)
		delete(opts, "patch")
		opts["stdin"] = patch
	}
	for _, gate := range [][2]string{{"minCohort", "--conformance-min-cohort"}, {"minShare", "--conformance-min-share"}} {
		if v, ok := opts[gate[0]]; ok && v != nil {
			args = append(args, gate[1], fmt.Sprint(v))
		}
	}
	return runMagusJSON[types.Diff](ctx, "diff", args, opts)
}

// runMagusJSON runs a nested magus subcommand and decodes its report into T.
//
// It forces `-o json` and quiet: the caller is consuming the value, not watching the
// output, and letting a caller pass its own -o would hand back a shape T cannot decode.
// The alternative (returning ExecResult and making every caller run the bytes through
// jsonDecode) loses the type at the boundary, and annotating a decoded value back to a
// mirror does NOT restore it: that compiles and silently reads null for every field.
func runMagusJSON[T any](ctx context.Context, sub string, args []string, opts map[string]any) (T, error) {
	var out T
	quiet := map[string]any{"quiet": true}
	for k, v := range opts {
		if k == "quiet" {
			continue
		}
		quiet[k] = v
	}
	res, runErr := runMagusSub(ctx, sub, append(append([]string(nil), args...), "-o", "json"), quiet)
	// DECODE FIRST, exit status second. A report command exits non-zero precisely when
	// it has something to report (doctor exits 1 because a check failed), and raising
	// there would throw away the very payload the caller asked for. A report that parsed
	// IS the answer; the caller branches on it (summary.fail), which is strictly more
	// than an exit code carries. Only an unparsable answer is a failure to answer.
	if derr := json.Unmarshal([]byte(res.Stdout), &out); derr == nil {
		return out, nil
	}
	if runErr != nil {
		return out, runErr
	}
	return out, fmt.Errorf("magus.%s: decode report: %s", sub, res.Stderr)
}

// runMagusSub runs a nested magus invocation for subcommand sub: it prepends sub
// to args (so the subcommand name is fixed by the caller, not user-supplied) and
// hands off to runMagus.
func runMagusSub(ctx context.Context, sub string, args []string, opts map[string]any) (types.ExecResult, error) {
	return runMagus(ctx, sub, append([]string{sub}, args...), opts)
}

// resolveRunDir picks the directory a nested magus runs in: the contextual project dir,
// or opts.dir resolved RELATIVE to it, exactly as proc.exec's dir is, so the two spell the
// same idea the same way. An absolute opts.dir wins outright, and with no contextual dir
// there is nothing to resolve against, so it is used as given.
func resolveRunDir(ctx context.Context, opts map[string]any) string {
	dir, _ := CwdFromContext(ctx)
	sub, ok := opts["dir"].(string)
	if !ok || sub == "" {
		return dir
	}
	if filepath.IsAbs(sub) || dir == "" {
		return sub
	}
	return filepath.Join(dir, sub)
}

// runMagus runs a nested magus invocation with the full arg vector, yielding the
// caller's concurrency slot for the duration so the child can run. Output streams
// live and is captured: on success it returns the same {stdout, stderr, code, ok}
// object as proc.exec, so a magusfile can read a subcommand's output (e.g. `magus
// describe graph -o markdown` to generate MAGUS.md). It raises when the child can't
// launch or exits non-zero unless opts.allow_failure is true, mirroring proc.exec. label
// names the calling method for error messages.
//
// The child runs in the working directory carried by ctx (WithCwd) but loads the whole
// workspace, so a command scoped to one project names it. opts may carry "root",
// emitted as the global --root flag, which precedes the subcommand.
// nestedExecOptions is how a nested magus is launched: where it runs, what it inherits, and
// what it is fed.
//
// Extracted from runMagus so the option plumbing can be asserted without spawning anything.
// Each field answers one opt, spelled as proc.exec spells it:
//
//   - dir runs the child somewhere else, resolved RELATIVE to the contextual project dir, so
//     a nested magus reaching a sibling project needs no proc.exec("magus", ...).
//   - quiet captures the output without echoing it, for a command whose stdout is consumed.
//   - stdin feeds the child. A subcommand that reads a credential there (graph push) is the
//     case it exists for: an argument would put the token in a process listing and in every
//     run log. It was missing, so the magusfile that pipes a token to `graph push` had it
//     dropped on the floor and the publish failed on every push to main.
func nestedExecOptions(ctx context.Context, opts map[string]any, env []string) run.ExecOptions {
	quiet, _ := opts["quiet"].(bool)
	return run.ExecOptions{
		Dir:     resolveRunDir(ctx, opts),
		Env:     env,
		Capture: true,
		Quiet:   quiet,
		Stdin:   optStringDefault(opts, "stdin", ""),
	}
}

func runMagus(ctx context.Context, label string, args []string, opts map[string]any) (types.ExecResult, error) {
	self, err := os.Executable()
	if err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: executable: %w", label, err)
	}

	// Global flags (e.g. --root) precede the subcommand and its args.
	var full []string
	if root, ok := opts["root"].(string); ok && root != "" {
		full = append(full, "--root", root)
	}
	full = append(full, args...)

	// childEnv withholds server sockets from subprocesses, while a recursive Magus
	// call must retain both that trusted transport and its captured lease.
	env := recursiveMagusEnv(ctx)

	lim := cache.LimiterFromContext(ctx)
	var rec types.ExecResult
	var cmdErr error
	runFn := func() error {
		res, err := run.Exec(ctx, self, full, nestedExecOptions(ctx, opts, env))
		rec, cmdErr = nestedResult(label, full, res, err, opts)
		return nil
	}
	if err := proc.RunChildSync(ctx, lim, runFn); err != nil {
		return types.ExecResult{}, fmt.Errorf("magus.%s: %w", label, err)
	}
	return rec, cmdErr
}

// nestedResult turns a nested magus's exit into what the calling method returns.
// A non-zero exit raises unless opts.allow_failure is true, which returns the
// result instead, as proc.exec's runResult does; a sandbox denial always raises.
func nestedResult(label string, full []string, res run.ExecResult, err error, opts map[string]any) (types.ExecResult, error) {
	if err != nil && errors.Is(err, types.ExecDenied) {
		return types.ExecResult{}, err
	}
	var rec types.ExecResult
	if res.Started || optBool(opts, "allow_failure") {
		// Recorded even on a non-zero exit. A child that RAN said something, and
		// that output is the answer for a command whose failure IS its report:
		// doctor exits 1 because a check failed, and dropping stdout there left
		// nothing to decode. The error is still returned alongside, so a caller
		// that only wants the happy path is unaffected.
		rec = types.ExecResult{
			Stdout: strings.TrimSpace(res.Stdout),
			Stderr: strings.TrimSpace(res.Stderr),
			Code:   res.Code,
			OK:     res.Code == 0,
		}
	}
	if res.Code == 0 || optBool(opts, "allow_failure") {
		return rec, nil
	}
	if !res.Started {
		// The child never launched (binary not found, permission, ctx cancelled
		// before exec); surface the real cause, not a fabricated "code -1".
		return rec, fmt.Errorf("magus.%s: %s: %w", label, strings.Join(full, " "), err)
	}
	// The child's own diagnostic is not repeated here. It reaches the console
	// itself (printed by the child when it runs as its own process, and by
	// proc.Forward when it was adopted), so folding it into this message too
	// produced the same paragraph twice, once truncated into a `cause:` line.
	// Under opts.quiet nothing streams, and a caller that only reads the error
	// sees no ExecResult, so the child's stderr rides in the error or is lost.
	cmdErr := fmt.Errorf("magus.%s: %s exited with code %d", label, strings.Join(full, " "), res.Code)
	if quiet, _ := opts["quiet"].(bool); quiet {
		if msg := strings.TrimSpace(res.Stderr); msg != "" {
			cmdErr = fmt.Errorf("%w: %s", cmdErr, msg)
		}
	}
	return rec, cmdErr
}

// recursiveMagusEnv carries only the invocation facts a nested Magus process needs.
// It deliberately rebuilds BAGGAGE from the context instead of forwarding the mutable
// process value, so a script cannot shed its bound lease before calling magus\cmd.
func recursiveMagusEnv(ctx context.Context) []string {
	var env []string
	if lease := proc.LeaseFromContext(ctx); lease != "" {
		env = append(env, trail.EnvBaggage+"="+trail.BaggageLease+"="+lease)
	}
	for _, k := range run.ProcForwardVars {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
			slog.DebugContext(ctx, types.FormatDiagnostic(types.ProcSocketWithheld,
				"magus socket injected into recursive magus invocation"), "var", k)
		}
	}
	return env
}

// MagusDiagnoseDrift diagnoses a generate gate's drift into a coded diagnostic. Given the
// target's declared output globs and input globs (project-relative) and the fact that the
// tree drifted, it distinguishes the three causes the plan defines:
//
//   - outputs dirty AND a declared input is also dirty -> MGS4006 StaleGeneratedOutput:
//     a source input changed, so regeneration is expected; commit it.
//   - outputs dirty, inputs byte-identical, running a DEV build -> MGS4005 EnvironmentalDrift:
//     the committed form is produced by the pinned release (compat contract), so a dev
//     build's differing output is version/tool skew, not the developer's change.
//   - outputs dirty, inputs byte-identical, running a RELEASE build -> MGS4003
//     NondeterministicOutput: same inputs and generator version, yet output differs: a
//     reproducibility bug.
//
// It RETURNS the classification rather than throwing, so the gate owns the response:
// fail on a clean-tree drift, warn on a mid-edit dirty one. The record is a plain map:
//
//	{ drifted: bool, code: str, message: str, url: str, files: []str }
//
// drifted is false when the outputs are not actually dirty. files carries the backend's
// status lines, so a gate can say WHICH files moved without shelling out to the VCS.
//
// It composes vcs.isDirty rather than replacing it: isDirty stays the general primitive,
// and this is the drift-specific reading on top plus the version signal.
func MagusDiagnoseDrift(ctx context.Context, outputs, inputs []string) (types.DriftResult, error) {
	// Same keys as the drifted verdict, so a caller can read .files unconditionally
	// rather than discovering the key is absent only on the clean path.
	var clean types.DriftResult
	v, _ := resolveVCS(ctx)
	if v == nil {
		return clean, nil
	}
	dir, err := EffectiveCwd(ctx)
	if err != nil {
		dir = ""
	}
	// DirtyFiles, not Dirty: the verdict carries WHICH outputs drifted, and Dirty is
	// defined in terms of this anyway, so naming them costs nothing extra. A gate that
	// reports only "something drifted" sends its reader to reproduce the run just to
	// learn what a status call already knew, and a gate fires precisely when the
	// reader is looking at a CI log rather than the tree.
	dirtyFiles, err := v.DirtyFiles(ctx, dir, outputs)
	if err != nil {
		// Split from the !outDirty case below on purpose: they were one branch, so a
		// failed probe returned the same "clean" verdict as a genuinely clean tree. A
		// drift diagnosis that cannot read the tree has no verdict to give.
		return types.DriftResult{}, vcsFailed(err, "read %s status", v.Name())
	}
	if len(dirtyFiles) == 0 {
		return clean, nil
	}
	inDirty := false
	if len(inputs) > 0 {
		inDirty, _ = v.Dirty(ctx, dir, inputs)
	}

	// Shared with `magus vcs add`, which asks the same question at staging time: see
	// types.ClassifyDrift for why the fork lives there rather than here.
	code, msg := types.ClassifyDrift(inDirty, types.MagusVersionFromContext(ctx))
	root, err := v.Root(ctx, dir)
	if err != nil {
		root = dir
	}
	files := make([]types.Path, 0, len(dirtyFiles))
	for _, p := range dirtyFiles {
		files = append(files, types.Path{Value: p, Base: root})
	}
	return types.DriftResult{
		Drifted: true,
		Code:    string(code),
		Message: msg,
		URL:     types.CodeURL(code),
		Files:   files,
	}, nil
}
