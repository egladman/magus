---
title: magus.yaml configuration
generated_from: internal/config/config.go
description: Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.
tags: [config, magus.yaml, configuration, environment variables, flags, reference]
---

# Configuration

magus resolves configuration from these layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, a `magus.yaml` in the working directory below the workspace root, the `magus.yaml` at the workspace root, then the user config at `$XDG_CONFIG_HOME/magus/magus.yaml`. This page is the inventory of config keys, each with its `magus.yaml` path, the environment variable and CLI flag that also set it, and its value type. A key a file alone sets says file only; `<name>` in a key stands for the name of a list entry.

## agents

| Config key                | Environment variable | Flag          | Type     |
| ------------------------- | -------------------- | ------------- | -------- |
| `agents.billing`          | _(file only)_        | _(file only)_ | string   |
| `agents.profile`          | _(file only)_        | _(file only)_ | string   |
| `agents.prompt_cache_ttl` | _(file only)_        | _(file only)_ | duration |

## cache

| Config key                     | Environment variable                 | Flag                             | Type                               |
| ------------------------------ | ------------------------------------ | -------------------------------- | ---------------------------------- |
| `cache.dir`                    | `MAGUS_CACHE_DIR`                    | `--cache-dir`                    | string                             |
| `cache.include.arch.enabled`   | `MAGUS_CACHE_INCLUDE_ARCH_ENABLED`   | _(env only)_                     | bool _(env only)_                  |
| `cache.include.os.enabled`     | `MAGUS_CACHE_INCLUDE_OS_ENABLED`     | _(env only)_                     | bool _(env only)_                  |
| `cache.remote.insecure`        | `MAGUS_CACHE_REMOTE_INSECURE`        | `--cache-remote-insecure`        | bool                               |
| `cache.remote.insecure_reason` | `MAGUS_CACHE_REMOTE_INSECURE_REASON` | `--cache-remote-insecure-reason` | string                             |
| `cache.remote.trusted_keys`    | `MAGUS_CACHE_REMOTE_TRUSTED_KEYS`    | _(env only)_                     | list _(comma-separated, env only)_ |
| `cache.remote.write.enabled`   | `MAGUS_CACHE_REMOTE_WRITE_ENABLED`   | _(env only)_                     | bool _(env only)_                  |
| `cache.size_mb`                | `MAGUS_CACHE_SIZE_MB`                | `--cache-size-mb`                | int                                |
| `cache.write.enabled`          | `MAGUS_CACHE_WRITE_ENABLED`          | _(env only)_                     | bool _(env only)_                  |

## checkout

| Config key           | Environment variable | Flag          | Type   |
| -------------------- | -------------------- | ------------- | ------ |
| `checkout.bootstrap` | _(file only)_        | _(file only)_ | string |

## ci

| Config key              | Environment variable          | Flag                      | Type   |
| ----------------------- | ----------------------------- | ------------------------- | ------ |
| `ci.machine.cores`      | `MAGUS_CI_MACHINE_CORES`      | `--ci-machine-cores`      | int    |
| `ci.machine.memory_mb`  | `MAGUS_CI_MACHINE_MEMORY_MB`  | `--ci-machine-memory-mb`  | int    |
| `ci.machine.name`       | `MAGUS_CI_MACHINE_NAME`       | `--ci-machine-name`       | string |
| `ci.max_shards`         | `MAGUS_CI_MAX_SHARDS`         | `--ci-max-shards`         | int    |
| `ci.record_runs`        | `MAGUS_CI_RECORD_RUNS`        | `--ci-record-runs`        | bool   |
| `ci.remote_gate`        | `MAGUS_CI_REMOTE_GATE`        | `--ci-remote-gate`        | string |
| `ci.runner_pool_budget` | `MAGUS_CI_RUNNER_POOL_BUDGET` | `--ci-runner-pool-budget` | int    |

## console

