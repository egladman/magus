---
title: magus.yaml configuration
description: Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.
tags: [config, magus.yaml, configuration, environment variables, flags, reference]
---

# Configuration

magus resolves configuration from three layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, then the `magus.yaml` file at the workspace root. This page is the complete inventory of config keys, each with its `magus.yaml` path, environment variable, CLI flag, value type, and built-in default.

## cache

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `cache.dir` | `MAGUS_CACHE_DIR` | `--cache-dir` | string | - |
| `cache.immutable` | `MAGUS_CACHE_IMMUTABLE` | `--cache-immutable` | bool | `false` |
| `cache.remote.insecure` | `MAGUS_CACHE_REMOTE_INSECURE` | `--cache-remote-insecure` | bool | `false` |
| `cache.remote.trusted_keys` | `MAGUS_CACHE_REMOTE_TRUSTED_KEYS` | _(env only)_ | list _(comma-separated, env only)_ | - |
| `cache.size_mb` | `MAGUS_CACHE_SIZE_MB` | `--cache-size-mb` | int | `0` |

## ci

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `ci.max_shards` | `MAGUS_CI_MAX_SHARDS` | `--ci-max-shards` | int | `8` |
| `ci.runner_pool_budget` | `MAGUS_CI_RUNNER_POOL_BUDGET` | `--ci-runner-pool-budget` | int | `0` |

## console

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `console.enabled` | `MAGUS_CONSOLE_ENABLED` | _(env only)_ | bool _(env only)_ | `true` |
| `console.url` | `MAGUS_CONSOLE_URL` | `--console-url` | string | - |

## daemon

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `daemon.address` | `MAGUS_DAEMON_ADDRESS` | `--daemon-address` | string | - |
| `daemon.enabled` | `MAGUS_DAEMON_ENABLED` | `--daemon-enabled` | bool | `true` |
| `daemon.idle_ttl` | `MAGUS_DAEMON_IDLE_TTL` | `--daemon-idle-ttl` | duration | `6h` |
| `daemon.maintenance.rotate_activities` | `MAGUS_DAEMON_MAINTENANCE_ROTATE_ACTIVITIES` | `--daemon-maintenance-rotate-activities` | duration | - |
| `daemon.maintenance.rotate_logs` | `MAGUS_DAEMON_MAINTENANCE_ROTATE_LOGS` | `--daemon-maintenance-rotate-logs` | duration | - |
| `daemon.maintenance.sync_graph` | `MAGUS_DAEMON_MAINTENANCE_SYNC_GRAPH` | `--daemon-maintenance-sync-graph` | duration | - |
| `daemon.socket` | `MAGUS_DAEMON_SOCKET` | `--daemon-socket` | string | - |
| `daemon.workspaces` | `MAGUS_DAEMON_WORKSPACES` | _(env only)_ | list _(comma-separated, env only)_ | - |

## general

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `concurrency` | `MAGUS_CONCURRENCY` | `-j`, `--concurrency` | int | `min(NumCPU,8)` |
| `default_charms` | `MAGUS_DEFAULT_CHARMS` | _(env only)_ | list _(comma-separated, env only)_ | - |
| `dry_run` | `MAGUS_DRY_RUN` | `-u`, `--dry-run` | bool | `false` |
| `history_path` | `MAGUS_HISTORY_PATH` | `--history-path` | string | `$XDG_STATE_HOME/magus/history/v1.json` |
| `requires_magus` | `MAGUS_REQUIRES_MAGUS` | `--requires-magus` | string | - |
| `target_timeout` | `MAGUS_TARGET_TIMEOUT` | `--target-timeout` | duration | - |

## graph

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `graph.depth` | `MAGUS_GRAPH_DEPTH` | `--graph-depth` | int | `0` |
| `graph.direction` | `MAGUS_GRAPH_DIRECTION` | `--graph-direction` | string | `downstream` |
| `graph.roots` | `MAGUS_GRAPH_ROOTS` | `--graph-roots` | string | - |
| `graph.spell` | `MAGUS_GRAPH_SPELL` | `--graph-spell` | string | - |

## hints

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `hints.enabled` | `MAGUS_HINTS_ENABLED` | _(env only)_ | bool _(env only)_ | `true` |

