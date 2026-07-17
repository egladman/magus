---
title: magus.yaml configuration
description: Every magus.yaml config key with its MAGUS_* environment variable, CLI flag, and type. Generated from the config schema.
tags: [config, magus.yaml, configuration, environment variables, flags, reference]
---

# Configuration

magus resolves configuration from three layers, highest precedence first: a CLI flag, a `MAGUS_*` environment variable, then the `magus.yaml` file at the workspace root. This page is the complete inventory of config keys, each with its `magus.yaml` path, environment variable, CLI flag, and value type.

## cache

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `cache.dir` | `MAGUS_CACHE_DIR` | `--cache-dir` | string |
| `cache.immutable` | `MAGUS_CACHE_IMMUTABLE` | `--cache-immutable` | bool |
| `cache.remote.insecure` | `MAGUS_CACHE_REMOTE_INSECURE` | `--cache-remote-insecure` | bool |
| `cache.remote.trusted_keys` | `MAGUS_CACHE_REMOTE_TRUSTED_KEYS` | _(env only)_ | list _(comma-separated, env only)_ |
| `cache.size_mb` | `MAGUS_CACHE_SIZE_MB` | `--cache-size-mb` | int |

## ci

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `ci.max_shards` | `MAGUS_CI_MAX_SHARDS` | `--ci-max-shards` | int |
| `ci.runner_pool_budget` | `MAGUS_CI_RUNNER_POOL_BUDGET` | `--ci-runner-pool-budget` | int |

## console

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `console.enabled` | `MAGUS_CONSOLE_ENABLED` | _(env only)_ | bool _(env only)_ |
| `console.url` | `MAGUS_CONSOLE_URL` | `--console-url` | string |

## daemon

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `daemon.address` | `MAGUS_DAEMON_ADDRESS` | `--daemon-address` | string |
| `daemon.enabled` | `MAGUS_DAEMON_ENABLED` | `--daemon-enabled` | bool |
| `daemon.idle_ttl` | `MAGUS_DAEMON_IDLE_TTL` | `--daemon-idle-ttl` | duration |
| `daemon.socket` | `MAGUS_DAEMON_SOCKET` | `--daemon-socket` | string |
| `daemon.workspaces` | `MAGUS_DAEMON_WORKSPACES` | _(env only)_ | list _(comma-separated, env only)_ |

## general

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `assume_interactive` | `MAGUS_ASSUME_INTERACTIVE` | `--assume-interactive` | bool |
| `concurrency` | `MAGUS_CONCURRENCY` | `-j`, `--concurrency` | int |
| `default_charms` | `MAGUS_DEFAULT_CHARMS` | _(env only)_ | list _(comma-separated, env only)_ |
| `dry_run` | `MAGUS_DRY_RUN` | `-u`, `--dry-run` | bool |
| `history_path` | `MAGUS_HISTORY_PATH` | `--history-path` | string |

## graph

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `graph.depth` | `MAGUS_GRAPH_DEPTH` | `--graph-depth` | int |
| `graph.direction` | `MAGUS_GRAPH_DIRECTION` | `--graph-direction` | string |
| `graph.roots` | `MAGUS_GRAPH_ROOTS` | `--graph-roots` | string |
| `graph.spell` | `MAGUS_GRAPH_SPELL` | `--graph-spell` | string |

## hints

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `hints.enabled` | `MAGUS_HINTS_ENABLED` | _(env only)_ | bool _(env only)_ |

