---
title: magus command
generated_from: internal/cli/registry.go
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

## Global Flags

Global flags are accepted by every subcommand and may appear before or
after the subcommand word. Last-write-wins, matching kubectl conventions.

**--root** *path*
: Workspace root. Default: walk up from cwd until go.mod is found. Must precede the subcommand.

**--config** *path*
: Config file path. Default: search for magus.yaml in CWD, workspace root, and $XDG_CONFIG_HOME/magus/. Must precede the subcommand.

**--output** *fmt*, **-o** *fmt*
: Output format: text (default), json, yaml, name, jsonl, or template[=\<go-template\>]. Honored by subcommands that emit structured data. A template body renders a Go text/template over the same value -o json emits (field names are the json keys); a bare -o template with no body lists that output's fields instead of rendering - the json keys usable in -o json and -o template, with each field's type and doc.

**--concurrency** *N*
: Maximum number of concurrent build steps. 0 means use the configured value (or MAGUS_CONCURRENCY, or concurrency_profile, balanced (min(NumCPU,8)) by default).

**-v**
: Increase log verbosity. Repeat for more detail (-v, -vv, -vvv).

## Subcommands

**ls**
: List all discovered projects. See [**magus-ls**(1)](magus-ls.md).

**describe**
: Define a magus concept and list its entities. See [**magus-describe**(1)](magus-describe.md).

**run**
: Run a target for selected projects. See [**magus-run**(1)](magus-run.md).

**x**
: Reproduce an output ref, or pick project + target. See [**magus-x**(1)](magus-x.md).

**where**
: Print the absolute path of a project. See [**magus-where**(1)](magus-where.md).

**affected**
: Run a target for VCS-diff affected projects. See [**magus-affected**(1)](magus-affected.md).

**graph**
: The workspace's graphs as objects: deps, export, stats. See [**magus-graph**(1)](magus-graph.md).

**query**
: Search the knowledge graph, and retrieve a run's output or journal by id. See [**magus-query**(1)](magus-query.md).

**explain**
: Show one knowledge-graph node's context: data, edges, and reach. See [**magus-explain**(1)](magus-explain.md).

**path**
: Connect two knowledge-graph nodes: the shortest chain of edges between them. See [**magus-path**(1)](magus-path.md).

**refs**
: List where an ingested code symbol is defined and referenced. See [**magus-refs**(1)](magus-refs.md).

**watch**
: Emit changed file paths to stdout. See [**magus-watch**(1)](magus-watch.md).

**events**
: Stream workspace events as JSONL for an integration to consume. See [**magus-events**(1)](magus-events.md).

**status**
: Inspect concurrency pool and configuration. See [**magus-status**(1)](magus-status.md).

**clean**
: Remove declared outputs (regenerable, never sources). See [**magus-clean**(1)](magus-clean.md).

**shell**
: Check a command against this workspace's conventions before running it. See [**magus-shell**(1)](magus-shell.md).

**vcs**
: Staging and conflict resolution that knows what is generated. See [**magus-vcs**(1)](magus-vcs.md).

**queue**
: Merge approved changes through a speculative, partitioned merge queue. See [**magus-queue**(1)](magus-queue.md).

**doctor**
: Validate the workspace. See [**magus-doctor**(1)](magus-doctor.md).

**config**
: View or update magus configuration. See [**magus-config**(1)](magus-config.md).

**session**
: What magus invocations did and what agents are blocked on: humans read and dispose, hosts write. See [**magus-session**(1)](magus-session.md).

**memory**
: Durable cross-session project memory. See [**magus-memory**(1)](magus-memory.md).

**job**
: Fork a job, take it, return it with its result, and verify that result. See [**magus-job**(1)](magus-job.md).

**notes**
: Human-authored notes committed to the repository. See [**magus-notes**(1)](magus-notes.md).

**diff**
: Read the working tree's changes in the order they deserve attention. See [**magus-diff**(1)](magus-diff.md).

**server**
: Manage the magus server: MCP, the console, APIs and background jobs. See [**magus-server**(1)](magus-server.md).

**broker**
: The per-user process holding this host's capacity and shared services. See [**magus-broker**(1)](magus-broker.md).

