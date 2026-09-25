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

**MAGUS_CACHE_REMOTE_INSECURE**
: Disable remote-cache signature verification (accept/produce unsigned artifacts); for trusted single-repo CI only. Requires cache.remote.insecure_reason (default: false). Equivalent magus.yaml key: **cache.remote.insecure**.

**MAGUS_CACHE_REMOTE_INSECURE_REASON**
: Why this cache runs unverified; required whenever cache.remote.insecure is true. Equivalent magus.yaml key: **cache.remote.insecure_reason**.

**MAGUS_LOG_FORMAT**
: Output format: pretty, plain, text, or json (default: pretty). Equivalent magus.yaml key: **log.format**.

**MAGUS_LOG_LEVEL**
: Minimum log level: trace, debug, info, warn, error (trace also prints the startup timing table) (default: info). Equivalent magus.yaml key: **log.level**.

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

**MAGUS_MCP_ENABLED**
: When 0 or false, refuse to start the MCP server (default: true). Equivalent magus.yaml key: **mcp.enabled**.

**MAGUS_MCP_ADDRESS**
: host:port for the MCP Streamable HTTP server \`magus server\` starts (default: 127.0.0.1:7391). Equivalent magus.yaml key: **mcp.address**.

**MAGUS_MCP_INSECURE_BIND**
: Permit a non-loopback mcp.address, which serves bearer tokens over plaintext HTTP; without it such an address is an error (default: false). Equivalent magus.yaml key: **mcp.insecure_bind**.

**MAGUS_HINTS_ENABLED**
: When false, suppress all hint messages printed to stderr (default: true). Equivalent magus.yaml key: **hints.enabled**.

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

**MAGUS_SANDBOX**
: off, best-effort, or required. On, magus scrubs child-process env to a minimum allowlist and refuses reads, writes and execs outside the workspace and a curated allowlist. Kernel landlock (Linux 5.13+) enforces that for every process; without it only magus's own bindings are checked, which best-effort accepts (MGS2005) and required refuses (MGS2012). See magus.yaml sandbox.allow and sandbox.env.passthrough for extension (default: off). Equivalent magus.yaml key: **sandbox.mode**.

**MAGUS_UPDATE_URL**
: Env-only, no magus.yaml equivalent: override the release index URL for \`magus self update\`; set to a self-hosted copy of index.json to use a private update channel (default: https://eli.gladman.cc/magus/public/release/index.json)

**MAGUS_NO_BOOTSTRAP_EXEC**
: Env-only, no magus.yaml equivalent: when 1, true or yes, disable the pre-workspace-load check that replaces this process with a workspace-local ./magus found by walking up from the working directory (or --root); set it to force the binary actually invoked to run instead, e.g. while debugging that binary itself (default: false)

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

