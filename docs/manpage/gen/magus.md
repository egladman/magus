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
: Output format: text (default), json, yaml, name, wide, or template=\<go-template\>. Honoured by subcommands that emit structured data.

**--concurrency** *N*
: Maximum number of concurrent build steps. 0 means use the configured value (or MAGUS_CONCURRENCY, or min(NumCPU,8)).

**-v**
: Increase log verbosity. Repeat for more detail (-v, -vv, -vvv).

## Subcommands

**ls**
: List all discovered projects. See [**magus-ls**(1)](magus-ls.md).

**describe**
: Explain why a project is in the affected set. See [**magus-describe**(1)](magus-describe.md).

**run**
: Run a target for selected projects. See [**magus-run**(1)](magus-run.md).

**x**
: Interactive shorthand: pick project + target. See [**magus-x**(1)](magus-x.md).

**where**
: Print the absolute path of a project. See [**magus-where**(1)](magus-where.md).

**tail**
: Stream the most recent cached log (interactive only). See [**magus-tail**(1)](magus-tail.md).

**affected**
: Run a target for VCS-diff affected projects. See [**magus-affected**(1)](magus-affected.md).

**watch**
: Emit changed file paths to stdout. See [**magus-watch**(1)](magus-watch.md).

**status**
: Inspect concurrency pool and configuration. See [**magus-status**(1)](magus-status.md).

**doctor**
: Validate the workspace. See [**magus-doctor**(1)](magus-doctor.md).

**config**
: View or update magus configuration. See [**magus-config**(1)](magus-config.md).

**server**
: Manage the persistent magus daemon. See [**magus-server**(1)](magus-server.md).

**completion**
: Print a shell completion script. See [**magus-completion**(1)](magus-completion.md).

**init**
: Bootstrap a workspace (magus.yaml + magusfile.tl + merge driver). See [**magus-init**(1)](magus-init.md).

**self**
: Manage the magus binary (update/install need -tags selfmanage). See [**magus-self**(1)](magus-self.md).

**version**
: Print version, commit, and build date. See [**magus-version**(1)](magus-version.md).

## Environment

**MAGUS_CACHE_DIR**
: Override the default cache location (.magus/ in the workspace root). Equivalent magus.yaml key: **cache.dir**.

**MAGUS_CACHE_IMMUTABLE**
: When true (or 1), open the cache in read-only mode: replay hits but never write new entries (default: false). Equivalent magus.yaml key: **cache.immutable**.

**MAGUS_CACHE_SIZE_MB**
: Cache disk usage cap in MB (binary, 1\<\<20); 0 means unlimited (default: 0). Equivalent magus.yaml key: **cache.size_mb**.

**MAGUS_LOG_FORMAT**
: Output format: pretty, plain, text, or json (default: pretty). Equivalent magus.yaml key: **log.format**.

**MAGUS_LOG_LEVEL**
: Minimum log level: trace, debug, info, warn, error (trace also prints the startup timing table) (default: info). Equivalent magus.yaml key: **log.level**.

**MAGUS_CONCURRENCY**
: Maximum number of concurrently running per-project build steps (default: min(NumCPU,8)). Equivalent magus.yaml key: **concurrency**.

**MAGUS_HISTORY_PATH**
: Path to the runtime-history JSON shared by flake detection, the CI forecaster, graph timing, and bisect (default: $XDG_STATE_HOME/magus/history/v1.json). Equivalent magus.yaml key: **history_path**.

**MAGUS_DRY_RUN**
: When 1 or true, print what would run without executing anything (default: false). Equivalent magus.yaml key: **dry_run**.

**MAGUS_STRICT**
: When 1 or true, workspace correctness warnings (e.g. unregistered dependencies) become errors that fail the run (default: false). Equivalent magus.yaml key: **strict**.

**MAGUS_VCS_ENABLED**
: Master switch for VCS-driven affected detection; false makes affected fall back to all projects (default: true). Equivalent magus.yaml key: **vcs.enabled**.

**MAGUS_VCS_NAME**
: Pin the active VCS by name (git, hg, jj); empty autodetects from .git/.hg/.jj. Equivalent magus.yaml key: **vcs.name**.

**MAGUS_VCS_BASE_REF**
: Default base ref for the active VCS adapter, e.g. origin/main for git. Equivalent magus.yaml key: **vcs.base_ref**.

**MAGUS_VCS_\<NAME\>_BASE_REF**
: Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs

**MAGUS_DAEMON_SOCKET**
: Runtime proc-server socket set by the daemon for forwarded child processes; unix:// URL or bare path. Equivalent magus.yaml key: **daemon.socket**.

**MAGUS_CI_MAX_SHARDS**
: Maximum number of parallel CI shards; -1 means unlimited (default: 8). Equivalent magus.yaml key: **ci.max_shards**.

