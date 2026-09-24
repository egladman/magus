---
title: MCP
description: magus serves its tools as a Model Context Protocol server, over stdio for the agent host that launches magus mcp, or over Streamable HTTP from magus server.
tags: [mcp, model-context-protocol, ai, agents, claude, codex, cursor, server, ide, stdio]
aliases: [guides/mcp]
---

# MCP

magus serves its tools as an **MCP (Model Context Protocol) server**, so agents and IDE plugins that speak MCP (Claude Desktop, Cursor, VS Code Copilot, and others) can call them directly instead of shelling out. There are two ways to reach it:

- **stdio** (`magus mcp`): the host launches magus and talks to it over its stdin and stdout. It opens the workspace it is launched in and needs no server and no token. Start here.
- **Streamable HTTP** (`magus server`): one long-lived server at `http://127.0.0.1:7391/mcp` that several clients share, authenticated with a bearer token.

Both serve the same tools. magus prints what a host needs (`magus mcp --help`) and never writes a host's config file; the snippets below are for you to place.

For the full agent surface built on top of MCP - the installable skills, `MAGUS.md` routing, durable memory, and the drift check - see [Agents](agents.md).

## stdio: the host launches magus

Register magus with your MCP client as a stdio server. Most clients take a command and its arguments:

```json
{
  "command": "magus",
  "args": ["mcp"]
}
```

The host starts `magus mcp` in the workspace it opens, and it serves that workspace until the host closes stdin. Stdout carries only protocol frames; logs, and one line saying what is being served, go to stderr, which most hosts keep as the server's log.

There is no token to mint. The caller is the local process the host started, running as you, so every tool call is admitted with the `stdio` credential, which holds `mcp=write` and nothing past it: the same grant a connector token holds (see [Tokens and grants](../../concepts/tokens.md)). The activity trail records each call with that credential and the client's name.

To check the wiring by hand, pipe a handshake in:

```sh
printf '%s\n' \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | magus mcp
```

Each line of output is one JSON-RPC reply. Typed at a terminal with nothing piped in, `magus mcp` waits for a host to speak; press Ctrl+C to stop it.

A host that launches one process per workspace gets one `magus mcp` per workspace. When several clients should share one warm server instead, use `magus server`.

A stdio server that runs a target asks the [broker](server.md) for host capacity like any other run.

## Streamable HTTP: the server serves MCP