| Config key                  | Environment variable              | Flag                          | Type              |
| --------------------------- | --------------------------------- | ----------------------------- | ----------------- |
| `console.enabled`           | `MAGUS_CONSOLE_ENABLED`           | _(env only)_                  | bool _(env only)_ |
| `console.pulls.active`      | `MAGUS_CONSOLE_PULLS_ACTIVE`      | `--console-pulls-active`      | duration          |
| `console.pulls.floor`       | `MAGUS_CONSOLE_PULLS_FLOOR`       | `--console-pulls-floor`       | duration          |
| `console.pulls.idle`        | `MAGUS_CONSOLE_PULLS_IDLE`        | `--console-pulls-idle`        | duration          |
| `console.pulls.reserve`     | `MAGUS_CONSOLE_PULLS_RESERVE`     | `--console-pulls-reserve`     | int               |
| `console.pulls.runner_wait` | `MAGUS_CONSOLE_PULLS_RUNNER_WAIT` | `--console-pulls-runner-wait` | duration          |
| `console.pulls.stale_after` | `MAGUS_CONSOLE_PULLS_STALE_AFTER` | `--console-pulls-stale-after` | duration          |

## diagnostics

| Config key           | Environment variable | Flag          | Type   |
| -------------------- | -------------------- | ------------- | ------ |
| `diagnostics.docs`   | _(file only)_        | _(file only)_ | string |
| `diagnostics.prefix` | _(file only)_        | _(file only)_ | string |

## diff

| Config key | Environment variable | Flag         | Type              |
| ---------- | -------------------- | ------------ | ----------------- |
| `diff.tui` | `MAGUS_DIFF_TUI`     | _(env only)_ | bool _(env only)_ |

## disk

| Config key                | Environment variable            | Flag                        | Type     |
| ------------------------- | ------------------------------- | --------------------------- | -------- |
| `disk.critical_free_mb`   | `MAGUS_DISK_CRITICAL_FREE_MB`   | `--disk-critical-free-mb`   | int      |
| `disk.growth_mb_per_hour` | `MAGUS_DISK_GROWTH_MB_PER_HOUR` | `--disk-growth-mb-per-hour` | int      |
| `disk.min_free_mb`        | `MAGUS_DISK_MIN_FREE_MB`        | `--disk-min-free-mb`        | int      |
| `disk.sample_every`       | `MAGUS_DISK_SAMPLE_EVERY`       | `--disk-sample-every`       | duration |

## general

| Config key            | Environment variable        | Flag                    | Type                               |
| --------------------- | --------------------------- | ----------------------- | ---------------------------------- |
| `broker`              | `MAGUS_BROKER`              | `--broker`              | string                             |
| `budget_wait`         | `MAGUS_BUDGET_WAIT`         | `--budget-wait`         | duration                           |
| `concurrency`         | `MAGUS_CONCURRENCY`         | `-j`, `--concurrency`   | int                                |
| `concurrency_profile` | `MAGUS_CONCURRENCY_PROFILE` | `--concurrency-profile` | string                             |
| `default_charms`      | `MAGUS_DEFAULT_CHARMS`      | _(env only)_            | list _(comma-separated, env only)_ |
| `dry_run`             | `MAGUS_DRY_RUN`             | `-u`, `--dry-run`       | bool                               |
| `history_path`        | `MAGUS_HISTORY_PATH`        | `--history-path`        | string                             |
| `max_failures`        | `MAGUS_MAX_FAILURES`        | `--max-failures`        | int                                |
| `required_version`    | _(file only)_               | _(file only)_           | string                             |
| `shell_integration`   | `MAGUS_SHELL_INTEGRATION`   | `--shell-integration`   | string                             |
| `shutdown_grace`      | `MAGUS_SHUTDOWN_GRACE`      | `--shutdown-grace`      | duration                           |
| `stall_timeout`       | `MAGUS_STALL_TIMEOUT`       | `--stall-timeout`       | duration                           |
| `target_timeout`      | `MAGUS_TARGET_TIMEOUT`      | `--target-timeout`      | duration                           |

## hints

| Config key      | Environment variable  | Flag         | Type              |
| --------------- | --------------------- | ------------ | ----------------- |
| `hints.enabled` | `MAGUS_HINTS_ENABLED` | _(env only)_ | bool _(env only)_ |

## jobs

