# magus: agent guidance

How an AI agent should drive a magus workspace. A **reference template** — copy the relevant parts into your project's `AGENTS.md` or `CLAUDE.md`.

## Detecting magus

Run `magus doctor` and read the `MCP server` line. `daemon is running; MCP available at http://…/mcp` means use the MCP tools below; otherwise fall back to the CLI. Don't assume MCP is up — check, or handle the connection error.

## Prefer MCP when the daemon is up

Structured tool responses beat scraping CLI text: results are JSON, run tools return per-project event streams (status, duration, exit code, log path), and runs share the daemon's concurrency pool. When MCP is down, shell out to the equivalent CLI command.

## Tools

| Tool                     | Use it to                                                                     |
| ------------------------ | ----------------------------------------------------------------------------- |
| `magus_list_projects`    | List projects (path, spell, dep count). Call first to learn the layout.       |
| `magus_list_targets`     | List a project's targets — confirm one exists before running it.              |
| `magus_where`            | Resolve a fuzzy/partial name to an absolute path.                             |
| `magus_describe_project` | Explain why a project is (or isn't) in the affected set; show graph position. |
| `magus_run_target`       | Run one target for named projects (see params below).                         |
| `magus_run_affected`     | Run a target on all VCS-changed projects (`magus affected <target>`).         |
| `magus_doctor`           | Validate workspace health (ok/warn/fail per check).                           |
| `magus_status`           | Inspect the live pool: active slots, queue depth, cache stats.                |
| `magus_affected_plan`    | Emit a CI shard plan for the affected set.                                    |
| `magus_config_get`       | Read the resolved config (read-only).                                         |
| `magus_tail_log`         | Fetch the last run's stdout/stderr — call after a failed run.                 |

**`magus_run_target` params:** `target` is a target name (`build`, `test`, `lint`, …) or an op-direct `spell::op` form (e.g. `go::go-test`, `typescript::eslint`). Projects go in the **separate** `projects` param (space-separated paths; `/` for all; omit for cwd-scope). Add `dry_run: true` to preview, `write: true` to let `format`/`generate` mutate files.

## Behavior

- **Announce before you act.** Say what run tool you're about to invoke; the daemon logs a matching `[MCP] llm=… tool=…` banner the human can correlate.
- **`dry_run` uncertain side effects.** For deploy/release/publish-style targets, preview with `dry_run: true` and show the plan first.
- **Don't mutate config via MCP** (not exposed). Point the user at `magus config set key=<key>,value=<value>`.
- **Fall back gracefully** to the CLI if MCP is unavailable or a call fails.

## Security

Reaching the MCP endpoint equals shell access to the workspace. It requires a **bearer token** (`Authorization: Bearer <token>`, 401 without): the daemon auto-generates one on first start, stored `0600` at `$XDG_CONFIG_HOME/magus/mcp_token` and managed via `magus config mcp token {print,generate,revoke}`. The token is defense-in-depth on top of the `127.0.0.1` bind and `Host`/`Origin` validation (403 on non-loopback, blocks browser DNS-rebinding) — none of which protects a deliberately exposed port. Treat the token as a local secret; do **not** expose the port over Tailscale, ngrok, SSH forwards, or any network with untrusted hosts. If asked, decline and suggest SSH access to the CLI instead.