## knowledge

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `knowledge.max_size_mb` | `MAGUS_KNOWLEDGE_MAX_SIZE_MB` | `--knowledge-max-size-mb` | int | - |
| `knowledge.symbol_indexing.disabled` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_DISABLED` | `--knowledge-symbol-indexing-disabled` | bool | - |
| `knowledge.symbol_indexing.min_interval_seconds` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_MIN_INTERVAL_SECONDS` | `--knowledge-symbol-indexing-min-interval-seconds` | int | - |
| `knowledge.symbol_indexing.quiet_seconds` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_QUIET_SECONDS` | `--knowledge-symbol-indexing-quiet-seconds` | int | - |
| `knowledge.vcs.authorship` | `MAGUS_KNOWLEDGE_VCS_AUTHORSHIP` | _(env only)_ | bool _(env only)_ | - |
| `knowledge.vcs.enabled` | `MAGUS_KNOWLEDGE_VCS_ENABLED` | `--knowledge-vcs-enabled` | bool | - |
| `knowledge.vcs.max_commits` | `MAGUS_KNOWLEDGE_VCS_MAX_COMMITS` | `--knowledge-vcs-max-commits` | int | - |
| `knowledge.workspaces` | `MAGUS_KNOWLEDGE_WORKSPACES` | _(env only)_ | list _(comma-separated, env only)_ | - |

## log

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `log.format` | `MAGUS_LOG_FORMAT` | `--log-format` | string | `pretty` |
| `log.level` | `MAGUS_LOG_LEVEL` | `--log-level` | string | `info` |
| `log.silent` | `MAGUS_LOG_SILENT` | _(env only)_ | bool _(env only)_ | - |
| `log.stream` | `MAGUS_LOG_STREAM` | _(env only)_ | bool _(env only)_ | - |

## mcp

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `mcp.address` | `MAGUS_MCP_ADDRESS` | `--mcp-address` | string | `127.0.0.1:7391` |
| `mcp.enabled` | `MAGUS_MCP_ENABLED` | _(env only)_ | bool _(env only)_ | `true` |

## report

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `report.filter` | `MAGUS_REPORT_FILTER` | _(env only)_ | list _(comma-separated, env only)_ | - |

## sandbox

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `sandbox.enabled` | `MAGUS_SANDBOX_ENABLED` | `--sandbox-enabled` | bool | `false` |
| `sandbox.env.passthrough` | `MAGUS_SANDBOX_ENV_PASSTHROUGH` | _(env only)_ | list _(comma-separated, env only)_ | - |

## secret

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `secret.interactive_timeout` | `MAGUS_SECRET_INTERACTIVE_TIMEOUT` | `--secret-interactive-timeout` | duration | - |
| `secret.unattended_timeout` | `MAGUS_SECRET_UNATTENDED_TIMEOUT` | `--secret-unattended-timeout` | duration | - |

## telemetry

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `telemetry.enabled` | `MAGUS_TELEMETRY_ENABLED` | `--telemetry-enabled` | bool | `false` |
| `telemetry.endpoint` | `MAGUS_TELEMETRY_ENDPOINT` | `--telemetry-endpoint` | string | - |
| `telemetry.headers` | _(yaml only)_ | _(yaml only)_ | map _(yaml only)_ | - |
| `telemetry.insecure` | `MAGUS_TELEMETRY_INSECURE` | `--telemetry-insecure` | bool | `false` |
| `telemetry.protocol` | `MAGUS_TELEMETRY_PROTOCOL` | `--telemetry-protocol` | string | `grpc` |
| `telemetry.sample_ratio` | `MAGUS_TELEMETRY_SAMPLE_RATIO` | `--telemetry-sample-ratio` | float | `1.0` |
| `telemetry.service_name` | `MAGUS_TELEMETRY_SERVICE_NAME` | `--telemetry-service-name` | string | `magus` |

## vcs

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `vcs.base_ref` | `MAGUS_VCS_BASE_REF` | `--vcs-base-ref` | string | - |
| `vcs.enabled` | `MAGUS_VCS_ENABLED` | _(env only)_ | bool _(env only)_ | `true` |
| `vcs.name` | `MAGUS_VCS_NAME` | `--vcs-name` | string | - |

## volatility

| Config key | Environment variable | Flag | Type | Default |
|------------|----------------------|------|------|---------|
| `volatility.annotate_gha` | `MAGUS_VOLATILITY_ANNOTATE_GHA` | `--volatility-annotate-gha` | bool | `true` |
| `volatility.bootstrap_samples` | `MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES` | `--volatility-bootstrap-samples` | int | `20` |
| `volatility.enabled` | `MAGUS_VOLATILITY_ENABLED` | `--volatility-enabled` | bool | `true` |
| `volatility.min_samples` | `MAGUS_VOLATILITY_MIN_SAMPLES` | `--volatility-min-samples` | int | `20` |
| `volatility.threshold` | `MAGUS_VOLATILITY_THRESHOLD` | `--volatility-threshold` | float | `0.05` |

## See also

- [magus config](manpage/magus-config.md): the CLI verb that reads and writes these keys.
- [Workspace and projects](../concepts/workspace.md): where `magus.yaml` sits in workspace discovery.