## knowledge

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `knowledge.max_size_mb` | `MAGUS_KNOWLEDGE_MAX_SIZE_MB` | `--knowledge-max-size-mb` | int |
| `knowledge.symbol_indexing.disabled` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_DISABLED` | `--knowledge-symbol-indexing-disabled` | bool |
| `knowledge.symbol_indexing.min_interval_seconds` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_MIN_INTERVAL_SECONDS` | `--knowledge-symbol-indexing-min-interval-seconds` | int |
| `knowledge.symbol_indexing.quiet_seconds` | `MAGUS_KNOWLEDGE_SYMBOL_INDEXING_QUIET_SECONDS` | `--knowledge-symbol-indexing-quiet-seconds` | int |
| `knowledge.vcs.authorship` | `MAGUS_KNOWLEDGE_VCS_AUTHORSHIP` | _(env only)_ | bool _(env only)_ |
| `knowledge.vcs.enabled` | `MAGUS_KNOWLEDGE_VCS_ENABLED` | `--knowledge-vcs-enabled` | bool |
| `knowledge.vcs.max_commits` | `MAGUS_KNOWLEDGE_VCS_MAX_COMMITS` | `--knowledge-vcs-max-commits` | int |
| `knowledge.workspaces` | `MAGUS_KNOWLEDGE_WORKSPACES` | _(env only)_ | list _(comma-separated, env only)_ |

## log

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `log.format` | `MAGUS_LOG_FORMAT` | `--log-format` | string |
| `log.level` | `MAGUS_LOG_LEVEL` | `--log-level` | string |
| `log.silent` | `MAGUS_LOG_SILENT` | _(env only)_ | bool _(env only)_ |

## mcp

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `mcp.address` | `MAGUS_MCP_ADDRESS` | `--mcp-address` | string |
| `mcp.enabled` | `MAGUS_MCP_ENABLED` | _(env only)_ | bool _(env only)_ |

## report

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `report.filter` | `MAGUS_REPORT_FILTER` | _(env only)_ | list _(comma-separated, env only)_ |

## sandbox

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `sandbox.enabled` | `MAGUS_SANDBOX_ENABLED` | `--sandbox-enabled` | bool |
| `sandbox.env.passthrough` | `MAGUS_SANDBOX_ENV_PASSTHROUGH` | _(env only)_ | list _(comma-separated, env only)_ |

## telemetry

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `telemetry.enabled` | `MAGUS_TELEMETRY_ENABLED` | `--telemetry-enabled` | bool |
| `telemetry.endpoint` | `MAGUS_TELEMETRY_ENDPOINT` | `--telemetry-endpoint` | string |
| `telemetry.insecure` | `MAGUS_TELEMETRY_INSECURE` | `--telemetry-insecure` | bool |
| `telemetry.protocol` | `MAGUS_TELEMETRY_PROTOCOL` | `--telemetry-protocol` | string |
| `telemetry.sample_ratio` | `MAGUS_TELEMETRY_SAMPLE_RATIO` | `--telemetry-sample-ratio` | float |
| `telemetry.service_name` | `MAGUS_TELEMETRY_SERVICE_NAME` | `--telemetry-service-name` | string |

## vcs

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `vcs.base_ref` | `MAGUS_VCS_BASE_REF` | `--vcs-base-ref` | string |
| `vcs.enabled` | `MAGUS_VCS_ENABLED` | _(env only)_ | bool _(env only)_ |
| `vcs.name` | `MAGUS_VCS_NAME` | `--vcs-name` | string |

## volatility

| Config key | Environment variable | Flag | Type |
|------------|----------------------|------|------|
| `volatility.annotate_gha` | `MAGUS_VOLATILITY_ANNOTATE_GHA` | `--volatility-annotate-gha` | bool |
| `volatility.bootstrap_samples` | `MAGUS_VOLATILITY_BOOTSTRAP_SAMPLES` | `--volatility-bootstrap-samples` | int |
| `volatility.enabled` | `MAGUS_VOLATILITY_ENABLED` | `--volatility-enabled` | bool |
| `volatility.min_samples` | `MAGUS_VOLATILITY_MIN_SAMPLES` | `--volatility-min-samples` | int |
| `volatility.threshold` | `MAGUS_VOLATILITY_THRESHOLD` | `--volatility-threshold` | float |

