---
title: magus command
generated_from: internal/commands/registry.go
description: Standalone build orchestrator and content-addressed cache for polyglot monorepos, with workspace-aware subcommands for build, test, lint, and inspect.
tags: [cli, magus, build, monorepo, orchestrator, cache, workspace]
---

# magus

magus - workspace-aware build orchestrator and content-addressed cache

## Synopsis

**magus** [flags] \<subcommand\> [args]

## Description

magus is a standalone build orchestrator and content-addressed cache for
multi-language monorepos, and an evolution of Mage. It provides workspace-aware
subcommands for building, testing, linting, and inspecting projects without
requiring Mage to be installed.

magus reads optional configuration from magus.yaml (XDG, workspace root, or
CWD) and MAGUS_\* environment variables. All configuration can be overridden
with CLI flags.

Global flags are accepted by every subcommand and may appear before or
after the subcommand word. Last-write-wins, matching kubectl conventions.

## Global Flags

**--concurrency** *N*
: Run at most N build steps at once. 0 means use the configured value (or MAGUS_CONCURRENCY, or concurrency_profile, balanced (min(NumCPU,8)) by default).

**--config** *path*
: Config file path. Default: search for magus.yaml in CWD, workspace root, and $XDG_CONFIG_HOME/magus/. Must precede the subcommand.

**-o** *format*
: Short for --output; takes the same format

**--output** *format*
: Output format: text (default), json, yaml, name, jsonl, or template[=\<go-template\>]. Honored by subcommands that emit structured data. A template body renders a Go text/template over the same value -o json emits (field names are the json keys); a bare -o template with no body lists that output's fields instead of rendering - the json keys usable in -o json and -o template, with each field's type and doc.

**--root** *path*
: Workspace root path. Default: walk up from cwd until go.mod is found. Must precede the subcommand.

**-v**
: Increase log verbosity. Repeat for more detail (-v, -vv, -vvv).

## Subcommands

**ls**
: List all discovered projects. See [**magus-ls**(1)](magus-ls.md).

**describe**
: Define a magus concept and list its entities. See [**magus-describe**(1)](magus-describe.md).

**where**
: Print the absolute path of a project. See [**magus-where**(1)](magus-where.md).

**run**
: Run a target for selected projects. See [**magus-run**(1)](magus-run.md).

**affected**
: Run a target for VCS-diff affected projects. See [**magus-affected**(1)](magus-affected.md).

**x**
: Reproduce an output ref, or pick project + target. See [**magus-x**(1)](magus-x.md).

**clean**
: Remove declared outputs (regenerable, never sources). See [**magus-clean**(1)](magus-clean.md).

**shell**
: Check a command against this workspace's conventions before running it. See [**magus-shell**(1)](magus-shell.md).

**query**
: Search the knowledge graph, and retrieve a run's output or journal by id. See [**magus-query**(1)](magus-query.md).

**refs**
: List where an ingested code symbol is defined and referenced. See [**magus-refs**(1)](magus-refs.md).

**explain**
: Show one knowledge-graph node's context: data, edges, and reach. See [**magus-explain**(1)](magus-explain.md).

**path**
: Connect two knowledge-graph nodes: the shortest chain of edges between them. See [**magus-path**(1)](magus-path.md).

**graph**
: The workspace's graphs as objects: deps, export, stats. See [**magus-graph**(1)](magus-graph.md).

**diff**
: Read the working tree's changes in the order they deserve attention. See [**magus-diff**(1)](magus-diff.md).

**vcs**
: Staging and conflict resolution that knows what is generated. See [**magus-vcs**(1)](magus-vcs.md).

**session**
: What magus invocations did and what agents are blocked on: humans read and dispose, hosts write. See [**magus-session**(1)](magus-session.md).

**job**
: Fork a job, take it, return it with its result, and verify that result. See [**magus-job**(1)](magus-job.md).

**notes**
: Human-authored notes committed to the repository. See [**magus-notes**(1)](magus-notes.md).

**queue**
: Merge approved changes through a speculative, partitioned merge queue. See [**magus-queue**(1)](magus-queue.md).