| Config key                | Environment variable            | Flag                        | Type     |
| ------------------------- | ------------------------------- | --------------------------- | -------- |
| `jobs.default_timeout`    | `MAGUS_JOBS_DEFAULT_TIMEOUT`    | `--jobs-default-timeout`    | duration |
| `jobs.max_claim_files`    | `MAGUS_JOBS_MAX_CLAIM_FILES`    | `--jobs-max-claim-files`    | int      |
| `jobs.max_claim_projects` | `MAGUS_JOBS_MAX_CLAIM_PROJECTS` | `--jobs-max-claim-projects` | int      |
| `jobs.max_claims`         | `MAGUS_JOBS_MAX_CLAIMS`         | `--jobs-max-claims`         | int      |
| `jobs.max_depth`          | `MAGUS_JOBS_MAX_DEPTH`          | `--jobs-max-depth`          | int      |
| `jobs.max_live`           | `MAGUS_JOBS_MAX_LIVE`           | `--jobs-max-live`           | int      |
| `jobs.max_validations`    | `MAGUS_JOBS_MAX_VALIDATIONS`    | `--jobs-max-validations`    | int      |
| `jobs.model_tier_matrix`  | _(file only)_                   | _(file only)_               | string   |
| `jobs.retain`             | `MAGUS_JOBS_RETAIN`             | `--jobs-retain`             | duration |
| `jobs.stale_after`        | `MAGUS_JOBS_STALE_AFTER`        | `--jobs-stale-after`        | duration |

## knowledge