**mcp**
: Serve MCP over stdio for the agent host that launched it. See [**magus-mcp**(1)](magus-mcp.md).

**buzz**
: Run a Buzz script. See [**magus-buzz**(1)](magus-buzz.md).

**completion**
: Print a shell completion script. See [**magus-completion**(1)](magus-completion.md).

**man**
: Install the man pages embedded in this binary. See [**magus-man**(1)](magus-man.md).

**init**
: Bootstrap a workspace (magus.yaml + magusfile.buzz + merge driver). See [**magus-init**(1)](magus-init.md).

**spell**
: Build, publish, pull and list spells as OCI artifacts, and pin them in magus.lock. See [**magus-spell**(1)](magus-spell.md).

**agent**
: Manage skills, harnesses, and agent feedback. See [**magus-agent**(1)](magus-agent.md).

**self**
: Manage the magus binary (update, refresh, registry, install-shorthand). See [**magus-self**(1)](magus-self.md).

**version**
: Print the client and server versions. See [**magus-version**(1)](magus-version.md).

## Environment

**MAGUS_CACHE_DIR**
: Override the default cache location (.magus/ in the workspace root). Equivalent magus.yaml key: **cache.dir**.

**MAGUS_CACHE_WRITE_ENABLED**
: When false (or 0), replay cache hits but never write new entries, locally or to a remote (default: true). Equivalent magus.yaml key: **cache.write.enabled**.

**MAGUS_CACHE_REMOTE_WRITE_ENABLED**
: When false (or 0), write the local cache tier but never the remote tier. Unset, the remote tier is written when the local tier is and a signing key is held. True makes remote writes required: an error without a signing key or with cache.write.enabled false, and a failed remote write fails the step (default: cache.write.enabled). Equivalent magus.yaml key: **cache.remote.write.enabled**.

**MAGUS_CACHE_INCLUDE_OS_ENABLED**
: When true, the host OS keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay (default: false). Equivalent magus.yaml key: **cache.include.os.enabled**.

**MAGUS_CACHE_INCLUDE_ARCH_ENABLED**
: When true, the host architecture keys every cache entry; off by default because a manifest guard already refuses a cross-platform replay (default: false). Equivalent magus.yaml key: **cache.include.arch.enabled**.

**MAGUS_CACHE_SIZE_MB**
: Cache disk usage cap in MB (binary, 1\<\<20); 0 means unlimited (default: 0). Equivalent magus.yaml key: **cache.size_mb**.

**MAGUS_CACHE_REMOTE_TRUSTED_KEYS**
: Comma-separated base64 Ed25519 public keys a remote artifact must be signed by; required when a remote backend is wired. Equivalent magus.yaml key: **cache.remote.trusted_keys**.

**MAGUS_CACHE_REMOTE_INSECURE**
: Disable remote-cache signature verification (accept/produce unsigned artifacts); for trusted single-repo CI only. Requires cache.remote.insecure_reason (default: false). Equivalent magus.yaml key: **cache.remote.insecure**.

**MAGUS_CACHE_REMOTE_INSECURE_REASON**
: Why this cache runs unverified; required whenever cache.remote.insecure is true. Equivalent magus.yaml key: **cache.remote.insecure_reason**.