**watch**
: Emit changed file paths to stdout. See [**magus-watch**(1)](magus-watch.md).

**events**
: Stream workspace events as JSONL for an integration to consume. See [**magus-events**(1)](magus-events.md).

**server**
: Manage the magus server: MCP, the console, APIs and background jobs. See [**magus-server**(1)](magus-server.md).

**broker**
: The per-user process holding this host's capacity and shared services. See [**magus-broker**(1)](magus-broker.md).

**mcp**
: Serve MCP over stdio for the agent host that launched it. See [**magus-mcp**(1)](magus-mcp.md).

**status**
: Inspect concurrency pool and configuration. See [**magus-status**(1)](magus-status.md).

**buzz**
: Run a Buzz script. See [**magus-buzz**(1)](magus-buzz.md).

**spell**
: Build, publish, pull and list spells as OCI artifacts, and pin them in magus.lock. See [**magus-spell**(1)](magus-spell.md).

**agent**
: Manage skills, harnesses, and agent feedback. See [**magus-agent**(1)](magus-agent.md).

**init**
: Bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver). See [**magus-init**(1)](magus-init.md).

**doctor**
: Validate the workspace. See [**magus-doctor**(1)](magus-doctor.md).

**config**
: View or update magus configuration. See [**magus-config**(1)](magus-config.md).

**completion**
: Print a shell completion script. See [**magus-completion**(1)](magus-completion.md).

**man**
: Install the man pages embedded in this binary. See [**magus-man**(1)](magus-man.md).

**self**
: Manage the magus binary (update, install-shorthand). See [**magus-self**(1)](magus-self.md).

**version**
: Print the client and server versions. See [**magus-version**(1)](magus-version.md).

## Environment

**MAGUS_CACHE_DIR**
: One dir for the entries and the checkout's own files (default: entries shared per repository, the rest in .magus/). Config file key: **cache.dir**.

**MAGUS_CACHE_WRITE_ENABLED**
: When false, replay cache hits but never write new entries, locally or to a remote (default true). Config file key: **cache.write.enabled**.

**MAGUS_CACHE_INCLUDE_OS_ENABLED**
: When true, this host property keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay. Config file key: **cache.include.os.enabled**.

**MAGUS_CACHE_INCLUDE_ARCH_ENABLED**
: When true, this host property keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay. Config file key: **cache.include.arch.enabled**.

**MAGUS_CACHE_SIZE_MB**
: Disk cap in MB (binary); 0 = unlimited. Config file key: **cache.size_mb**.

**MAGUS_CACHE_REMOTE_TRUSTED_KEYS**
: Base64 Ed25519 public keys a remote artifact must be signed by; required when a backend is wired. Config file key: **cache.remote.trusted_keys**.

**MAGUS_CACHE_REMOTE_INSECURE**
: Disables remote-cache signature verification: unsigned artifacts are imported and produced with no trust set. A shared cache without signing is a supply-chain hazard — use only for trusted single-repo CI, or to validate a backend before minting keys. When true, trusted_keys stops being required and InsecureReason starts. Config file key: **cache.remote.insecure**.

**MAGUS_CACHE_REMOTE_INSECURE_REASON**
: The prose behind Insecure, required whenever it is true. Same rule SkipCacheReason and DriftReason carry: switching off the check that says whether an artifact came from who it claims is a claim about this cache. This file is committed, so the next machine to trust the cache reads the answer here. Config file key: **cache.remote.insecure_reason**.

**MAGUS_CACHE_REMOTE_WRITE_ENABLED**
: When false, write the local tier but never the remote; unset writes the remote when the local tier is written and a signing key is held; true makes remote writes required. Config file key: **cache.remote.write.enabled**.

**MAGUS_CI_MAX_SHARDS**
: Max parallel shards; -1 = unlimited (default: 8). Config file key: **ci.max_shards**.

**MAGUS_CI_RUNNER_POOL_BUDGET**
: GHA matrix-level concurrency cap; 0 = no cap. Config file key: **ci.runner_pool_budget**.