When `magus server` is running, it also exposes the MCP server over Streamable HTTP. MCP is always compiled in; it is a runtime layer you turn off with `mcp.enabled=false` (see [Enabling and disabling](#enabling-and-disabling)) when you do not want it.

You don't need a separate process. Start the server as usual:

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
its own, so if the server is not running the tools silently disappear from the host.
`magus status` reports the endpoint's live health as its own block, checked independently
of the server's job socket:

```text
mcp endpoint
  url    http://127.0.0.1:7391/mcp
  state  serving
```

The `state` is one of:

| state         | meaning                                                          |
| ------------- | ---------------------------------------------------------------- |
| `serving`     | listening and a workspace is loaded - the tools are reachable    |
| `not-ready`   | listening, but no workspace is loaded yet                        |
| `unreachable` | nothing is listening; start the server with `magus server start` |
| `disabled`    | turned off by `mcp.enabled=false`                                |

For scripts and container probes, `magus status --probe=<kind>` exits `0` healthy / `1`
unhealthy. The kinds are `liveness` (the server answers), `readiness` (a workspace is
loaded), and `mcp` (this endpoint is reachable) - and they are comma-combinable, failing
if any listed check does:

```sh
magus status --probe=mcp             # fail if the tools are unreachable
magus status --probe=liveness,mcp    # fail if the server OR the endpoint is down
```

The server also serves `/livez`, `/readyz`, and `/healthz` on the same port. If `state` is
`unreachable` even though you expect a server, see
[Keeping the server running](server.md#keeping-the-server-running).

With `mcp.enabled: false` the server still keeps the knowledge graph and symbol indexes
current; turning off MCP turns off the endpoint and nothing else.

## Available tools

Both transports expose these tools. This list is authoritative at the time of writing;
`magus describe mcp-tools` (or the `magus_describe` tool with `kind: mcp_tools`) prints
the live set with full parameters, so trust that over this table if they ever differ.

The catalog itself is generated from the `std.Magus` module descriptor, the same
declaration the Buzz bindings, the checker declarations and
[the `magus` module reference](../../reference/buzz/magus.md) come from. Declare an
`MCPTool` there to add one; nothing in the handler package is hand-listed.

Discover:

| Tool                  | Purpose                                                                                                                             |
| --------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `magus_describe`      | Describe a concept and list its entities: spells, targets, projects, workspaces, mcp_tools (pass `name` for one entity's detail)    |
| `magus_describe_file` | Classify paths against declared globs: owning project, per-target claims, dependency edges, and declarations covering several paths |
| `magus_where`         | Resolve a fuzzy project name to its absolute path                                                                                   |
| `magus_config_get`    | Read the resolved workspace config (read-only)                                                                                      |

Run:

| Tool                     | Purpose                                                                    |
| ------------------------ | -------------------------------------------------------------------------- |
| `magus_run_target`       | Run a target (`build`, `test`, `lint`, `ci`, ...) for one or more projects |
| `magus_run_affected`     | Run a target on only the VCS-affected projects                             |
| `magus_affected_plan`    | Emit a provider-neutral CI shard plan for the affected set                 |
| `magus_affected_explain` | Explain why a project is in the affected set                               |

Inspect:

| Tool                   | Purpose                                                                                     |
| ---------------------- | ------------------------------------------------------------------------------------------- |
| `magus_doctor`         | Validate workspace health (config, cache, cycles, tool availability)                        |
| `magus_status`         | Report telemetry/cache settings and the live proc-server pool state                         |
| `magus_output`         | Fetch one target execution's exact captured output by its `out...` ref                      |
| `magus_insight`        | Lenses: hotspots, files, affinity, ownership, trend, unreferenced                           |
| `magus_vcs_checkpoint` | Resolve the working state's identity: revision, branch, dirty, patch digest; writes nothing |

Knowledge graph:

| Tool            | Purpose                                                                  |
| --------------- | ------------------------------------------------------------------------ |
| `magus_query`   | Search the graph and return ranked matches plus their neighborhood       |
| `magus_explain` | Show one node's data, edges with provenance, and how many nodes reach it |
| `magus_path`    | Shortest path between two nodes: how two entities relate                 |
| `magus_refs`    | Where a code symbol is defined and every file that references it (SCIP)  |
| `magus_stats`   | Graph shape: god nodes, orphans, doc coverage                            |

Review:

| Tool         | Purpose                                                                                                                                                                                                                                 |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `magus_diff` | Join the review session a person has open and pair with them on it: `op=state` (default) returns the annotated changeset, `comment`, `suggest`, and `resolve` write to it, addressed by workspace-relative path and 0-based hunk digest |

Memory and scratch:

| Tool           | Purpose                                                                                                                   |
| -------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `magus_memory` | User-owned per-repo memory: list/get/put/delete/verify named entries shared across worktrees                              |
| `magus_job`    | The orchestrating agent's declared jobs (list/fork/exec/exit/wait), recorded for humans to see; magus never enforces them |

Console:

| Tool                    | Purpose                                                                                                 |
| ----------------------- | ------------------------------------------------------------------------------------------------------- |
| `magus_console_present` | Return a tokenless link to a local console surface when the user asks to see dashboard status or output |

`magus_console_present` does not open a browser or hand a client a token. A compatible
desktop client may render its link as an action. Other clients can return the link as text.
Its `open` field is a shell command that opens the link signed in, for example
`open "http://127.0.0.1:7391/console/dashboard/#code=$(magus config console token create --code --expires 12h)"`;
the code is a substitution the person's shell expands, so it never appears in the reply,
and it is a one-time code the console trades within a minute for a console token that
expires in 12 hours, never the operator token. The guard refuses that command to an
agent session, so a person runs it.
The console must be enabled and bound locally.

Config mutation is not exposed over MCP. Use the CLI for `magus config set` and related commands.

## Enabling and disabling

MCP is on by default. To disable it:

```yaml
# magus.yaml
mcp:
  enabled: false
```

Or set `MAGUS_MCP_ENABLED=0` in the environment before starting the server.

To change the listen address:

```yaml
# magus.yaml
mcp:
  address: "127.0.0.1:9000"
```

Or `MAGUS_MCP_ADDRESS=127.0.0.1:9000`.

A non-loopback address (`0.0.0.0:7391` for a Kubernetes health probe, say) sends
every bearer token in cleartext, so the server refuses to start on one unless you
also set `mcp.insecure_bind: true` (or `MAGUS_MCP_INSECURE_BIND=true`). Front such
a listener with TLS or a tunnel.

## Security: keep this local

> **Warning:** Reaching the MCP endpoint is equivalent to having shell access to your build workspace. Any authenticated caller can execute arbitrary build targets, which in turn invoke arbitrary toolchain commands defined in your magusfiles.

`magus mcp` opens no listener: only the process that launched it can reach it, through its pipes. Everything below is about the daemon's HTTP endpoint.

The endpoint requires a **bearer token** whose grant includes `mcp=write` (see
[Tokens and grants](../../concepts/tokens.md)). Two kinds hold it:

- **A connector token** (`mgs_...`) - a named, hashed-at-rest token you mint per external client (a Claude connector, an IDE). It holds `mcp=write` and nothing else. Only its SHA-256 is stored, so it is shown once at creation; rotate by minting a new one. It always expires: 90 days by default, at most 366.
- **The operator token** (`mgo_...`) - the one retrievable secret the server generates on first start and stores `0600` at `$XDG_STATE_HOME/magus/mcp_token`. It holds every surface, token management included, so give an MCP client a connector token instead. An agent session is denied `magus config token print` and `generate` by the guard.

Every `/mcp` request must carry `Authorization: Bearer <token>`. A request without one, or with a token that is wrong, expired or revoked, gets `401`; a valid token without `mcp=write` (a console token) gets `403` [MGS9015](../../reference/codes/auth/MGS9015.md). Manage connector tokens with:

```text
magus config mcp connector create --name claude   # mint one (prints the secret once)
magus config mcp connector create --expires 366d   # the longest a token lives; "never" is refused
magus config mcp connector ls                    # names, ids, grants, and expiry
magus config mcp connector revoke <name|id>
```

`magus doctor` warns 14 days before a stored token expires, so a client is
re-minted before it starts failing.

The token must be presented in the `Authorization` header; the `/mcp` endpoint
does not accept a token in the URL query string (RFC 6750 keeps secrets out of
logs and history). How you connect depends on the client:

- **Claude Code** connects to the loopback endpoint directly with a header. Mint
  a connector token, then register the server at `user` scope so every workspace
  the server serves shares one connection (the server binds one loopback port for
  all of them):

  ```text
  magus config mcp connector create --name claude-code --expires 366d
  claude mcp add --transport http --scope user magus http://127.0.0.1:7391/mcp \
    --header "Authorization: Bearer <token>"
  ```

  The token expires in a year; `magus doctor` names it two weeks before, and
  the fix is the same two commands with a new token. `claude mcp list` should
  then report `magus ... - Connected`. **Restart the
  Claude Code session** afterward: a session only discovers MCP tools (and skills
  installed by `magus agent install .claude/skills`) at launch, so an already-open session
  will not see them until it is restarted.

- **Cursor** owns its MCP client config. `magus agent harness apply --id cursor`
  prints a short setup hint and a docs pointer; it does not write
  `.cursor/mcp.json`. Register Magus under Settings -> Tools & MCP (or
  hand-write `.cursor/mcp.json` / `~/.cursor/mcp.json`) at
  `http://127.0.0.1:7391/mcp`, preferably with
  `Authorization: Bearer ${env:MAGUS_MCP_TOKEN}` so the secret stays out of the
  file. Export `MAGUS_MCP_TOKEN` where the Cursor GUI process inherits it
  (macOS Dock launches often miss shell-profile exports), then restart Cursor
  or toggle Magus under Tools & MCP. Confirm with Output -> MCP Logs and
  `magus status --probe=mcp`.

- **Codex** uses user-level `~/.codex/config.toml` to register the local
  Streamable HTTP endpoint. Do not commit this client configuration. It contains
  no secret; the token comes from the process environment:

  ```toml
  [mcp_servers.magus]
  url = "http://127.0.0.1:7391/mcp"
  bearer_token_env_var = "MAGUS_MCP_TOKEN"
  enabled = true
  ```

  For Codex CLI, start the server, mint a connector token and store it as
  `MAGUS_MCP_TOKEN` in your local secret manager (it is shown once), export it
  in the shell that will launch Codex, then check registration and endpoint
  health:

  ```sh
  magus server start
  magus config mcp connector create --name codex --expires 366d
  codex mcp list
  magus status --probe=liveness,mcp
  ```

  For the ChatGPT desktop app or Codex IDE extension, set the variable through
  the OS environment before launching or restarting the client; exporting it in
  a terminal does not configure an already-running app. Start a new task after
  the server comes up. `codex mcp list` confirms configuration, while
  `magus status --probe=liveness,mcp` confirms the endpoint is live. If you
  change `mcp.address`, update the URL in `~/.codex/config.toml` too. In the
  desktop app, `/mcp` shows connected servers. Install matching guidance with
  `magus agent install .agents/skills`, then paste the `AGENTS.md` block it
  prints; see [Codex](agents/codex.md) for why Codex wants both locations.

- **Claude Desktop / other IDE plugins** that take a Streamable-HTTP URL plus
  headers use the same shape:

  ```json
  {
    "type": "streamable-http",
    "url": "http://127.0.0.1:7391/mcp",
    "headers": { "Authorization": "Bearer <token>" }
  }
  ```

  Prefer binding the token through the workspace **secret provider** (built-in
  environment provider: the ref `MAGUS_MCP_TOKEN`) rather than pasting a
  plaintext secret into a committed file. Harness spells declare that ref via
  `harness_mcp`; `magus agent harness apply` prints a host CLI command sketch
  and/or a docs pointer only - Magus does not write host MCP client config.
  Resolve the ref with `magus\secret.read("MAGUS_MCP_TOKEN")` inside a
  magusfile when a spell needs the value; hosts read the env var directly.

  Clients whose connector UI only speaks OAuth (no static-header option) reach a
  loopback server through the `mcp-remote` stdio bridge:
  `npx -y mcp-remote http://127.0.0.1:7391/mcp --header "Authorization: Bearer <token>"`.

- **The Claude API "MCP connector"** cannot reach this server: it requires a
  public `https://` URL and rejects `http://` and loopback addresses. Front the
  server with a TLS tunnel first if you need that path.

Treat the token as **defense in depth**, and still keep the port closed. The server binds to `127.0.0.1` by default, refuses any other address without `mcp.insecure_bind: true`, and validates the `Host` and `Origin` headers on every `/mcp` request, returning `403 Forbidden` for non-loopback values to block browser-based DNS-rebinding attacks. Anyone who reads the token gains the same workspace access, so keep it local.

**Do not expose it over:**

- Tailscale, Zerotier, or similar overlay networks where other devices can reach it
- ngrok, localtunnel, or other public tunnels
- SSH `-L` port-forwards shared with others
- Kubernetes `port-forward` in shared clusters
- Any network ACL that admits untrusted hosts

If you need to drive magus remotely, run the CLI over SSH instead.