**MAGUS_CI_RUNNER_POOL_BUDGET**
: Cross-shard concurrency cap at the GHA matrix level; 0 means unlimited (default: 0). Equivalent magus.yaml key: **ci.runner_pool_budget**.

**MAGUS_SHARD**
: CI matrix shard ID (e.g. "0"); equivalent to magus run --shard; set by .github/actions/magus

**MAGUS_N_SHARDS**
: Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus

**MAGUS_GRAPH_DIRECTION**
: Default graph direction: downstream or upstream (default: downstream). Equivalent magus.yaml key: **graph.direction**.

**MAGUS_GRAPH_SPELL**
: Filter graph output to a single spell. Equivalent magus.yaml key: **graph.spell**.

**MAGUS_GRAPH_DEPTH**
: Cap displayed graph depth (0 = unlimited) (default: 0). Equivalent magus.yaml key: **graph.depth**.

**MAGUS_GRAPH_ROOTS**
: Comma-separated starting nodes for graph traversal. Equivalent magus.yaml key: **graph.roots**.

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

**MAGUS_DAEMON_ADDRESS**
: Adopt-server socket as a unix:// URL; empty auto-generates a per-process socket. Equivalent magus.yaml key: **daemon.address**.

**MAGUS_DAEMON_IDLE_TTL**
: Idle workspace eviction TTL for the multi-workspace daemon; e.g. "6h", "30m" (default: 6h). Equivalent magus.yaml key: **daemon.idle_ttl**.

**MAGUS_DAEMON_WORKSPACES**
: Colon-separated list of workspace roots the daemon will serve; non-empty list triggers eager union of sandbox policies and rejection of out-of-list workspaces (MGS2010). Equivalent magus.yaml key: **daemon.workspaces**.

**MAGUS_ASSUME_INTERACTIVE**
: When 1 or true, assume an interactive terminal even if detection says otherwise (default: false). Equivalent magus.yaml key: **assume_interactive**.

**MAGUS_INTERPRETER_LUA_ENGINE**
: Select the Lua scripting backend: luajit (cgo) or gopherlua (pure-Go); empty picks the best compiled-in engine. Equivalent magus.yaml key: **interpreter.lua.engine**.

**MAGUS_MCP_ENABLED**
: When 0 or false, refuse to start the MCP server even when the binary was built with -tags mcp (default: true). Equivalent magus.yaml key: **mcp.enabled**.

**MAGUS_MCP_ADDRESS**
: host:port for the MCP Streamable HTTP server started alongside the daemon (default: 127.0.0.1:7391). Equivalent magus.yaml key: **mcp.address**.

**MAGUS_HINTS_ENABLED**
: When false, suppress all hint messages printed to stderr (default: true). Equivalent magus.yaml key: **hints.enabled**.

**MAGUS_FLAKE_ENABLED**
: Master switch for flakiness detection and auto-retry; false disables all retry logic (default: true). Equivalent magus.yaml key: **flake.enabled**.

**MAGUS_FLAKE_BOOTSTRAP_SAMPLES**
: Number of outcomes below which all failures are retried once (bootstrap phase) (default: 20). Equivalent magus.yaml key: **flake.bootstrap_samples**.

**MAGUS_FLAKE_MIN_SAMPLES**
: Minimum outcomes required before Wilson-score flake rate gates retry decisions (default: 20). Equivalent magus.yaml key: **flake.min_samples**.

**MAGUS_FLAKE_THRESHOLD**
: Wilson lower-bound flake rate above which a project+target is considered flaky (default: 0.05). Equivalent magus.yaml key: **flake.threshold**.

**MAGUS_FLAKE_ANNOTATE_GHA**
: When true, emit ::warning annotations and flake summary to $GITHUB_STEP_SUMMARY (default: true). Equivalent magus.yaml key: **flake.annotate_gha**.

**MAGUS_REPORT_FILTER**
: Comma-separated +type/-type terms restricting JSONL event emission (e.g. -graph.build,-graph.query). Equivalent magus.yaml key: **report.filter**.

**MAGUS_SANDBOX_ENABLED**
: When 1 or true, confine every subprocess and in-process spell to the workspace + a curated allowlist, scrub the child-process env to a minimum allowlist, and refuse paths outside it. See magus.yaml sandbox.allow and sandbox.env.passthrough for extension (default: false). Equivalent magus.yaml key: **sandbox.enabled**.

## Files

**magus.yaml**, **.magus.yaml**
: Configuration file. Searched in CWD, workspace root, and
$XDG_CONFIG_HOME/magus/ in ascending priority order. Both plain and
dot-prefixed names are accepted; having both in the same directory is an error.

**.magus-cache/**
: Content-addressed build cache in the workspace root. Override with
MAGUS_CACHE_DIR.

## See Also

[**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-tail**(1)](magus-tail.md), [**magus-affected**(1)](magus-affected.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-server**(1)](magus-server.md), [**magus-completion**(1)](magus-completion.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