**MAGUS_CI_RECORD_RUNS**
: Keeps the per-branch run log (forecast.Run) in the history file: which commit a branch passed or failed a target at, and when. On by default, because \`--base last-passed\` reads it and a CI run that cannot find its base gates less than it appears to (default: true). Config file key: **ci.record_runs**.

**MAGUS_VOLATILITY_ENABLED**
: The master switch for volatility detection and auto-retry; false disables all retry logic (default: true). Config file key: **volatility.enabled**.

**MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES**
: Outcomes below which all failures retry once (default: 20). Config file key: **volatility.bootstrap_samples**.

**MAGUS_VOLATILITY_MIN_SAMPLES**
: Minimum outcomes before Wilson-score gates retry (default: 20). Config file key: **volatility.min_samples**.

**MAGUS_VOLATILITY_THRESHOLD**
: Wilson lower-bound above which a project+target is volatile (default: 0.05). Config file key: **volatility.threshold**.

**MAGUS_VOLATILITY_ANNOTATE_GHA**
: Emit ::warning annotations and GITHUB_STEP_SUMMARY table (default: true). Config file key: **volatility.annotate_gha**.

**MAGUS_TELEMETRY_ENABLED**
: Turns OTLP export on; magus connects to Endpoint when true. Config file key: **telemetry.enabled**.

**MAGUS_TELEMETRY_ENDPOINT**
: Host:port; required when Enabled. Config file key: **telemetry.endpoint**.

**MAGUS_TELEMETRY_PROTOCOL**
: OTLP wire protocol: grpc or http (default: grpc). Config file key: **telemetry.protocol**.

**MAGUS_TELEMETRY_INSECURE**
: Disable TLS. Config file key: **telemetry.insecure**.

**MAGUS_TELEMETRY_SERVICE_NAME**
: Resource attribute service.name (default: magus). Config file key: **telemetry.service_name**.

**MAGUS_TELEMETRY_SAMPLE_RATIO**
: Head-based sampling ratio; 1.0 = all (default: 1). Config file key: **telemetry.sample_ratio**.

**MAGUS_SERVER_ENABLED**
: Lets a command hand itself to a running \`magus server\`; false runs each invocation self-contained. Default true. With Enabled false an invocation never discovers or adopts the server and hosts its own per-process pool. Recursive magus calls still forward over a per-process socket to share the concurrency budget; only the server is opted out of (default: true). Config file key: **server.enabled**.

**MAGUS_SERVER_ADDRESS**
: The unix:// socket \`magus server\` listens on; empty means server.sock in the runtime dir. Config file key: **server.address**.

**MAGUS_SERVER_IDLE_TTL**
: Controls workspace eviction in the multi-workspace server; 0 = default 6h. Config file key: **server.idle_ttl**.

**MAGUS_SERVER_WORKSPACES**
: The explicit list of workspace roots to serve; non-empty enables eager union of sandbox policies and rejects out-of-list workspaces (MGS2010). Config file key: **server.workspaces**.

**MAGUS_SERVER_MAINTENANCE_ROTATE_ACTIVITIES**
: Only how often the server CHECKS whether the activity trail is due for a trim; it is not a retention age. The trail rotates on event count instead: 10000 events, with a per-kind floor that keeps a rare kind's newest entries no matter how loud its neighbors are (see maxEvents and perKindFloor in internal/trail). So there is no age cutoff this field could hand it: an age cutoff would have to decide whether it or the per-kind floor wins when the two disagree, and that policy does not exist. It runs hourly because a rotate on an already-small trail costs one stat (default: 1h). Config file key: **server.maintenance.rotate_activities**.

**MAGUS_SERVER_MAINTENANCE_ROTATE_LOGS**
: Both how often the server checks the run-log journals AND, since the check doubles as the enforcement, the maximum age a kept journal may reach: a journal older than this is dropped even when the workspace is well under the count (500) and size (2GB) caps those also enforce, and the tightest of the three wins. Weekly by default, because run-logs otherwise have no age bound at all (default: 168h). Config file key: **server.maintenance.rotate_logs**.

**MAGUS_SERVER_MAINTENANCE_PRUNE_PRESERVED**
: Only how often the server checks for expired \`vcs checkpoint --preserve\` captures; it is not the retention window. That window is fixed at 30 days (vcs.preserveRetention) and stays out of this field on purpose: the days a preserved capture survives is a promise the \`--preserve\` flag itself advertises, and letting a per-server poll knob also change what gets deleted would make that promise mean whatever this config happened to say (see the rationale on vcs.PrunePreserved). Preserve prunes on its own call too, but only a repository preserved a SECOND time ever reaches that pass, so this scheduled job is what makes the promise hold for a repository preserved once and never again. Daily, since a capture surviving an extra day past the fixed window costs nothing (default: 24h). Config file key: **server.maintenance.prune_preserved**.

**MAGUS_SERVER_MAINTENANCE_SYNC_GRAPH**
: Reconcile the graph; default 6h (a safety net behind the VCS hook) (default: 6h). Config file key: **server.maintenance.sync_graph**.

**MAGUS_SERVER_MAINTENANCE_CHECK_REVIEW**
: Notices a merge or a new remark on a review this tree took part in. The only scheduled job that leaves the machine, so its default is the longest here: a pull request merges once, and a remark waiting fifteen minutes costs nobody anything (default: 15m). Config file key: **server.maintenance.check_review**.

**MAGUS_VCS_ENABLED**
: Master switch for VCS-driven affected detection; false makes affected fall back to all projects (default true). Config file key: **vcs.enabled**.

**MAGUS_VCS_NAME**
: Pin VCS by name (git/hg/sl/jj); empty = autodetect. Config file key: **vcs.name**.

**MAGUS_VCS_BASE_REF**
: Sets the default base ref. Per-VCS overrides use MAGUS_VCS_\<NAME\>_BASE_REF (dynamic; not a Config field). Config file key: **vcs.base_ref**.

**MAGUS_MCP_ENABLED**
: When false, refuse to start the MCP server (default true). Config file key: **mcp.enabled**.

**MAGUS_MCP_ADDRESS**
: Host:port; default 127.0.0.1:7391. Config file key: **mcp.address**.

**MAGUS_MCP_HTTP**
: Serves /mcp on the server's HTTP listener. False leaves the route unmounted while \`magus mcp\` (stdio) and the server socket keep serving MCP, and the listener keeps the console and health routes. Default true. Config file key: **mcp.http**.

**MAGUS_MCP_INSECURE_BIND**
: Permits a non-loopback Address. That listener serves bearer tokens over plaintext HTTP, so without it the server refuses to start rather than warn. Config file key: **mcp.insecure_bind**.

**MAGUS_CONSOLE_ENABLED**
: When false, the MCP HTTP server does not mount the console's read-only API and job service (default true when MCP is up). Config file key: **console.enabled**.

**MAGUS_REPORT_FILTER**
: Restricts event types via +type/-type/bare terms; any "+" sets default-deny. Config file key: **report.filter**.

**MAGUS_LOG_FORMAT**
: The log format: pretty, plain, text or json. Config file key: **log.format**.

**MAGUS_LOG_LEVEL**
: The minimum log level; "trace" also enables the startup timing table. Config file key: **log.level**.

**MAGUS_LOG_SILENT**
: Suppresses progress like --quiet, and additionally bounds the failing-project dump (tail + path to the full log) and bubbles up only lines a target marks as a notice ("magus:notice:"). Like --quiet it prints a failure's reasoning only at -v. Normally set via -s/--silent; MAGUS_LOG_SILENT=1 is the env equivalent. Pointer to distinguish "not set" from explicit false. Config file key: **log.silent**.

**MAGUS_LOG_STREAM**
: Shows every target's subprocess output live and interleaved, instead of withholding a passing target's output until it fails. This is deliberately NOT derived from Level: what gets logged and whether a target's own output is withheld are separate questions, and conflating them is why -v used to ambush people with a wall of concurrent output when they wanted a little more detail. Normally set via -vv (and implied by -vvv); MAGUS_LOG_STREAM=1 is the env equivalent. Pointer to distinguish "not set" from explicit false. Config file key: **log.stream**.

**MAGUS_HINTS_ENABLED**
: Controls whether hint messages (actionable nudges) are printed to stderr. Defaults to true. Set hints.enabled: false in magus.yaml or MAGUS_HINTS_ENABLED=false to suppress all hint output. Pointer to distinguish "not set" from explicit false (default: true). Config file key: **hints.enabled**.

**MAGUS_KNOWLEDGE_WORKSPACES**
: Additional workspace roots to union into a \`--global\` knowledge-graph query (query/explain/path and graph export/stats). Each is loaded and its node IDs are namespaced by the workspace ("\<name\>//\<id\>") so IDs from different repos cannot collide. Empty means --global covers only the current workspace. Config file key: **knowledge.workspaces**.

**MAGUS_KNOWLEDGE_PUBLISHED_REF**
: The OCI artifact a published knowledge graph is READ from, as \<registry\>/\<repository\>:\<tag\>. Empty (the default) means this workspace pulls nothing and every graph is built locally. Config file key: **knowledge.published_ref**.

**MAGUS_KNOWLEDGE_MAX_SIZE_MB**
: A soft cap on the knowledge shard store (\<cache\>/knowledge). When exceeded after a build, least-recently-used shard files are evicted; an evicted shard is restored from the remote cache or rebuilt on the next query. 0 (default) is unlimited: the store self-reconciles deleted projects, so a cap mainly bounds transient bloat. Config file key: **knowledge.max_size_mb**.

**MAGUS_KNOWLEDGE_VCS_ENABLED**
: Folds VCS history (last commit, commit count) onto file nodes as the @vcs shard. Config file key: **knowledge.vcs.enabled**.

**MAGUS_KNOWLEDGE_VCS_MAX_COMMITS**
: Bounds the history walk to the most recent N commits. 0 uses a built-in default; a small value keeps the scan fast on a large repo at the cost of undercounting commits for long-lived files. Config file key: **knowledge.vcs.max_commits**.

**MAGUS_KNOWLEDGE_VCS_AUTHORSHIP**
: Includes the \`author\` nodes and \`authored\` edges (who touched which files) in the graph. Defaults to ON (nil = on): authorship is context that helps an agent, and the edges are already bounded by MaxCommits. Set false to keep only the per-file vcs_\* attrs and omit the author layer (default: true). Config file key: **knowledge.vcs.authorship**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_DISABLED**
: Opts out of background auto-indexing. Auto-indexing is on by default in the server, so this is the switch to turn it off (e.g. the indexers are not installed and you index in CI instead). Config file key: **knowledge.symbol_indexing.disabled**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_QUIET_SECONDS**
: How long a project's sources must be quiet after the last change before it is re-indexed, so a burst of edits coalesces into one run. 0 uses a built-in default. Config file key: **knowledge.symbol_indexing.quiet_seconds**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_MIN_INTERVAL_SECONDS**
: The minimum time between re-index runs for one project, a ceiling on how often the indexer fires however often files change. 0 uses a built-in default. Config file key: **knowledge.symbol_indexing.min_interval_seconds**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_CALLEES**
: Fewest callees a function needs before the lens compares it (default: 3). Config file key: **knowledge.duplication.min_callees**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SHARED**
: Fewest shared callees a pair needs to be reported (default: 4). Config file key: **knowledge.duplication.min_shared**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SCORE**
: Lowest similarity score in [0,1] the lens reports (default: 0.7). Config file key: **knowledge.duplication.min_score**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SPAN_RATIO**
: Lowest ratio in [0,1] of the shorter function's span to the longer one's (default: 0.5). Config file key: **knowledge.duplication.min_span_ratio**.

**MAGUS_KNOWLEDGE_DUPLICATION_INCLUDE_TESTS**
: When true, the lens also compares test functions. Config file key: **knowledge.duplication.include_tests**.

**MAGUS_KNOWLEDGE_SESSIONS_DISABLED**
: Opts out entirely, so \`graph build\` runs no adapter and \`--no-sessions\` becomes the permanent answer. The switch to reach for when the transcripts on this machine are not yours to read. Config file key: **knowledge.sessions.disabled**.

**MAGUS_KNOWLEDGE_NOTES_SHARED**
: The workspace-relative directory holding notes the TEAM has: committed, so git attributes each one to whoever wrote it and review sees every change. Empty disables it entirely: no shard, no verification, no guard rule. Config file key: **knowledge.notes.shared**.

**MAGUS_KNOWLEDGE_NOTES_PRIVATE**
: A SECOND notes location, yours rather than the team's, and it may sit anywhere on disk: a vault, a scratch directory, somewhere outside any repository. Config file key: **knowledge.notes.private**.

**MAGUS_SECRET_INTERACTIVE_TIMEOUT**
: Bounds a provider read when stdin is a terminal. Default 60s (default: 1m). Config file key: **secret.interactive_timeout**.

**MAGUS_SECRET_UNATTENDED_TIMEOUT**
: Bounds a provider read with no terminal to prompt on. Default 10s (default: 10s). Config file key: **secret.unattended_timeout**.

**MAGUS_DIFF_TUI**
: Opens the interactive viewer when the terminal can draw it. nil = default true. Config file key: **diff.tui**.

**MAGUS_JOBS_MAX_DEPTH**
: How many levels below its root job a fork may land. 0 = unlimited. Config file key: **jobs.max_depth**.

**MAGUS_JOBS_MAX_LIVE**
: How many live jobs one root's tree may hold at once, the new one included. 0 = unlimited. Config file key: **jobs.max_live**.

**MAGUS_JOBS_DEFAULT_TIMEOUT**
: Bounds a fork that names no --timeout. Zero = no bound. Config file key: **jobs.default_timeout**.

**MAGUS_JOBS_STALE_AFTER**
: Ends a declared job nobody took and nobody updated for this long, and flags a taken one in \`magus ls jobs\` and \`magus doctor\`. Default 2h; 0 = never. Ending a row reads the workspace's own magus.yaml, never user-global config or the environment (default: 2h). Config file key: **jobs.stale_after**.

**MAGUS_JOBS_RETAIN**
: How long a job that is over (graded, ended, or exited with its result never collected), and the archive a clear or a delete wrote, stay in the job store once nothing touches them. Default 168h; 0 keeps them forever. Like StaleAfter it reads the workspace's own magus.yaml only (default: 168h). Config file key: **jobs.retain**.

**MAGUS_JOBS_MAX_VALIDATIONS**
: How many \`magus run\`s one job tree's leased workers may have going at once; a run past it waits for a seat and says whose run holds it. A run with no lease (the root, a person) is never counted. Default 1; 0 = unlimited. Workspace only, like StaleAfter (default: 1). Config file key: **jobs.max_validations**.

**MAGUS_JOBS_MAX_CLAIM_FILES**
: How many files one glob write path may match before a job record refuses it as too broad (MGS3043). Default 50; zero or negative is an error. A sweep that needs more waives the path. Workspace only, like StaleAfter (default: 50). Config file key: **jobs.max_claim_files**.

**MAGUS_JOBS_MAX_CLAIM_PROJECTS**
: How many projects one glob write path may reach before a job record refuses it as too broad (MGS3043). Default 1; zero or negative is an error. Workspace only, like StaleAfter (default: 1). Config file key: **jobs.max_claim_projects**.

**MAGUS_CONCURRENCY**
: Caps concurrent builds; top-level and in-process fan-out share one limiter. Overrides concurrency_profile when set. Config file key: **concurrency**.

**MAGUS_CONCURRENCY_PROFILE**
: Sets the default width relative to the machine: conservative (half the cores), balanced (min(cores, 8), the default) or aggressive (every core). Unset is balanced everywhere; magus reads no environment variable to change this, so CI asks for aggressive explicitly. Config file key: **concurrency_profile**.

**MAGUS_BROKER**
: Decides whether a run asks the broker, the per-user process holding this host's slots, declared memory and shared services: required (refuse a step when none answers), best-effort (the default: run unarbitrated and say so once), or off (never start or contact one; a run hosts its services itself). Config file key: **broker**.

**MAGUS_SHUTDOWN_GRACE**
: Bounds how long the broker and the server take to stop after a first SIGTERM. The broker seats nothing new and waits this long for the runs holding claims or services to finish; the server cancels the runs it adopted and waits this long for them to unwind. A second SIGTERM, or a SIGINT to the broker, stops either at once. Zero stops at once on the first signal; negative is an error (default: 5m). Config file key: **shutdown_grace**.

**MAGUS_MAX_FAILURES**
: Bounds how many projects may fail before a run stops starting more. Zero, the default, is unlimited: a batch runs everything it can and reports every failure at once. Config file key: **max_failures**.

**MAGUS_TARGET_TIMEOUT**
: Bounds how long any single target may run before magus cancels it. Zero, the default, means no limit. Config file key: **target_timeout**.

**MAGUS_STALL_TIMEOUT**
: Aborts an invocation that is making no progress at all: no target has started, finished, or written a line of output for this long while every selected project's lock is still held. Zero, the default, uses the built-in 15-minute window; a negative value turns the watchdog off. Config file key: **stall_timeout**.

**MAGUS_HISTORY_PATH**
: The path to the runtime-history JSON used by volatility detection, CI forecaster, graph timing, and bisect. Defaults to $XDG_STATE_HOME/magus/history/v1.json. Config file key: **history_path**.

**MAGUS_DRY_RUN**
: Prints what would run without executing. Equivalent to MAGUS_DRY_RUN=1. Config file key: **dry_run**.

**MAGUS_DEFAULT_CHARMS**
: Execution charms applied to every \`magus run\` / \`magus x\` by default, e.g. ["rw"] to make targets write locally without typing :rw. Per-run :charms stack on top. The ci anchor still strips "rw" (RunCI), so a local \`magus run ci\` stays read-only; --no-default-charms ignores these for one run. \`magus affected\` does not apply them, so CI stays read-only unless explicit. Config file key: **default_charms**.

**MAGUS_SANDBOX**
: Off (the default), best-effort (landlock confines each child where the host has it, magus's own binding checks where it does not), or required (refuse to run, MGS2012, unless the kernel can confine every child: landlock ABI 3). Config file key: **sandbox.mode**.

**MAGUS_SANDBOX_ENV_PASSTHROUGH**
: Adds exact names and prefix patterns such as MISE_\* to the built-in env allowlist. A prefix must be at least three characters and end in "_". Config file key: **sandbox.env.passthrough**.

**MAGUS_CACHE_SIGNING_KEY**
: Env-only, no magus.yaml equivalent: the Ed25519 seed that signs remote-cache writes (see \`magus config cache-key\`); env-only because a signing secret in a committed file is not a secret

**MAGUS_CACHE_TOOL_VERSION**
: Env-only, no magus.yaml equivalent: how tool versions key the cache: project (per project), workspace (once for the workspace), or off (default: project)

**MAGUS_VCS_\<NAME\>_BASE_REF**
: Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs

**MAGUS_PROC_SOCKET**
: Env-only, no magus.yaml equivalent: the proc-server socket a magus process exports for the magus processes it spawns; unix:// URL or bare path, read directly by the process that adopts it

**MAGUS_PROC_TOKEN**
: Env-only, no magus.yaml equivalent: the secret the MAGUS_PROC_SOCKET server requires on every request, exported beside it and inherited only by a nested magus

**MAGUS_SHARD**
: CI matrix shard ID (e.g. "0"); equivalent to magus run --shard; set by .github/actions/magus

**MAGUS_N_SHARDS**
: Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus

**MAGUS_UPDATE_URL**
: Env-only, no magus.yaml equivalent: override the release index URL for \`magus self update\`; set to a self-hosted copy of index.json to use a private update channel (default: https://eli.gladman.cc/magus/public/release/index.json)

**MAGUS_STAMPED_LEASE**
: Set by a shared server on a script it forks for a remote caller, and by an authenticated MCP caller for a child magus: the BAGGAGE lease was stamped by the transport rather than claimed by the spawner. It only downgrades: a missing lease reads as unstamped instead of unbound, never as a grant; never set it by hand

**MAGUS_NO_BOOTSTRAP_EXEC**
: Env-only, no magus.yaml equivalent: when 1, true or yes, disable the pre-workspace-load check that replaces this process with a workspace-local ./magus found by walking up from the working directory (or --root); set it to force the binary actually invoked to run instead, e.g. while debugging that binary itself (default: false)

**MAGUS_OFFLINE**
: Env-only, no magus.yaml equivalent: when set to anything but 0 or false, a remote-spell fetch and every std/http request a script sends fail with a named error instead of sending a request, and the lifecycle provider is not asked: \`magus describe tools\` replays its stored answer or reads unknown (offline) (default: false)

**MAGUS_DIFFTOOL**
: Env-only, no magus.yaml equivalent: the command, taking two paths, that \`--then file \<path\> diff\` compares a cached artifact with (default: $DIFFTOOL, then git diff --no-index)

**MAGUS_LOG_VIEWER_URL**
: Env-only, no magus.yaml equivalent: base URL of the log viewer page a live run opens, for a self-hosted mirror (default: the hosted log viewer)

**MAGUS_CONSOLE_DIR**
: Env-only, no magus.yaml equivalent: directory of the built console the LAN share serves (default: \<root\>/console/gen)

**MAGUS_PPROF**
: Env-only, no magus.yaml equivalent: write Go profiles for one invocation, as kind:path pairs (cpu, mem, trace), comma separated

**MAGUS_MCP_TOKEN**
: Env-only: the secret reference shipped harness spells name for the MCP bearer token; the environment secret provider reads it from this variable

**MAGUS_MCP_BUZZ_PURE**
: Set internally by the buzz MCP tool for its forked transform worker; never set it by hand

**MAGUS_MCP_CLIENT**
: Set internally by the client MCP tool for its forked worker; never set it by hand

**MAGUS_MCP_CLIENT_HOST**
: Set internally by the client MCP tool: the calling client's name, stamped onto the worker's activity trail; never set it by hand

**MAGUS_MCP_CLIENT_LEASE**
: Set internally by the client MCP tool: the calling client's job lease, or empty when the transport stamped none; never set it by hand

**MAGUS_S3_BUCKET**
: Env-only: the bucket the aws/s3-cache spell stores remote-cache artifacts in

**MAGUS_S3_ENDPOINT**
: Env-only: the S3-compatible endpoint the aws/s3-cache spell talks to (default: https://s3.\<region\>.amazonaws.com)

**MAGUS_LEVEL**
: Set by magus for the processes it spawns: the magus recursion depth, like make's MAKELEVEL; never set it by hand

**MAGUS_INVOCATION_ANCESTORS**
: Set by magus for the processes it spawns: the comma-separated invocations a nested magus runs underneath; never set it by hand

**MAGUS_BOOTSTRAP_EXEC_DONE**
: Set by magus when it replaces itself with a workspace-local ./magus, so the replacement never hops again; never set it by hand

**MAGUS_SYMBOL_INDEX**
: Set by magus for a spell's symbol indexer op: the path the indexer writes its SCIP index to; never set it by hand

**MAGUS_WORKSPACE_ROOT**
: Set by magus for a spell's symbol indexer op: the workspace root the index's paths are relative to; never set it by hand

**MAGUS_INTERNAL_ADVICE_MODE**
: Set by \`magus diff\` for the advice script it runs; the two halves of one feature, not a setting, and either may be renamed without notice

**MAGUS_INTERNAL_ADVICE_BASE_BRANCH**
: Set by \`magus diff\` alongside MAGUS_INTERNAL_ADVICE_MODE; not a setting

## Files

**magus.yaml, .magus.yaml**
: Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.

**.magus/**
: Content-addressed build cache in the workspace root. Override with MAGUS_CACHE_DIR.

## See Also

[**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-refs**(1)](magus-refs.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-status**(1)](magus-status.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