**MAGUS_CACHE_SIGNING_KEY**
: Env-only, no magus.yaml equivalent: the Ed25519 seed that signs remote-cache writes (see \`magus config cache-key\`); env-only because a signing secret in a committed file is not a secret

**MAGUS_CACHE_TOOL_VERSION**
: Env-only, no magus.yaml equivalent: how tool versions key the cache: project (per project), workspace (once for the workspace), or off (default: project)

**MAGUS_LOG_FORMAT**
: Output format: pretty, plain, text, or json (default: pretty). Equivalent magus.yaml key: **log.format**.

**MAGUS_LOG_LEVEL**
: Minimum log level: trace, debug, info, warn, error (trace also prints the startup timing table) (default: info). Equivalent magus.yaml key: **log.level**.

**MAGUS_LOG_SILENT**
: When true, the env equivalent of -s/--silent: suppress progress, bound the failing-project dump, and surface only lines a target marks as a notice (default: false). Equivalent magus.yaml key: **log.silent**.

**MAGUS_LOG_STREAM**
: When true, the env equivalent of -vv: stream every target's output live instead of withholding a passing target's output (default: false). Equivalent magus.yaml key: **log.stream**.

**MAGUS_CONCURRENCY**
: Maximum number of concurrently running per-project build steps; overrides concurrency_profile when positive (default: concurrency_profile decides). Equivalent magus.yaml key: **concurrency**.

**MAGUS_CONCURRENCY_PROFILE**
: Default build width relative to the machine: conservative (half the cores), balanced (min(cores,8)), or aggressive (every core, and all usable memory minus a 512 MiB floor). Unset is balanced everywhere; CI asks for aggressive explicitly (default: balanced). Equivalent magus.yaml key: **concurrency_profile**.

**MAGUS_BROKER**
: Whether a run asks the broker for this host's capacity and shared services: required refuses a step when none answers (MGS3022, exit 69), best-effort runs unarbitrated and says so once, off never starts or contacts one (default: best-effort). Equivalent magus.yaml key: **broker**.

**MAGUS_HISTORY_PATH**
: Path to the runtime-history JSON shared by volatility detection, the CI forecaster, graph timing, and bisect (default: $XDG_STATE_HOME/magus/history/v1.json). Equivalent magus.yaml key: **history_path**.

**MAGUS_DRY_RUN**
: When 1 or true, print what would run without executing anything (default: false). Equivalent magus.yaml key: **dry_run**.

**MAGUS_MAX_FAILURES**
: How many projects may fail before a run stops starting more; 1 is fail-fast, 0 is unlimited (default: 0). Equivalent magus.yaml key: **max_failures**.

**MAGUS_TARGET_TIMEOUT**
: Duration after which magus cancels any single target, subprocesses included; 0 means no limit (default: 0). Equivalent magus.yaml key: **target_timeout**.

**MAGUS_STALL_TIMEOUT**
: Abort an invocation that starts, finishes and prints nothing for this long; negative turns the watchdog off (default: 15m). Equivalent magus.yaml key: **stall_timeout**.

**MAGUS_DEFAULT_CHARMS**
: Comma-separated charms applied to every magus run/x by default (e.g. rw); the ci anchor still strips rw, and --no-default-charms ignores them for one run. Equivalent magus.yaml key: **default_charms**.

**MAGUS_VCS_ENABLED**
: Master switch for VCS-driven affected detection; false makes affected fall back to all projects (default: true). Equivalent magus.yaml key: **vcs.enabled**.

**MAGUS_VCS_NAME**
: Pin the active VCS by name (git, hg, sl, jj); empty autodetects from .git/.hg/.sl/.jj. Equivalent magus.yaml key: **vcs.name**.

**MAGUS_VCS_BASE_REF**
: Default base ref for the active VCS adapter, e.g. origin/main for git. Equivalent magus.yaml key: **vcs.base_ref**.

**MAGUS_VCS_\<NAME\>_BASE_REF**
: Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs

**MAGUS_PROC_SOCKET**
: Env-only, no magus.yaml equivalent: the proc-server socket a magus process exports for the magus processes it spawns; unix:// URL or bare path, read directly by the process that adopts it

**MAGUS_CI_MAX_SHARDS**
: Maximum number of parallel CI shards; -1 means unlimited (default: 8). Equivalent magus.yaml key: **ci.max_shards**.

**MAGUS_CI_RUNNER_POOL_BUDGET**
: Cross-shard concurrency cap at the GHA matrix level; 0 means unlimited (default: 0). Equivalent magus.yaml key: **ci.runner_pool_budget**.

**MAGUS_CI_RECORD_RUNS**
: Keep the per-branch run log (which commit a branch passed or failed at) in the history file (default: true). Equivalent magus.yaml key: **ci.record_runs**.

**MAGUS_SHARD**
: CI matrix shard ID (e.g. "0"); equivalent to magus run --shard; set by .github/actions/magus

**MAGUS_N_SHARDS**
: Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus

**MAGUS_TELEMETRY_ENABLED**
: Turn OTLP export on; magus connects to telemetry.endpoint when true (default: false). Equivalent magus.yaml key: **telemetry.enabled**.

**MAGUS_TELEMETRY_ENDPOINT**
: OTLP collector address as host:port (no scheme); required when telemetry is enabled. Equivalent magus.yaml key: **telemetry.endpoint**.

**MAGUS_TELEMETRY_PROTOCOL**
: OTLP wire protocol: grpc or http (default: grpc). Equivalent magus.yaml key: **telemetry.protocol**.

**MAGUS_TELEMETRY_INSECURE**
: Disable TLS for the OTLP exporter (plaintext local-collector setups) (default: false). Equivalent magus.yaml key: **telemetry.insecure**.

**MAGUS_TELEMETRY_SERVICE_NAME**
: Value of the resource attribute service.name on emitted spans/metrics (default: magus). Equivalent magus.yaml key: **telemetry.service_name**.

**MAGUS_TELEMETRY_SAMPLE_RATIO**
: Head-based trace sampling ratio in [0,1] (default: 1.0). Equivalent magus.yaml key: **telemetry.sample_ratio**.

**MAGUS_SERVER_ENABLED**
: When false, no command hands itself to a running \`magus server\`; each invocation runs self-contained (default: true). Equivalent magus.yaml key: **server.enabled**.

**MAGUS_SERVER_ADDRESS**
: Socket \`magus server\` listens on, as a unix:// URL; empty is server.sock in the runtime directory. Equivalent magus.yaml key: **server.address**.

**MAGUS_SERVER_IDLE_TTL**
: Idle workspace eviction TTL for the multi-workspace server; e.g. "6h", "30m" (default: 6h). Equivalent magus.yaml key: **server.idle_ttl**.

**MAGUS_SERVER_WORKSPACES**
: Colon-separated list of workspace roots the server will serve; non-empty list triggers eager union of sandbox policies and rejection of out-of-list workspaces (MGS2010). Equivalent magus.yaml key: **server.workspaces**.

**MAGUS_SERVER_MAINTENANCE_ROTATE_ACTIVITIES**
: How often the server checks whether the activity trail is due for a trim (default: 1h). Equivalent magus.yaml key: **server.maintenance.rotate_activities**.

**MAGUS_SERVER_MAINTENANCE_ROTATE_LOGS**
: How often the server checks the run-log journals, and the age past which it trims them (default: 168h). Equivalent magus.yaml key: **server.maintenance.rotate_logs**.

**MAGUS_SERVER_MAINTENANCE_PRUNE_PRESERVED**
: How often the server checks for expired \`vcs checkpoint --preserve\` captures (default: 24h). Equivalent magus.yaml key: **server.maintenance.prune_preserved**.

**MAGUS_SERVER_MAINTENANCE_SYNC_GRAPH**
: How often the server reconciles the knowledge graph, a safety net behind the VCS refresh hook (default: 6h). Equivalent magus.yaml key: **server.maintenance.sync_graph**.

**MAGUS_SERVER_MAINTENANCE_CHECK_REVIEW**
: How often the server checks for a merge or a new remark on a review this tree took part in (default: 15m). Equivalent magus.yaml key: **server.maintenance.check_review**.

**MAGUS_MCP_ENABLED**
: When 0 or false, refuse to start the MCP server (default: true). Equivalent magus.yaml key: **mcp.enabled**.

**MAGUS_MCP_ADDRESS**
: host:port for the MCP Streamable HTTP server \`magus server\` starts (default: 127.0.0.1:7391). Equivalent magus.yaml key: **mcp.address**.

**MAGUS_MCP_INSECURE_BIND**
: Permit a non-loopback mcp.address, which serves bearer tokens over plaintext HTTP; without it such an address is an error (default: false). Equivalent magus.yaml key: **mcp.insecure_bind**.

**MAGUS_CONSOLE_ENABLED**
: When false, the MCP HTTP server does not mount the console's read-only API and job service (default: true when MCP is up). Equivalent magus.yaml key: **console.enabled**.

**MAGUS_HINTS_ENABLED**
: When false, suppress all hint messages printed to stderr (default: true). Equivalent magus.yaml key: **hints.enabled**.

**MAGUS_DIFF_TUI**
: When false, \`magus diff\` prints its report instead of opening the interactive viewer (default: true). Equivalent magus.yaml key: **diff.tui**.

**MAGUS_JOBS_MAX_DEPTH**
: How many levels below its root job a \`magus job fork\` may land; 0 means unlimited (default: 0). Equivalent magus.yaml key: **jobs.max_depth**.

**MAGUS_JOBS_MAX_LIVE**
: How many live jobs one root's tree may hold at once, the new one included; 0 means unlimited (default: 0). Equivalent magus.yaml key: **jobs.max_live**.

**MAGUS_JOBS_DEFAULT_TIMEOUT**
: Timeout for a fork that names no --timeout; 0 means no bound (default: 0). Equivalent magus.yaml key: **jobs.default_timeout**.

**MAGUS_JOBS_STALE_AFTER**
: Flag a live job nobody updated for this long in \`magus ls jobs\` and \`magus doctor\`; 0 never flags (default: 0). Equivalent magus.yaml key: **jobs.stale_after**.

**MAGUS_SECRET_INTERACTIVE_TIMEOUT**
: Bound on a secret-provider read when stdin is a terminal (default: 60s). Equivalent magus.yaml key: **secret.interactive_timeout**.

**MAGUS_SECRET_UNATTENDED_TIMEOUT**
: Bound on a secret-provider read with no terminal to prompt on (default: 10s). Equivalent magus.yaml key: **secret.unattended_timeout**.

**MAGUS_KNOWLEDGE_WORKSPACES**
: Comma-separated extra workspace roots a --global knowledge-graph query unions in. Equivalent magus.yaml key: **knowledge.workspaces**.

**MAGUS_KNOWLEDGE_PUBLISHED_REF**
: OCI artifact a published knowledge graph is read from, as \<registry\>/\<repository\>:\<tag\>. Equivalent magus.yaml key: **knowledge.published_ref**.

**MAGUS_KNOWLEDGE_MAX_SIZE_MB**
: Soft cap on the knowledge shard store in MB; least-recently-used shards are evicted past it. 0 means unlimited (default: 0). Equivalent magus.yaml key: **knowledge.max_size_mb**.

**MAGUS_KNOWLEDGE_VCS_ENABLED**
: Fold VCS history (last commit, commit count) onto file nodes as the @vcs shard (default: false). Equivalent magus.yaml key: **knowledge.vcs.enabled**.

**MAGUS_KNOWLEDGE_VCS_MAX_COMMITS**
: Bound the history walk to the most recent N commits; 0 uses a built-in default (default: 0). Equivalent magus.yaml key: **knowledge.vcs.max_commits**.

**MAGUS_KNOWLEDGE_VCS_AUTHORSHIP**
: Include author nodes and authored edges in the @vcs shard; false keeps only the per-file attributes (default: true). Equivalent magus.yaml key: **knowledge.vcs.authorship**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_DISABLED**
: When true, the server never re-runs a project's scip op on its own (default: false). Equivalent magus.yaml key: **knowledge.symbol_indexing.disabled**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_QUIET_SECONDS**
: Seconds a project's sources must be quiet before the server re-indexes it; 0 uses a built-in default (default: 0). Equivalent magus.yaml key: **knowledge.symbol_indexing.quiet_seconds**.

**MAGUS_KNOWLEDGE_SYMBOL_INDEXING_MIN_INTERVAL_SECONDS**
: Minimum seconds between re-index runs for one project; 0 uses a built-in default (default: 0). Equivalent magus.yaml key: **knowledge.symbol_indexing.min_interval_seconds**.

**MAGUS_KNOWLEDGE_SESSIONS_DISABLED**
: When true, \`graph build\` runs no agent-session adapter (default: false). Equivalent magus.yaml key: **knowledge.sessions.disabled**.

**MAGUS_KNOWLEDGE_NOTES_SHARED**
: Workspace-relative directory of the team's committed notes. Equivalent magus.yaml key: **knowledge.notes.shared**.

**MAGUS_KNOWLEDGE_NOTES_PRIVATE**
: A second notes directory, yours rather than the team's, anywhere on disk. Equivalent magus.yaml key: **knowledge.notes.private**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_CALLEES**
: Fewest callees a function needs before the duplication lens compares it (default: 3). Equivalent magus.yaml key: **knowledge.duplication.min_callees**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SHARED**
: Fewest shared callees a pair needs to be reported as duplicates (default: 4). Equivalent magus.yaml key: **knowledge.duplication.min_shared**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SCORE**
: Lowest similarity score in [0,1] the duplication lens reports (default: 0.7). Equivalent magus.yaml key: **knowledge.duplication.min_score**.

**MAGUS_KNOWLEDGE_DUPLICATION_MIN_SPAN_RATIO**
: Lowest ratio in [0,1] of the shorter function's span to the longer one's (default: 0.5). Equivalent magus.yaml key: **knowledge.duplication.min_span_ratio**.

**MAGUS_KNOWLEDGE_DUPLICATION_INCLUDE_TESTS**
: When true, the duplication lens also compares test functions (default: false). Equivalent magus.yaml key: **knowledge.duplication.include_tests**.

**MAGUS_VOLATILITY_ENABLED**
: Master switch for volatility detection and auto-retry; false disables all retry logic (default: true). Equivalent magus.yaml key: **volatility.enabled**.

**MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES**
: Number of outcomes below which all failures are retried once (bootstrap phase) (default: 20). Equivalent magus.yaml key: **volatility.bootstrap_samples**.

**MAGUS_VOLATILITY_MIN_SAMPLES**
: Minimum outcomes required before Wilson-score volatility rate gates retry decisions (default: 20). Equivalent magus.yaml key: **volatility.min_samples**.

**MAGUS_VOLATILITY_THRESHOLD**
: Wilson lower-bound volatility rate above which a project+target is considered volatile (default: 0.05). Equivalent magus.yaml key: **volatility.threshold**.

**MAGUS_VOLATILITY_ANNOTATE_GHA**
: When true, emit ::warning annotations and volatility summary to $GITHUB_STEP_SUMMARY (default: true). Equivalent magus.yaml key: **volatility.annotate_gha**.

**MAGUS_REPORT_FILTER**
: Comma-separated +type/-type terms restricting JSONL event emission (e.g. -graph.build,-graph.query). Equivalent magus.yaml key: **report.filter**.

**MAGUS_SANDBOX_ENABLED**
: When 1 or true, confine every subprocess and in-process spell to the workspace + a curated allowlist, scrub the child-process env to a minimum allowlist, and refuse paths outside it. See magus.yaml sandbox.allow and sandbox.env.passthrough for extension (default: false). Equivalent magus.yaml key: **sandbox.enabled**.

**MAGUS_SANDBOX_ENV_PASSTHROUGH**
: Comma-separated names or globs (e.g. MISE_\*) added to the sandbox's child-process env allowlist. Equivalent magus.yaml key: **sandbox.env.passthrough**.

**MAGUS_UPDATE_URL**
: Env-only, no magus.yaml equivalent: override the release index URL for \`magus self update\`; set to a self-hosted copy of index.json to use a private update channel (default: https://eli.gladman.cc/magus/public/release/index.json)

**MAGUS_NO_BOOTSTRAP_EXEC**
: Env-only, no magus.yaml equivalent: when 1, true or yes, disable the pre-workspace-load check that replaces this process with a workspace-local ./magus found by walking up from the working directory (or --root); set it to force the binary actually invoked to run instead, e.g. while debugging that binary itself (default: false)

**MAGUS_REGISTRY_URL**
: Env-only, no magus.yaml equivalent: override the built-in registry source's URL with a mirror of its signed index (default: https://eli.gladman.cc/magus/public/registry/index.json)

**MAGUS_OFFLINE**
: Env-only, no magus.yaml equivalent: when set to anything but 0 or false, a registry or remote-spell fetch fails with a named error instead of sending a request (default: false)

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
: Set by magus for a spell's scip op: the path the indexer writes its SCIP index to; never set it by hand

**MAGUS_INTERNAL_ADVICE_MODE**
: Set by \`magus diff\` for the advice script it runs; the two halves of one feature, not a setting, and either may be renamed without notice

**MAGUS_INTERNAL_ADVICE_BASE_BRANCH**
: Set by \`magus diff\` alongside MAGUS_INTERNAL_ADVICE_MODE; not a setting

## Files

**magus.yaml**, **.magus.yaml**
: Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.

**.magus/**
: Content-addressed build cache in the workspace root. Override with
MAGUS_CACHE_DIR.

## See Also

[**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

