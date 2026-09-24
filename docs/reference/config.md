---
title: magus.yaml configuration
generated_from: internal/config/config.go
description: Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.
tags: [config, magus.yaml, configuration, environment variables, flags, reference]
---

# Configuration

magus resolves configuration from three layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, then the `magus.yaml` file at the workspace root. This page is the complete inventory of config keys, each with its `magus.yaml` path, environment variable, CLI flag, and value type.

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

## ci

| Config key              | Environment variable          | Flag                      | Type |
| ----------------------- | ----------------------------- | ------------------------- | ---- |
| `ci.max_shards`         | `MAGUS_CI_MAX_SHARDS`         | `--ci-max-shards`         | int  |
| `ci.record_runs`        | `MAGUS_CI_RECORD_RUNS`        | `--ci-record-runs`        | bool |
| `ci.runner_pool_budget` | `MAGUS_CI_RUNNER_POOL_BUDGET` | `--ci-runner-pool-budget` | int  |

## console

| Config key        | Environment variable    | Flag         | Type              |
| ----------------- | ----------------------- | ------------ | ----------------- |
| `console.enabled` | `MAGUS_CONSOLE_ENABLED` | _(env only)_ | bool _(env only)_ |

## diff

| Config key | Environment variable | Flag         | Type              |
| ---------- | -------------------- | ------------ | ----------------- |
| `diff.tui` | `MAGUS_DIFF_TUI`     | _(env only)_ | bool _(env only)_ |

## general

| Config key            | Environment variable        | Flag                    | Type                               |
| --------------------- | --------------------------- | ----------------------- | ---------------------------------- |
| `broker`              | `MAGUS_BROKER`              | `--broker`              | string                             |
| `concurrency`         | `MAGUS_CONCURRENCY`         | `-j`, `--concurrency`   | int                                |
| `concurrency_profile` | `MAGUS_CONCURRENCY_PROFILE` | `--concurrency-profile` | string                             |
| `default_charms`      | `MAGUS_DEFAULT_CHARMS`      | _(env only)_            | list _(comma-separated, env only)_ |
| `dry_run`             | `MAGUS_DRY_RUN`             | `-u`, `--dry-run`       | bool                               |
| `history_path`        | `MAGUS_HISTORY_PATH`        | `--history-path`        | string                             |
| `max_failures`        | `MAGUS_MAX_FAILURES`        | `--max-failures`        | int                                |
| `shutdown_grace`      | `MAGUS_SHUTDOWN_GRACE`      | `--shutdown-grace`      | duration                           |
| `stall_timeout`       | `MAGUS_STALL_TIMEOUT`       | `--stall-timeout`       | duration                           |
| `target_timeout`      | `MAGUS_TARGET_TIMEOUT`      | `--target-timeout`      | duration                           |

## hints

| Config key      | Environment variable  | Flag         | Type              |
| --------------- | --------------------- | ------------ | ----------------- |
| `hints.enabled` | `MAGUS_HINTS_ENABLED` | _(env only)_ | bool _(env only)_ |

## jobs

| Config key             | Environment variable         | Flag                     | Type     |
| ---------------------- | ---------------------------- | ------------------------ | -------- |
| `jobs.default_timeout` | `MAGUS_JOBS_DEFAULT_TIMEOUT` | `--jobs-default-timeout` | duration |
| `jobs.max_depth`       | `MAGUS_JOBS_MAX_DEPTH`       | `--jobs-max-depth`       | int      |
| `jobs.max_live`        | `MAGUS_JOBS_MAX_LIVE`        | `--jobs-max-live`        | int      |
| `jobs.stale_after`     | `MAGUS_JOBS_STALE_AFTER`     | `--jobs-stale-after`     | duration |

## knowledge

| Config key                                       | Environment variable                                   | Flag                                               | Type                               |
| ------------------------------------------------ | ------------------------------------------------------ | -------------------------------------------------- | ---------------------------------- |
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
| `knowledge.workspaces`                           | `MAGUS_KNOWLEDGE_WORKSPACES`                           | _(env only)_                                       | list _(comma-separated, env only)_ |

## log

| Config key   | Environment variable | Flag           | Type              |
| ------------ | -------------------- | -------------- | ----------------- |
| `log.format` | `MAGUS_LOG_FORMAT`   | `--log-format` | string            |
| `log.level`  | `MAGUS_LOG_LEVEL`    | `--log-level`  | string            |
| `log.silent` | `MAGUS_LOG_SILENT`   | _(env only)_   | bool _(env only)_ |
| `log.stream` | `MAGUS_LOG_STREAM`   | _(env only)_   | bool _(env only)_ |

## mcp

| Config key          | Environment variable      | Flag                  | Type              |
| ------------------- | ------------------------- | --------------------- | ----------------- |
| `mcp.address`       | `MAGUS_MCP_ADDRESS`       | `--mcp-address`       | string            |
| `mcp.enabled`       | `MAGUS_MCP_ENABLED`       | _(env only)_          | bool _(env only)_ |
| `mcp.insecure_bind` | `MAGUS_MCP_INSECURE_BIND` | `--mcp-insecure-bind` | bool              |

## report

| Config key      | Environment variable  | Flag         | Type                               |
| --------------- | --------------------- | ------------ | ---------------------------------- |
| `report.filter` | `MAGUS_REPORT_FILTER` | _(env only)_ | list _(comma-separated, env only)_ |

## sandbox

| Config key                | Environment variable            | Flag                | Type                               |
| ------------------------- | ------------------------------- | ------------------- | ---------------------------------- |
| `sandbox.enabled`         | `MAGUS_SANDBOX_ENABLED`         | `--sandbox-enabled` | bool                               |
| `sandbox.env.passthrough` | `MAGUS_SANDBOX_ENV_PASSTHROUGH` | _(env only)_        | list _(comma-separated, env only)_ |

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

## telemetry

| Config key               | Environment variable           | Flag                       | Type   |
| ------------------------ | ------------------------------ | -------------------------- | ------ |
| `telemetry.enabled`      | `MAGUS_TELEMETRY_ENABLED`      | `--telemetry-enabled`      | bool   |
| `telemetry.endpoint`     | `MAGUS_TELEMETRY_ENDPOINT`     | `--telemetry-endpoint`     | string |
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