| Config key                                       | Environment variable                                   | Flag                                               | Type                               |
| ------------------------------------------------ | ------------------------------------------------------ | -------------------------------------------------- | ---------------------------------- |
| `knowledge.conformance.exclude`                  | _(file only)_                                          | _(file only)_                                      | list _(comma-separated, env only)_ |
| `knowledge.conformance.gates.bar`                | _(file only)_                                          | _(file only)_                                      | float                              |
| `knowledge.conformance.gates.max_findings`       | _(file only)_                                          | _(file only)_                                      | int                                |
| `knowledge.conformance.gates.min_cohort`         | _(file only)_                                          | _(file only)_                                      | int                                |
| `knowledge.conformance.gates.min_share`          | _(file only)_                                          | _(file only)_                                      | float                              |
| `knowledge.conformance.min_sample`               | _(file only)_                                          | _(file only)_                                      | int                                |
| `knowledge.conformance.step`                     | _(file only)_                                          | _(file only)_                                      | float                              |
| `knowledge.duplication.include_tests`            | `MAGUS_KNOWLEDGE_DUPLICATION_INCLUDE_TESTS`            | `--knowledge-duplication-include-tests`            | bool                               |
| `knowledge.duplication.min_callees`              | `MAGUS_KNOWLEDGE_DUPLICATION_MIN_CALLEES`              | `--knowledge-duplication-min-callees`              | int                                |
| `knowledge.duplication.min_score`                | `MAGUS_KNOWLEDGE_DUPLICATION_MIN_SCORE`                | `--knowledge-duplication-min-score`                | float                              |
| `knowledge.duplication.min_shared`               | `MAGUS_KNOWLEDGE_DUPLICATION_MIN_SHARED`               | `--knowledge-duplication-min-shared`               | int                                |
| `knowledge.duplication.min_span_ratio`           | `MAGUS_KNOWLEDGE_DUPLICATION_MIN_SPAN_RATIO`           | `--knowledge-duplication-min-span-ratio`           | float                              |
| `knowledge.max_size_mb`                          | `MAGUS_KNOWLEDGE_MAX_SIZE_MB`                          | `--knowledge-max-size-mb`                          | int                                |
| `knowledge.notes.private`                        | `MAGUS_KNOWLEDGE_NOTES_PRIVATE`                        | `--knowledge-notes-private`                        | string                             |
| `knowledge.notes.shared`                         | `MAGUS_KNOWLEDGE_NOTES_SHARED`                         | `--knowledge-notes-shared`                         | string                             |
| `knowledge.published_ref`                        | `MAGUS_KNOWLEDGE_PUBLISHED_REF`                        | `--knowledge-published-ref`                        | string                             |
| `knowledge.sessions.disabled`                    | `MAGUS_KNOWLEDGE_SESSIONS_DISABLED`                    | `--knowledge-sessions-disabled`                    | bool                               |
| `knowledge.symbol_indexing.disabled`             | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_DISABLED`             | `--knowledge-symbol-indexing-disabled`             | bool                               |
| `knowledge.symbol_indexing.min_interval_seconds` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_MIN_INTERVAL_SECONDS` | `--knowledge-symbol-indexing-min-interval-seconds` | int                                |
| `knowledge.symbol_indexing.quiet_seconds`        | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_QUIET_SECONDS`        | `--knowledge-symbol-indexing-quiet-seconds`        | int                                |
| `knowledge.vcs.authorship`                       | `MAGUS_KNOWLEDGE_VCS_AUTHORSHIP`                       | _(env only)_                                       | bool _(env only)_                  |
| `knowledge.vcs.enabled`                          | `MAGUS_KNOWLEDGE_VCS_ENABLED`                          | `--knowledge-vcs-enabled`                          | bool                               |
| `knowledge.vcs.max_commits`                      | `MAGUS_KNOWLEDGE_VCS_MAX_COMMITS`                      | `--knowledge-vcs-max-commits`                      | int                                |
| `knowledge.workspaces.<name>.name`               | _(file only)_                                          | _(file only)_                                      | string                             |
| `knowledge.workspaces.<name>.path`               | _(file only)_                                          | _(file only)_                                      | string                             |

## log

| Config key   | Environment variable | Flag           | Type              |
| ------------ | -------------------- | -------------- | ----------------- |
| `log.format` | `MAGUS_LOG_FORMAT`   | `--log-format` | string            |
| `log.level`  | `MAGUS_LOG_LEVEL`    | `--log-level`  | string            |
| `log.loader` | `MAGUS_LOG_LOADER`   | `--log-loader` | string            |
| `log.silent` | `MAGUS_LOG_SILENT`   | _(env only)_   | bool _(env only)_ |
| `log.stream` | `MAGUS_LOG_STREAM`   | _(env only)_   | bool _(env only)_ |

## mcp

| Config key            | Environment variable        | Flag                  | Type                               |
| --------------------- | --------------------------- | --------------------- | ---------------------------------- |
| `mcp.address`         | `MAGUS_MCP_ADDRESS`         | `--mcp-address`       | string                             |
| `mcp.allowed_origins` | `MAGUS_MCP_ALLOWED_ORIGINS` | _(env only)_          | list _(comma-separated, env only)_ |
| `mcp.enabled`         | `MAGUS_MCP_ENABLED`         | _(env only)_          | bool _(env only)_                  |
| `mcp.http`            | `MAGUS_MCP_HTTP`            | _(env only)_          | bool _(env only)_                  |
| `mcp.insecure_bind`   | `MAGUS_MCP_INSECURE_BIND`   | `--mcp-insecure-bind` | bool                               |

## queue

| Config key              | Environment variable | Flag          | Type                               |
| ----------------------- | -------------------- | ------------- | ---------------------------------- |
| `queue.carry_approvals` | _(file only)_        | _(file only)_ | list _(comma-separated, env only)_ |

## report

| Config key      | Environment variable  | Flag         | Type                               |
| --------------- | --------------------- | ------------ | ---------------------------------- |
| `report.filter` | `MAGUS_REPORT_FILTER` | _(env only)_ | list _(comma-separated, env only)_ |

## sandbox

| Config key                      | Environment variable            | Flag          | Type                               |
| ------------------------------- | ------------------------------- | ------------- | ---------------------------------- |
| `sandbox.allow.<name>.base`     | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.bin`      | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.env`      | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.mode`     | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.name`     | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.path`     | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.allow.<name>.requires` | _(file only)_                   | _(file only)_ | string                             |
| `sandbox.env.passthrough`       | `MAGUS_SANDBOX_ENV_PASSTHROUGH` | _(env only)_  | list _(comma-separated, env only)_ |
| `sandbox.mode`                  | `MAGUS_SANDBOX`                 | `--sandbox`   | string                             |

## secret

| Config key                   | Environment variable               | Flag                           | Type     |
| ---------------------------- | ---------------------------------- | ------------------------------ | -------- |
| `secret.interactive_timeout` | `MAGUS_SECRET_INTERACTIVE_TIMEOUT` | `--secret-interactive-timeout` | duration |
| `secret.unattended_timeout`  | `MAGUS_SECRET_UNATTENDED_TIMEOUT`  | `--secret-unattended-timeout`  | duration |

## server

| Config key                             | Environment variable                         | Flag                                     | Type                               |
| -------------------------------------- | -------------------------------------------- | ---------------------------------------- | ---------------------------------- |
| `server.address`                       | `MAGUS_SERVER_ADDRESS`                       | `--server-address`                       | string                             |
| `server.enabled`                       | `MAGUS_SERVER_ENABLED`                       | `--server-enabled`                       | bool                               |
| `server.idle_ttl`                      | `MAGUS_SERVER_IDLE_TTL`                      | `--server-idle-ttl`                      | duration                           |
| `server.maintenance.check_review`      | `MAGUS_SERVER_MAINTENANCE_CHECK_REVIEW`      | `--server-maintenance-check-review`      | duration                           |
| `server.maintenance.prune_preserved`   | `MAGUS_SERVER_MAINTENANCE_PRUNE_PRESERVED`   | `--server-maintenance-prune-preserved`   | duration                           |
| `server.maintenance.rotate_activities` | `MAGUS_SERVER_MAINTENANCE_ROTATE_ACTIVITIES` | `--server-maintenance-rotate-activities` | duration                           |
| `server.maintenance.rotate_logs`       | `MAGUS_SERVER_MAINTENANCE_ROTATE_LOGS`       | `--server-maintenance-rotate-logs`       | duration                           |
| `server.maintenance.sync_graph`        | `MAGUS_SERVER_MAINTENANCE_SYNC_GRAPH`        | `--server-maintenance-sync-graph`        | duration                           |
| `server.workspaces`                    | `MAGUS_SERVER_WORKSPACES`                    | _(env only)_                             | list _(comma-separated, env only)_ |

## spells

| Config key                          | Environment variable           | Flag          | Type                               |
| ----------------------------------- | ------------------------------ | ------------- | ---------------------------------- |
| `spells.allow_shadow.<name>.name`   | _(file only)_                  | _(file only)_ | string                             |
| `spells.allow_shadow.<name>.reason` | _(file only)_                  | _(file only)_ | string                             |
| `spells.symbol_indexers`            | `MAGUS_SPELLS_SYMBOL_INDEXERS` | _(env only)_  | list _(comma-separated, env only)_ |

## telemetry

| Config key               | Environment variable           | Flag                       | Type   |
| ------------------------ | ------------------------------ | -------------------------- | ------ |
| `telemetry.enabled`      | `MAGUS_TELEMETRY_ENABLED`      | `--telemetry-enabled`      | bool   |
| `telemetry.endpoint`     | `MAGUS_TELEMETRY_ENDPOINT`     | `--telemetry-endpoint`     | string |
| `telemetry.headers`      | _(file only)_                  | _(file only)_              | map    |
| `telemetry.insecure`     | `MAGUS_TELEMETRY_INSECURE`     | `--telemetry-insecure`     | bool   |
| `telemetry.protocol`     | `MAGUS_TELEMETRY_PROTOCOL`     | `--telemetry-protocol`     | string |
| `telemetry.sample_ratio` | `MAGUS_TELEMETRY_SAMPLE_RATIO` | `--telemetry-sample-ratio` | float  |
| `telemetry.service_name` | `MAGUS_TELEMETRY_SERVICE_NAME` | `--telemetry-service-name` | string |

## vcs

| Config key     | Environment variable | Flag             | Type              |
| -------------- | -------------------- | ---------------- | ----------------- |
| `vcs.base_ref` | `MAGUS_VCS_BASE_REF` | `--vcs-base-ref` | string            |
| `vcs.enabled`  | `MAGUS_VCS_ENABLED`  | _(env only)_     | bool _(env only)_ |
| `vcs.name`     | `MAGUS_VCS_NAME`     | `--vcs-name`     | string            |

## volatility

| Config key                     | Environment variable                 | Flag                             | Type  |
| ------------------------------ | ------------------------------------ | -------------------------------- | ----- |
| `volatility.annotate_gha`      | `MAGUS_VOLATILITY_ANNOTATE_GHA`      | `--volatility-annotate-gha`      | bool  |
| `volatility.bootstrap_samples` | `MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES` | `--volatility-bootstrap-samples` | int   |
| `volatility.enabled`           | `MAGUS_VOLATILITY_ENABLED`           | `--volatility-enabled`           | bool  |
| `volatility.min_samples`       | `MAGUS_VOLATILITY_MIN_SAMPLES`       | `--volatility-min-samples`       | int   |
| `volatility.threshold`         | `MAGUS_VOLATILITY_THRESHOLD`         | `--volatility-threshold`         | float |

## work

| Config key        | Environment variable | Flag          | Type                               |
| ----------------- | -------------------- | ------------- | ---------------------------------- |
| `work.local_only` | _(file only)_        | _(file only)_ | list _(comma-separated, env only)_ |

## Environment only

No config key carries these variables.

| Environment variable        | Default                                                | Meaning                                                                                                                                                                                                                                                                                                                 |
| --------------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `MAGUS_CACHE_SIGNING_KEY`   |                                                        | Env-only, no magus.yaml equivalent: the Ed25519 seed that signs remote-cache writes (see `magus config cache-key`); env-only because a signing secret in a committed file is not a secret                                                                                                                               |
| `MAGUS_CACHE_TOOL_VERSION`  | project                                                | Env-only, no magus.yaml equivalent: how tool versions key the cache: project (per project), workspace (once for the workspace), or off                                                                                                                                                                                  |
| `MAGUS_VCS_<NAME>_BASE_REF` |                                                        | Per-VCS base-ref override, e.g. MAGUS_VCS_GIT_BASE_REF; dynamic pattern, read directly by package vcs                                                                                                                                                                                                                   |
| `MAGUS_SHARD`               |                                                        | CI matrix shard ID (e.g. "0"); equivalent to magus run --shard; set by .github/actions/magus                                                                                                                                                                                                                            |
| `MAGUS_N_SHARDS`            |                                                        | Total shard count for this matrix run; equivalent to magus run --n-shards; set by .github/actions/magus                                                                                                                                                                                                                 |
| `MAGUS_UPDATE_URL`          | https://eli.gladman.cc/magus/public/release/index.json | Env-only, no magus.yaml equivalent: override the release index URL for `magus self update`; set to a self-hosted copy of index.json to use a private update channel                                                                                                                                                     |
| `MAGUS_NO_BOOTSTRAP_EXEC`   | false                                                  | Env-only, no magus.yaml equivalent: when 1, true or yes, disable the pre-workspace-load check that replaces this process with a workspace-local ./magus found by walking up from the working directory (or --root); set it to force the binary actually invoked to run instead, e.g. while debugging that binary itself |
| `MAGUS_OFFLINE`             | false                                                  | Env-only, no magus.yaml equivalent: when set to anything but 0 or false, a remote-spell fetch and every std/http request a script sends fail with a named error instead of sending a request, and the lifecycle provider is not asked: `magus describe tools` replays its stored answer or reads unknown (offline)      |
| `MAGUS_DIFFTOOL`            | $DIFFTOOL, then git diff --no-index                    | Env-only, no magus.yaml equivalent: the command, taking two paths, that `--then file <path> diff` compares a cached artifact with                                                                                                                                                                                       |
| `MAGUS_LOG_VIEWER_URL`      | the hosted log viewer                                  | Env-only, no magus.yaml equivalent: base URL of the log viewer page a live run opens, for a self-hosted mirror                                                                                                                                                                                                          |
| `MAGUS_CONSOLE_DIR`         |                                                        | Env-only, no magus.yaml equivalent: directory of the built console the server serves at /console/ and the LAN share serves; unset serves none, and the workspace's own console/gen is never served unless this names it                                                                                                 |
| `MAGUS_PPROF`               |                                                        | Env-only, no magus.yaml equivalent: write Go profiles for one invocation, as kind:path pairs (cpu, mem, trace), comma separated                                                                                                                                                                                         |
| `MAGUS_MCP_TOKEN`           |                                                        | Env-only: the secret reference shipped harness spells name for the MCP bearer token; the environment secret provider reads it from this variable                                                                                                                                                                        |
| `MAGUS_S3_BUCKET`           |                                                        | Env-only: the bucket the aws/s3-cache spell stores remote-cache artifacts in                                                                                                                                                                                                                                            |
| `MAGUS_S3_ENDPOINT`         | https://s3.<region>.amazonaws.com                      | Env-only: the S3-compatible endpoint the aws/s3-cache spell talks to                                                                                                                                                                                                                                                    |

## Set by magus

magus sets these for the processes it spawns. Never set one by hand.

| Environment variable                | Meaning                                                                                                                                                                                                                                                                                               |
| ----------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `MAGUS_PROC_SOCKET`                 | The proc-server socket a magus process exports for the magus processes it spawns; unix:// URL or bare path, read directly by the process that adopts it                                                                                                                                               |
| `MAGUS_PROC_TOKEN`                  | The secret the MAGUS_PROC_SOCKET server requires on every request, exported beside it and inherited only by a nested magus                                                                                                                                                                            |
| `MAGUS_PROC_TREE`                   | The ids of every bounded process tree a process descends from, inherited by every child so a command's leftovers are found                                                                                                                                                                            |
| `MAGUS_STAMPED_LEASE`               | Set by a shared server on a script it forks for a remote caller, and by an authenticated MCP caller for a child magus: the BAGGAGE lease was stamped by the transport rather than claimed by the spawner. It only downgrades: a missing lease reads as unstamped instead of unbound, never as a grant |
| `MAGUS_MCP_BUZZ_PURE`               | Set by the buzz MCP tool for its forked transform worker                                                                                                                                                                                                                                              |
| `MAGUS_MCP_CLIENT`                  | Set by the client MCP tool for its forked worker                                                                                                                                                                                                                                                      |
| `MAGUS_MCP_CLIENT_HOST`             | Set by the client MCP tool: the calling client's name, stamped onto the worker's activity trail                                                                                                                                                                                                       |
| `MAGUS_MCP_CLIENT_LEASE`            | Set by the client MCP tool: the calling client's job lease, or empty when the transport stamped none                                                                                                                                                                                                  |
| `MAGUS_LEVEL`                       | The magus recursion depth, like make's MAKELEVEL                                                                                                                                                                                                                                                      |
| `MAGUS_INVOCATION_ANCESTORS`        | The comma-separated invocations a nested magus runs underneath                                                                                                                                                                                                                                        |
| `MAGUS_BOOTSTRAP_EXEC_DONE`         | Set when magus replaces itself with a workspace-local ./magus, so the replacement never hops again                                                                                                                                                                                                    |
| `MAGUS_SYMBOL_INDEX`                | Set for a spell's symbol indexer op: the path the indexer writes its SCIP index to                                                                                                                                                                                                                    |
| `MAGUS_WORKSPACE_ROOT`              | Set for a spell's symbol indexer op: the workspace root the index's paths are relative to                                                                                                                                                                                                             |
| `MAGUS_INTERNAL_ADVICE_MODE`        | Set by `magus diff` for the advice script it runs; the two halves of one feature, not a setting, and either may be renamed without notice                                                                                                                                                             |
| `MAGUS_INTERNAL_ADVICE_BASE_BRANCH` | Set by `magus diff` alongside MAGUS_INTERNAL_ADVICE_MODE; not a setting                                                                                                                                                                                                                               |
| `MAGUS_DETACHED_REFRESH`            | Set on the detached symbol refresh a graph read starts, so that process exits rather than queues behind another refresh                                                                                                                                                                               |

