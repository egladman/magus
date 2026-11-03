//go:build !wasm

package std

// magusObjects are the records the magus root constructors return, each
// method a generated forward to its Impl. Every method raises.
var magusObjects = []Object{
	{
		Name: "Workspace",
		Doc:  "The workspace a script reads and the checkout its job facts are about: magus\\workspace(dir).",
		Methods: []Method{
			{
				Name: "jobs",
				Doc: "Every row of the job store as one typed report: {jobs, overlaps}. jobs are in the order " +
					"they were declared; overlaps are derived on this read - every pair of live (non-terminal) " +
					"jobs whose declared write_paths intersect, the derivation the console's JobService.ListJobs " +
					"uses too. Read straight off the workspace already open on the context - no subprocess. " +
					"Inside a magus\\guard.spawn, command, write or stop rule it answers from the rows the guard " +
					"read for that call, and every other job member raises there: the store is read-only to a rule.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "JobList"}},
				Raises:  true,
				Impl:    MagusWorkspaceJobs,
			},
			{
				Name: "putJob",
				Doc: "Record or advance one row, merging only the fields opts names: parent, criteria, checkpoint, " +
					"write_paths, deny_paths, read_paths, depends_on, model, check (`<target> <project> [-- args]`), " +
					"state (declared, running, exited, pass, fail, no_return), read_only, max_attempts. A key opts " +
					"omits is left untouched, so a later put advances a row without erasing what an earlier put " +
					"declared; a key present with an empty value is an explicit clear. id is the row's identity to " +
					"upsert on, the value an orchestrator also puts in the worker's prompt. Store-stamped fields " +
					"cannot be set here. A put is validated whole before anything is stored, and raises every " +
					"problem at once (MGS3037 through MGS3046), each with its field and the fix. The record's " +
					"schema is what `magus job fork --schema` prints. A put that CREATES a row raises when its " +
					"write_paths cover a file the workspace has to load while another live job with write paths is " +
					"bound to this checkout. Returns the stored row.",
				Args: []Arg{
					{Name: "id", Type: TypeString},
					{Name: "opts", Type: TypeAnyMap, Optional: true},
				},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Job"}},
				Raises:  true,
				Impl:    MagusWorkspacePutJob,
			},
			{
				Name: "clearJobs",
				Doc: "Drop every row, which is how a fresh plan starts. Returns how many rows it dropped; an " +
					"empty store clears 0, which is not an error. Clearing is also how one orchestrator can " +
					"silently erase another's plan, so a caller unsure whether it owns the whole store reads " +
					"jobs() first.",
				Returns: []Ret{{Type: TypeInt}},
				Raises:  true,
				Impl:    MagusWorkspaceClearJobs,
			},
			{
				Name: "forkFacts",
				Doc: "What a fork at this Workspace's dir leaves waiting: finishedBranches, branchesUnknown, " +
					"integrateRecord, commitsSinceTest, lastTestRef, lastTestRevision and testUnknown. Facts only: " +
					"repo policy decides what they stop. It reads the version control per finished row, so call " +
					"it for a fork line, not on every tool call.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "JobForkFacts"}},
				Raises:  true,
				Impl:    MagusWorkspaceForkFacts,
			},
			{
				Name: "unmerged",
				Doc: "What the branch checked out at this Workspace's dir does not hold, for the job trees forked " +
					"from it: filed or ended work off the branch, uncommitted paths in a filed row's checkout, and " +
					"follow_ups no row carries and no reason dismisses. risks lists unanswered unresolved_risks " +
					"apart. Facts only: repo policy decides what they stop. An unknown branch fills unknown and an " +
					"unreadable head is a row, so neither reads as merged. It reads the version control per row, " +
					"so call it from a stop rule, not on every tool call.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "JobUnmergedReport"}},
				Raises:  true,
				Impl:    MagusWorkspaceUnmerged,
			},
			{
				Name:    "tierOutcomes",
				Doc:     "Recorded outcomes per (model tier, job kind): {definition, rows}. Annotate `> ModelTiers`. Workspace-wide; takes no options. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "ModelTiers"}},
				Raises:  true,
				Impl:    MagusWorkspaceTierOutcomes,
			},
			{
				Name:    "grouping",
				Doc:     "How jobs partitioned work: {definition, rows}. Annotate `> Grouping`. Workspace-wide; takes no options. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Grouping"}},
				Raises:  true,
				Impl:    MagusWorkspaceGrouping,
			},
			{
				Name:    "volatility",
				Doc:     "Run-outcome flakiness: {definition, threshold, targets}. Annotate `> Volatility`. Workspace-wide; takes no options. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Volatility"}},
				Raises:  true,
				Impl:    MagusWorkspaceVolatility,
			},
			{
				Name:    "skills",
				Doc:     "Every skill this workspace offers an agent, sorted by name then form: each {name, description, source, form, body, current}. source is `shipped` for magus's own catalog, which yields a `short` and a `full` entry per skill, or `local` for a hand-authored skill found in an installed skills directory (a wired harness's skill paths, where `magus agent install` writes), which yields one entry with form `full`. body is exactly what an agent loads: the SKILL.md text with its frontmatter excluded, the generated footer kept. current is true when every installed copy of that entry is byte-equal to what this magus would install, false when one differs or none is installed, and always true for a local skill. opts.name selects one skill and raises on a name nothing offers, naming the near matches; opts.form (`short`, `full`, or `both`, the default) narrows the shipped entries and leaves local ones alone. An unknown option raises. Pair it with magus\\workspace().putJob and magus\\cmd(\"describe\", [\"job\", id]) to hand a worker its brief and its skills from magus itself rather than from pasted files. Read from the workspace on the context; raises MGS1022 in a script run outside one.",
				Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "SkillsOptions"}},
				Returns: []Ret{{Type: TypeAny, Object: "[Skill]"}},
				Raises:  true,
				Impl:    MagusWorkspaceSkills,
			},
			{
				Name:    "attention",
				Doc:     "List the OPEN attention requests of this repository's session store: {requests, store}, each request {id, outcome, source, where, lease, message, ...} as `magus session attention -o json` reports them. Read-only by design: a magusfile may refuse to proceed while a request is open, but disposing one is a human act (see docs/design/judgment/manual-on-purpose.md), so no method here closes anything - the person runs `magus session dispose <id> -reason <text>`. Runs a nested magus, so it works from a `magus buzz` script as well as a magusfile; opts.root and opts.dir as on doctor. Raises only when the subprocess cannot run or its output cannot decode.",
				Args:    []Arg{{Name: "args", Type: TypeStringSlice}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "CommandOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "AttentionList"}},
				Raises:  true,
				Impl:    MagusWorkspaceAttention,
			},
			{
				Name:   "fileDiskAttention",
				Doc:    "Open the attention request a disk reclaim files for a person, with message as its text. It keeps one id, so a request still open is not filed again. Disposing it stays a person's act. Raises MGS1022 outside a workspace.",
				Args:   []Arg{{Name: "message", Type: TypeString}},
				Raises: true,
				Impl:   MagusWorkspaceFileDiskAttention,
			},
			{
				Name:    "diskReclaim",
				Doc:     "What a disk reclaim reads off this workspace: {caches, listCommand}, caches each {spell, env, dir, trimMb} for a cache a resolved spell gives a trim budget, and listCommand the script serving event:disk-low, empty when none does. Annotate the result `> DiskReclaim`. Read in-process from the workspace on the context; raises MGS1022 outside one, and raises when a budgeted cache locates no directory.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "DiskReclaim"}},
				Raises:  true,
				Impl:    MagusWorkspaceDiskReclaim,
			},
			{
				Name:   "bustCache",
				Doc:    "Invalidate the build cache. Escape hatch - prefer modeling missing inputs as Sources. No arg clears all; a project path clears one project.",
				Args:   []Arg{{Name: "projectPath", Type: TypeString, Optional: true}},
				Raises: true,
				Impl:   MagusWorkspaceBustCache,
			},
			{
				Name:    "diagnoseDrift",
				Doc:     "Diagnose why a generate gate's declared outputs drifted and RETURN the verdict {drifted, code, message, url, files} so the caller decides whether to fail or warn. Pass the target's output globs and (optional) input globs, project-relative. code is MGS4006 when a declared input changed (real drift, commit it), MGS4005 when the inputs are unchanged but a dev build produced differing output (version/tool skew, not your change), or MGS4003 when a release build's identical inputs still differ (a reproducibility bug). files are the drifted outputs as Paths based at the repository root. drifted is false with every field zero when the outputs are clean. It lives here rather than on vcs because choosing between those codes is magus policy; vcs only supplies the probe. Composes vcs\\status; does not replace it.",
				Args:    []Arg{{Name: "outputs", Type: TypeStringSlice}, {Name: "inputs", Type: TypeStringSlice, Optional: true}},
				Returns: []Ret{{Type: TypeAny, Object: "DriftResult"}},
				Raises:  true,
				Impl:    MagusWorkspaceDiagnoseDrift,
			},
		},
	},
	{
		Name: "Graph",
		Doc:  "The symbol index the knowledge graph answers from: magus\\graph(). Each method raises once the index moved since the Graph was built, naming the projects whose index came or went.",
		Methods: []Method{
			{
				Name:    "query",
				Doc:     "Search the knowledge graph: {definition, schemaVersion, query, budget, matchCount, offset, matches, nodes, links, answer}. Annotate the result `> QueryResult`. query is free text plus field matchers (kind=spell, project=pkg/foo, relation=uses, kind!=op, id=~build$). A query that seeds code symbols (kind=symbol) reads the symbol shards too; every other query reads the domain graph. opts.budget caps the neighborhood (default 50); opts.limit and opts.offset window the matches while matchCount stays the total. Read answer.verdict before trusting zero matches: `unknown` means part of the workspace had no symbol index. An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "query", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "QueryOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "QueryResult"}},
				Raises:  true,
				Impl:    MagusGraphQuery,
			},
			{
				Name:    "explain",
				Doc:     "One knowledge-graph node's context card: {definition, schemaVersion, node, blastRadius, out, in, docsURL, resolution}. Annotate the result `> ExplainResult`. node is a node ID (target:pkg/foo:build), a workspace path (internal/httpx), or a name that resolves to one. A path resolves to its dir or file node exactly before any ranked match; resolution says which happened (id, path or fuzzy), and a caller anchoring by path refuses fuzzy. A path between two nodes is magus\\graph().path. Raises when node resolves to nothing, naming magus\\graph().refs when the name could be a code symbol this graph does not load. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "node", Type: TypeString}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "ExplainResult"}},
				Raises:  true,
				Impl:    MagusGraphExplain,
			},
			{
				Name:    "path",
				Doc:     "The shortest chain of edges between two knowledge-graph nodes: {definition, schemaVersion, from, to, found, steps}. Annotate the result `> PathResult`. Edges are walked in both directions. opts.relations limits the hops to those relations, so found false under it means no path of those relations. A resolved pair with no connection returns found false; an endpoint that resolves to nothing, an unknown option, and an unknown relation raise. The endpoints are `node` and `to` rather than from and to because `from` is a reserved Buzz word. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "node", Type: TypeString}, {Name: "to", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "PathOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "PathResult"}},
				Raises:  true,
				Impl:    MagusGraphPath,
			},
			{
				Name:    "refs",
				Doc:     "Where a code symbol is defined and every file that references it: {definition, schemaVersion, symbol, label, fileCount, refCount, defs, refs, answer}. Annotate the result `> RefsResult`. symbol is a symbol node ID or a name that resolves to one, drawn from the workspace's declared SCIP indexes. A symbol nothing defines is an answer, not a raise: answer.verdict says whether that is a verified absence or a blind spot. opts.limit and opts.offset window refs while fileCount and refCount stay the totals. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "symbol", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "RefsOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "RefsResult"}},
				Raises:  true,
				Impl:    MagusGraphRefs,
			},
			{
				Name:    "stats",
				Doc:     "The knowledge graph's shape: {definition, nodeCount, edgeCount, gods, orphans, coverage, isolatedCount, componentCount, largestComponentSize}. Annotate the result `> KnowledgeStats`. gods are the most connected nodes, where structural risk concentrates; orphans are docs that document nothing and spells no target uses. kind scopes every section to one node kind (spell, target, doc, ...); omit it for the whole graph. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "kind", Type: TypeString, Optional: true}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "KnowledgeStats"}},
				Raises:  true,
				Impl:    MagusGraphStats,
			},
			{
				Name:    "neighborhood",
				Doc:     "The knowledge subgraph around one focus node: {definition, schemaVersion, focus, resolution, options, nodes, links, folds, answer}. Annotate the result `> NeighborhoodResult`. focus is a node ID, a workspace path, or a name; resolution says how it was reached. opts is a NeighborhoodOptions: depth is the most hops (0 means 1); relations are the only relations walked; direction is out, in, or empty for both; collapse folds every source node under each workspace path prefix into that prefix's dir node, longest prefix winning, and folds lists what each absorbed. Read answer.verdict before trusting a thin result: an imports walk with no symbol index is unknown, not absent. An unknown option, relation or direction raises, as does a focus that resolves to nothing. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "focus", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "NeighborhoodOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "NeighborhoodResult"}},
				Raises:  true,
				Impl:    MagusGraphNeighborhood,
			},
			{
				Name:    "dir",
				Doc:     "One workspace directory as the knowledge graph holds it: {path, id, layer, language, imports, importedBy, importsIndexed, calls, calledBy, children, files}. Annotate the result `> Dir`. path is workspace-relative (internal/httpx). imports and importedBy are the package directories it imports and that import it; importsIndexed false means no symbol index read this directory, so empty lists there say nothing. calls and calledBy are DirCall records, one per `magus:calls` marker, with the transport it declares. layer is what magus\\project's \"layers\" declares for it. Raises MGS7005 when the graph holds no dir node for path, naming the nearest one when a typo is likely, so a figure never draws a box for a directory that is not there. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "path", Type: TypeString}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Dir"}},
				Raises:  true,
				Impl:    MagusGraphDir,
			},
			{
				Name:    "dirs",
				Doc:     "Every directory whose workspace path matches glob, as Dir records sorted by path. Annotate the result `> [Dir]`. glob is a doublestar pattern (internal/**). opts is a DirsOptions: layer keeps one declared layer, and a layer nothing declares raises MGS7006 rather than matching nothing; language keeps one package language; depth bounds how many segments below the glob's literal prefix a match may sit (0 is unbounded). An unknown option raises. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "glob", Type: TypeString}, {Name: "opts", Type: TypeAnyMap, Optional: true, Object: "DirsOptions"}},
				Returns: []Ret{{Type: TypeAny, Object: "[Dir]"}},
				Raises:  true,
				Impl:    MagusGraphDirs,
			},
			{
				Name:    "layer",
				Doc:     "One layer magus\\project's \"layers\" key declares: {name, declared, dirs}. Annotate the result `> Layer`. declared are the directories and globs declared for it; dirs are the Dir records it covers. Raises MGS7006 on a name no declaration uses, listing the declared ones. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Args:    []Arg{{Name: "name", Type: TypeString}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Layer"}},
				Raises:  true,
				Impl:    MagusGraphLayer,
			},
			{
				Name:    "deps",
				Doc:     "The project dependency DAG as {nodes, dependsOn, blastRadius}. nodes is in TOPOLOGICAL order, so iterating it is already a valid build order; dependsOn gives each node's direct predecessors and blastRadius how many projects it can transitively affect. Served in-process from the workspace on the context - no subprocess.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Deps"}},
				Raises:  true,
				Impl:    MagusGraphDeps,
			},
			{
				Name:    "imports",
				Doc:     "The workspace's package import graph: {indexed, packages}, packages mapping each package directory (\".\" for the root) to the sorted package directories it imports, read off the workspace's declared SCIP indexes. Annotate the result `> ImportGraph`. Test files, packages outside the workspace, and self-imports are left out. indexed is false when no symbol index was ingested, and packages is then empty because nobody looked, not because nothing imports anything: a caller checking drift refuses on it rather than reporting none. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "ImportGraph"}},
				Raises:  true,
				Impl:    MagusGraphImports,
			},
			{
				Name:    "symbols",
				Doc:     "Every non-test, non-generated symbol's declaration and doc comment in the workspace's merged symbol indexes, whatever language indexed them, as data: {symbols, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each symbol is {node, source, language, name, kind, owner, doc}: source is the declaration's path and line, since an index records no position inside a doc; kind is function, method, type, interface, struct or value, or empty when the naming index read no shape; owner is the enclosing type of a member; doc is the whole comment with its lines kept. Nothing here judges the text: a magusfile rule reads it and decides. indexes is each declared symbol index as `magus status` judges it, judged just before the graph is read, as magus\\graph().precedents reports it: an index not `up-to-date` gave the symbols nothing or something old, so a gate on them checks indexes first. Test files and declared outputs are never listed. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Symbols"}},
				Raises:  true,
				Impl:    MagusGraphSymbols,
			},
			{
				Name:    "precedents",
				Doc:     "The precedents the workspace's merged symbol indexes establish, the rows `Graph.Precedents` mines: {precedents, indexes}, keyed as JSON is, with no Buzz object mirroring it. Each precedent is {family, scope, key, follow, cohort, share, established, cited, departures}: follow of cohort cases share one shape, established when the cohort and share clear the conformance gate, and departures are the cases that do not. indexes is each declared symbol index as `magus status` judges it ({project, op, language, freshness, detail}), judged just before the graph is read: an index not `up-to-date` gave the rows nothing or something old, so a gate on the rows checks indexes first and fails rather than reading silence as agreement. Declared outputs are never counted. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Holds"}},
				Raises:  true,
				Impl:    MagusGraphPrecedents,
			},
			{
				Name:    "unreferenced",
				Doc:     "Symbols nothing in the workspace names: {definition, symbols, answer}. Annotate `> Unreferenced`. Workspace-wide SCIP read; takes no options. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Unreferenced"}},
				Raises:  true,
				Impl:    MagusGraphUnreferenced,
			},
			{
				Name:    "duplication",
				Doc:     "Functions that call the same workspace symbols in the same proportions: {definition, groups, answer}. Annotate `> Duplication`. Workspace-wide SCIP read; takes no options. Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Duplication"}},
				Raises:  true,
				Impl:    MagusGraphDuplication,
			},
		},
	},
	{
		Name: "Change",
		Doc:  "A change against a base and the tree it was built on: magus\\change(base, opts). Each method raises once the tree moved since the Change was built, naming the paths that moved.",
		Methods: []Method{
			{
				Name: "tier",
				Doc: "Tier the change against target's gate: {base, target, tier, affected, evidence, gate}, tier one " +
					"of trivial, mechanical, scoped or full, and anything magus cannot prove is full. The base is " +
					"the Change's; opts.paths replaces the version-control diff, and opts.checkout assesses another " +
					"checkout of the repository, such as a job's, loaded for the one call. Read-only: nothing runs " +
					"but `go list` in the Go modules the change touches. A change that cannot be read raises.",
				Args: []Arg{
					{Name: "target", Type: TypeString},
					{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "TierOptions"},
				},
				Returns: []Ret{{Type: TypeAnyMap, Object: "TierReport"}},
				Raises:  true,
				Impl:    MagusChangeTier,
			},
			{
				Name:    "diff",
				Doc:     "Read the working tree's uncommitted changes, annotated and ordered by what they can break: for each file the owning project, whether it is a declared `output` (generated - the source edit is the review), how widely its changed symbols are referenced (`reach`), whether another project can see them (`visibility`), observed `coverage`, how often it has been changing (`churn`), and which agent sessions wrote it (`touches`). Files come back in the order magus recommends READING them - generated last whatever its reach, then widest reach first - so a caller renders the list as given rather than sorting it again. Each file also lists its `hunks`, and `order` is every hunk arranged for reading: `groups` of `steps`, a definition before its uses, an interface before its implementations, code before its tests, each hunk carrying the `why` that placed it, and `count` proving every hunk is placed once. `files` orders whole files and `order` orders their hunks; the two are independent, and `order` can reach one file's hunks in separate steps. `order` is absent, with a `notes` entry naming the rebuild, when the symbol index is not current, a touched project has no index built, or the patch could not be read. Returns a typed Diff envelope; branch on `role` and `visibility` rather than grepping text. opts.rev reviews a committed range written base...head instead of the working tree, which is what a caller running where the tree is clean (a CI checkout) has to pass to see anything at all. opts.patch reviews a unified diff given as text instead, the way `magus diff --patch -` reads one: a pull request's patch, for files that may not match the tree. opts.baseline is a `magus graph export --symbols -o json` of the base: with it every changed symbol carries what the change did to it (`change` is added, removed, signature, or body) and `api` carries the semver bump that proves, a floor and never a ceiling. Each symbol the change adds, renames or re-signs carries `checks`: what the conformance checks found against how the rest of the workspace declares the same kind of thing, with opts.minCohort and opts.minShare as their silence gates (default 5 and 0.8). opts.from reads a review an earlier `magus diff -o json` saved instead of computing it again, so several readers of one change pay for one diff. Runs a nested magus, so it needs no workspace on the context and works from a `magus buzz` script.",
				Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "DiffOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Diff"}},
				Raises:  true,
				Impl:    MagusChangeDiff,
			},
			{
				Name:    "impact",
				Doc:     "The blast radius of a changeset: {base, changedFileCount, changedFiles, seedProjects, affectedProjects, notes}. Each affected project carries whether it was a seed and, for a seed, the changed files in it. Annotate the result `> Impact`. This is `magus affected --impact`: the report, with no target run. opts is a CommandOptions: root and dir as on doctor.",
				Args:    []Arg{{Name: "opts", Type: TypeAnyMap, Optional: true, Object: "CommandOptions"}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Impact"}},
				Raises:  true,
				Impl:    MagusChangeImpact,
			},
			{
				Name:    "affected",
				Doc:     "Compute the VCS-affected project set against base (empty uses the configured base ref): {base, changed, seed, filesBySeed, affected}. Served in-process from the workspace on the context - no subprocess. Raises when the diff cannot be computed, rather than reporting an empty set, since an empty set and an uncomputable one mean opposite things to a caller deciding what to build.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Affected"}},
				Raises:  true,
				Impl:    MagusChangeAffected,
			},
			{
				Name:    "conformance",
				Doc:     "The conformance lens as data: {definition, rows, excluded, answer, indexes, base, ceilings, unmeasured}, keyed as JSON is. Each row is {key, n, p50, p90, p95, p99, live, value, propose, violations}: key names a metric (file-lines, func-lines, type-methods, file-symbols, dep-fanout, clone-tokens, export-scope, call-sequence, shared-body, shared-reach, caller-spread, mode-knobs, alias-share, name-clash), a population (source, main, test) and a scope; value is the anchor knowledge.conformance configures; live is the configured percentile today; violations are the files, symbols, packages or clone groups past value, each {id, source, measure}. propose is zero, or a tighter anchor a person may configure; it is never looser than value, and nothing enforces live. checks and base hold a change to a conformance ceiling: base is a revision, and ceilings are the files changed since it whose count of violations of a metric in checks rose past their count there, each {key, file, base, head, violations}; a drop is progress, and unmeasured lists each touched file only an external indexer reads, {file, language, reason}, which holds no ceiling. No count is stored in the workspace: it reads the base's measure magus\\change().mergeBase kept in the user cache, and raises when none is kept or the base has no checks. excluded counts the files knowledge.conformance.exclude hides. answer is unknown, never a pass, when a symbol index is missing. indexes is each declared symbol index as `magus status` judges it, judged just before the graph is read, as magus\\graph().precedents reports it: an index not `up-to-date` gave the rows something old, so a gate on them checks indexes first. Declared outputs are never counted. Read in-process from the workspace on the context; raises MGS1022 outside one, and raises on a policy it cannot honor or a base it cannot measure whole.",
				Args:    []Arg{{Name: "checks", Type: TypeStringSlice, Optional: true}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "ConformanceReport"}},
				Raises:  true,
				Impl:    MagusChangeConformance,
			},
			{
				Name:    "mergeBase",
				Doc:     "Measures the merge base of base and HEAD for magus\\change().conformance's ceilings and returns the commit. It exports the base and runs magus's in-process Go, Buzz and shell indexers over it with this workspace's configuration, as a library call that opens no workspace and starts no run, then keeps the conformance lens in the user cache per commit, policy and indexer versions, which every checkout on the machine shares; a kept measure is reused. A language only an external indexer reads gets no base measure. Raises MGS1022 outside a workspace, and raises when the base cannot be resolved or an in-process indexer cannot read one of its sources.",
				Returns: []Ret{{Type: TypeString}},
				Raises:  true,
				Impl:    MagusChangeMergeBase,
			},
		},
	},
	{
		Name: "History",
		Doc:  "The window of version-control history the metrics read: magus\\history(opts).",
		Methods: []Method{
			{
				Name:    "hotspots",
				Doc:     "Churn × complexity ranking: {definition, commits, since, nodes, files}. Annotate `> Hotspots`. Project nodes by default; files always included (edit frequency weighted by complexity). Takes HistoryMetricsOptions (commits, default 500; since; dir). Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Hotspots"}},
				Raises:  true,
				Impl:    MagusHistoryHotspots,
			},
			{
				Name:    "affinity",
				Doc:     "Projects that change together (temporal coupling): {definition, commits, since, pairs}. Annotate `> Affinity`. Hidden pairs co-change with no declared dependency. Takes HistoryMetricsOptions (commits, default 500; since; dir). Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Affinity"}},
				Raises:  true,
				Impl:    MagusHistoryAffinity,
			},
			{
				Name:    "ownership",
				Doc:     "Author concentration per project: {definition, commits, since, projects}. Annotate `> Ownership`. Primary author share, bus-factor-1, and stale (quiet in the recent half). Takes HistoryMetricsOptions (commits, default 500; since; dir). Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Ownership"}},
				Raises:  true,
				Impl:    MagusHistoryOwnership,
			},
			{
				Name:    "trend",
				Doc:     "Rising vs cooling projects across the window's halves: {definition, commits, since, projects}. Annotate `> Trend`. Takes HistoryMetricsOptions (commits, default 500; since; dir). Read in-process from the workspace on the context; raises MGS1022 outside one.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "Trend"}},
				Raises:  true,
				Impl:    MagusHistoryTrend,
			},
		},
	},
	{
		Name: "Job",
		Doc:  "One row of the job store: magus\\job(id). Its methods act on the row by id.",
		Methods: []Method{
			{
				Name: "exit",
				Doc: "File a worker's result and the recorded attempt behind its output_ref, then move the job to " +
					"exited. With no result it records no_return instead: the worker returned no evidence, which " +
					"is distinct from a result that fails verification. A result is decoded with the strict, " +
					"versioned contract `magus job exit --stdin` uses, keys in its JSON spelling (schema_version, " +
					"changed_paths, output_ref). The output ref is resolved from this checkout before it is filed. " +
					"Returns the stored row.",
				Args:    []Arg{{Name: "result", Type: TypeAnyMap, Optional: true}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "Job"}},
				Raises:  true,
				Impl:    MagusJobExit,
			},
			{
				Name: "wait",
				Doc: "Verify a returned result against the job's current terms and record pass only when every " +
					"check holds. A rejected result returns JobStatus with every violation and leaves the row's " +
					"state unchanged; it does not raise. With no result it uses the result and attempt exit filed. " +
					"A holder cannot wait on its own job.",
				Args:    []Arg{{Name: "result", Type: TypeAnyMap, Optional: true}},
				Returns: []Ret{{Type: TypeAnyMap, Object: "JobStatus"}},
				Raises:  true,
				Impl:    MagusJobWait,
			},
			{
				Name: "register",
				Doc: "Take this job in this checkout: record the base it is on, read from the checkout's version " +
					"control as `magus vcs checkpoint -o name` prints it, and learn how it compares with the " +
					"checkpoint the job was handed. Returns {job, advice}: the stored row, which carries " +
					"reported_base, registered and base_verdict (match, revision-match, diverged or unknown), and " +
					"a sentence naming both revisions and what to do next. The verdict is a fact, never a refusal. " +
					"A checkout that reports no revision raises.",
				Returns: []Ret{{Type: TypeAnyMap, Object: "JobRegistration"}},
				Raises:  true,
				Impl:    MagusJobRegister,
			},
		},
	},
	{
		Name: "Session",
		Doc:  "Which session guard record a read covers, and inside which window: magus\\session(opts).",
		Methods: []Method{
			{
				Name: "trail",
				Doc: "The session's guard record inside its window: {session, host, since, until, checkouts, " +
					"observations, spawns}. An empty session is the one with the newest observation in the window. " +
					"Reads the trail of every checkout of this repository that changed inside the window, because a " +
					"session's hooks record into the checkout they ran from. An unreadable time raises.",
				Returns: []Ret{{Type: TypeAny, Object: "Trail"}},
				Raises:  true,
				Impl:    MagusSessionTrail,
			},
			{
				Name: "marks",
				Doc: "Every verdict a person recorded on a trail row in this repository, oldest first; a later mark " +
					"on an id supersedes an earlier one. Kept per repository identity, so every checkout and every " +
					"session reads the same marks. Raises on a store line that does not decode.",
				Returns: []Ret{{Type: TypeAny, Object: "[TrailMark]"}},
				Raises:  true,
				Impl:    MagusSessionMarks,
			},
			{
				Name: "mark",
				Doc: "Record a person's verdict on one trail row and return it as stored, its time stamped. id is the " +
					"row's stable id (fb and 12 hex digits), section one of refused, advised, unguarded, " +
					"next-not-taken, verdict one of should-deny, should-advise, wrong-deny, fine; key names the rule " +
					"or shape the row groups by. Appends to the per-repository store and rewrites nothing. Raises on " +
					"a malformed mark or an unwritable store.",
				Args:    []Arg{{Name: "mark", Type: TypeAnyMap, Object: "TrailMark"}},
				Returns: []Ret{{Type: TypeAny, Object: "TrailMark"}},
				Raises:  true,
				Impl:    MagusSessionMark,
			},
		},
	},
}
