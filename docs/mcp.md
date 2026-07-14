---
title: MCP
description: The magus daemon exposes a Model Context Protocol server over Streamable HTTP so AI agents and IDE plugins can call build tools directly.
tags: [mcp, model-context-protocol, ai, agents, claude, cursor, daemon, ide]
---

# MCP: driving magus from agents

When the daemon is running, it also exposes an **MCP (Model Context Protocol) server** over Streamable HTTP. Agents and IDE plugins that speak MCP (Claude Desktop, Cursor, VS Code Copilot, and others) can call magus tools directly instead of shelling out.

Magus targets humans first. MCP is always compiled in; it is a runtime layer you turn off with `mcp.enabled=false` (see [Enabling and disabling](#enabling-and-disabling)) when you do not want it.

For the full agent surface built on top of MCP - the installable skills, `MAGUS.md` routing, durable memory, and the drift check - see [Agents](agents.md).

## Starting the daemon starts MCP

You don't need a separate process. Start the daemon as usual:

```sh
magus server start
```

The MCP endpoint comes up alongside it:

```text
http://127.0.0.1:7391/mcp
```

`magus doctor` reports whether MCP is reachable and prints the endpoint URL.

## Is MCP actually reachable?

An agent host connects to the MCP endpoint over HTTP; nothing starts that endpoint on
its own, so if the daemon is not running the tools silently disappear from the host.
`magus status` reports the endpoint's live health as its own block, checked independently
of the daemon's job socket:

```text
mcp endpoint
  url    http://127.0.0.1:7391/mcp
  state  serving
```

The `state` is one of:

| state         | meaning                                                              |
| ------------- | ------------------------------------------------------------------- |
| `serving`     | listening and a workspace is loaded - the tools are reachable       |
| `not-ready`   | listening, but no workspace is loaded yet                           |
| `unreachable` | nothing is listening; start the daemon with `magus server start`    |
| `disabled`    | turned off by `mcp.enabled=false`                                   |

For scripts and container probes, `magus status --probe=<kind>` exits `0` healthy / `1`
unhealthy. The kinds are `liveness` (the daemon answers), `readiness` (a workspace is
loaded), and `mcp` (this endpoint is reachable) - and they are comma-combinable, failing
if any listed check does:

```sh
magus status --probe=mcp             # fail if the tools are unreachable
magus status --probe=liveness,mcp    # fail if the daemon OR the endpoint is down
```

The daemon also serves `/livez`, `/readyz`, and `/healthz` on the same port. If `state` is
`unreachable` even though you expect a daemon, see
[Keeping the daemon running](daemon.md#keeping-the-daemon-running).

## Available tools

The daemon exposes 20 tools. This list is authoritative at the time of writing;
`magus describe mcp-tools` (or the `magus_describe` tool with `kind: mcp_tools`) prints
the live set with full parameters, so trust that over this table if they ever differ.

Discover:

| Tool                 | Purpose                                                                            |
| -------------------- | --------------------------------------------------------------------------------- |
| `magus_describe`     | Describe a concept and list its entities: spells, targets, projects, workspaces, mcp_tools (pass `name` for one entity's detail) |
| `magus_describe_file`| Classify paths against declared globs: owning project, and output (generated) vs source |
| `magus_where`        | Resolve a fuzzy project name to its absolute path                                  |
| `magus_config_get`   | Read the resolved workspace config (read-only)                                     |

Run:

| Tool                    | Purpose                                                          |
| ----------------------- | ---------------------------------------------------------------- |
| `magus_run_target`      | Run a target (`build`, `test`, `lint`, `ci`, ...) for one or more projects |
| `magus_run_affected`    | Run a target on only the VCS-affected projects                   |
| `magus_affected_plan`   | Emit a provider-neutral CI shard plan for the affected set       |
| `magus_affected_explain`| Explain why a project is in the affected set                     |

Inspect:

| Tool             | Purpose                                                             |
| ---------------- | ------------------------------------------------------------------ |
| `magus_doctor`   | Validate workspace health (config, cache, cycles, tool availability) |
| `magus_status`   | Report telemetry/cache settings and the live proc-server pool state |
| `magus_tail_log` | Return the most recent captured build log for a project            |
| `magus_output`   | Fetch one target execution's exact captured output by its `out...` ref |
| `magus_insight`  | VCS-history lenses: hotspots, files, affinity, ownership, trend    |

Knowledge graph:

| Tool          | Purpose                                                                  |
| ------------- | ------------------------------------------------------------------------ |
| `magus_query` | Search the graph and return ranked matches plus their neighborhood       |
| `magus_explain`| Show one node's data, edges with provenance, and how many nodes reach it |
| `magus_path`  | Shortest path between two nodes: how two entities relate                  |
| `magus_refs`  | Where a code symbol is defined and every file that references it (SCIP)   |
| `magus_stats` | Graph shape: god nodes, orphans, doc coverage                            |

Memory and scratch:

| Tool               | Purpose                                                                         |
| ------------------ | ------------------------------------------------------------------------------- |
| `magus_memory`     | Durable per-repo `status` / `progress` / `decisions` files, shared across sessions |
| `magus_scratchpad` | Private per-workspace scratch file for the agent's intermediate notes            |

Config mutation is not exposed over MCP. Use the CLI for `magus config set` and related commands.

## Enabling and disabling

MCP is on by default. To disable it:

```yaml
# magus.yaml
mcp:
  enabled: false
```

Or set `MAGUS_MCP_ENABLED=0` in the environment before starting the daemon.

To change the listen address:

```yaml
# magus.yaml
mcp:
  address: "127.0.0.1:9000"
```

Or `MAGUS_MCP_ADDRESS=127.0.0.1:9000`.

## Security: keep this local

> **Warning:** Reaching the MCP endpoint is equivalent to having shell access to your build workspace. Any authenticated caller can execute arbitrary build targets, which in turn invoke arbitrary toolchain commands defined in your magusfiles.

The endpoint requires a **bearer token**, and accepts two kinds:

- **The cli token** - a single, retrievable secret the daemon generates on first start and stores `0600` at `$XDG_STATE_HOME/magus/mcp_token` (`~/.local/state/magus/mcp_token`). magus's own commands reuse it (for example `graph open --live`). The secret never reaches the daemon log, so retrieve it with `magus config mcp token print`.
- **Connector tokens** - named, hashed-at-rest, expiring secrets you mint per external client (a Claude connector, an IDE). Only their SHA-256 is stored, so a connector token is shown once at creation and can never be re-displayed; rotate by minting a new one.

Every `/mcp` request must carry `Authorization: Bearer <token>` with either kind; requests without a valid token get `401 Unauthorized`. Manage them with:

```text
magus config mcp token print                     # show the cli token
magus config mcp token generate                  # mint a new cli token (--force to rotate)
magus config mcp token revoke                     # delete it (daemon mints a fresh one on next start)

magus config mcp connector create --name claude   # mint a connector token (prints the secret once)
magus config mcp connector create --expires 30d    # override the default 90-day expiry (or "never")
magus config mcp connector list                    # names, fingerprints, and expiry
magus config mcp connector revoke <name|fingerprint>
```

The token must be presented in the `Authorization` header; the `/mcp` endpoint
does not accept a token in the URL query string (RFC 6750 keeps secrets out of
logs and history). How you connect depends on the client:

- **Claude Code** connects to the loopback endpoint directly with a header. Mint
  a connector token, then register the server at `user` scope so every workspace
  the daemon serves shares one connection (the daemon binds one loopback port for
  all of them):

  ```text
  magus config mcp connector create --name claude-code --expires never
  claude mcp add --transport http --scope user magus http://127.0.0.1:7391/mcp \
    --header "Authorization: Bearer <token>"
  ```

  `claude mcp list` should then report `magus ... - Connected`. **Restart the
  Claude Code session** afterward: a session only discovers MCP tools (and skills
  installed by `magus agent install claude`) at launch, so an already-open session
  will not see them until it is restarted.

- **Claude Desktop / other IDE plugins** that take a Streamable-HTTP URL plus
  headers use the same shape:

  ```json
  {
    "type": "streamable-http",
    "url": "http://127.0.0.1:7391/mcp",
    "headers": { "Authorization": "Bearer <token>" }
  }
  ```

  Clients whose connector UI only speaks OAuth (no static-header option) reach a
  loopback server through the `mcp-remote` stdio bridge:
  `npx -y mcp-remote http://127.0.0.1:7391/mcp --header "Authorization: Bearer <token>"`.

- **The Claude API "MCP connector"** cannot reach this server: it requires a
  public `https://` URL and rejects `http://` and loopback addresses. Front the
  daemon with a TLS tunnel first if you need that path.

Treat the token as **defense in depth**, and still keep the port closed. The server binds to `127.0.0.1` by default and validates the `Host` and `Origin` headers on every `/mcp` request, returning `403 Forbidden` for non-loopback values to block browser-based DNS-rebinding attacks. Anyone who reads the token gains the same workspace access, so keep it local.

**Do not expose it over:**

- Tailscale, Zerotier, or similar overlay networks where other devices can reach it
- ngrok, localtunnel, or other public tunnels
- SSH `-L` port-forwards shared with others
- Kubernetes `port-forward` in shared clusters
- Any network ACL that admits untrusted hosts

If you need to drive magus remotely, run the CLI over SSH instead.
