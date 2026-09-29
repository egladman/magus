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

## Is the loopback MCP endpoint reachable?

For a host configured with the loopback URL, `magus status` reports the HTTP
endpoint's live health as its own block, checked independently of the server's
socket. This block does not describe a host using stdio or the server's Unix socket:

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
magus status --probe=mcp             # fail if loopback HTTP MCP is unreachable
magus status --probe=liveness,mcp    # fail if the server OR the endpoint is down
```

The server also serves `/livez`, `/readyz`, and `/healthz` on the same port. If `state` is
`unreachable` even though you expect a server, see
[Keeping the server running](server.md#keeping-the-server-running).

With `mcp.enabled: false` the server still keeps the knowledge graph and symbol indexes
current; turning off MCP turns off the endpoint and nothing else.

## Which transport when

The same tools answer on each transport. What differs is who can reach them and what
proves who they are:

| Transport                 | Where                                    | Credential                                  | Use it for                                                                                          |
| ------------------------- | ---------------------------------------- | ------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| stdio                     | `magus mcp`, launched by the host        | none; the host launched the process as you  | one host driving the workspace it opens, with no server running                                     |
| Streamable HTTP, socket   | `/mcp` on the server's own `server.sock` | the kernel's word that the peer runs as you | a local client that speaks HTTP over a unix socket and should share the server's warm graph         |
| Streamable HTTP, loopback | `http://127.0.0.1:7391/mcp`              | a bearer token holding `mcp=write`          | a client that only takes a URL, runs as another user, or reaches the server through a tunnel or TLS |

Whichever the transport, each tool call is held to `mcp=write` again, and a caller
below it gets [MGS9015](../../reference/codes/auth/MGS9015.md) as a tool error.

## MCP over the server socket

The server has one unix socket, `server.sock` in the private (`0700`) runtime directory,
and it speaks HTTP. `/mcp` is one path on it, beside the Connect APIs and the control
operations the CLI uses (see [the server's socket](server.md#two-transports)):

```text
$XDG_RUNTIME_DIR/magus/server.sock      # else <user cache dir>/magus/run/, else /tmp/magus-<uid>/
```

It is Streamable HTTP, like the loopback endpoint, carried over the socket instead of TCP,
and it takes no token. What admits a caller is the user it runs as: for every connection
the server asks the kernel for the peer's uid (`SO_PEERCRED` on Linux, `LOCAL_PEERCRED` on
macOS) and admits only its own. Any other peer, root included, gets `403`
[MGS9022](../../reference/codes/auth/MGS9022.md). An admitted call carries the
`socket-peer` credential, which holds `mcp=write` and `console=write` but never token
management, and the activity trail records each call under it. On a platform where magus
cannot read a peer's uid, the socket carries the control operations alone and MCP stays
on loopback.

To check it by hand, send a handshake with curl:

```sh
curl --unix-socket "$XDG_RUNTIME_DIR/magus/server.sock" http://magus/mcp \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}'
```

`mcp.enabled: false` takes `/mcp` off the socket and off loopback alike.

## Available tools

Both transports expose one `client` tool for the `magus` module, plus the
operations no member of that module covers. That is the current inventory, not
a claim that every CLI command deserves an MCP twin. This list is authoritative
at the time of writing; `magus describe mcp-tools` (or `client` calling
`magus\describe` with the arguments of `magus describe mcp-tools`) prints the
live set with full parameters, so trust that over this table if they ever differ.

The catalog itself is generated from the `std.Magus` module descriptor, the same
declaration the Buzz bindings, the checker declarations and
[the `magus` module reference](../../reference/buzz/magus.md) come from. Declare an
`MCPTool` there to add one; nothing in the handler package is hand-listed.

### The boundary and the fallback

MCP is an agent-facing adapter to Magus's existing workspace operations. It
does not own a second graph, build engine, job store or VCS implementation.
Use a typed MCP tool when the host exposes it. If the tool is missing or its
call fails, use the corresponding CLI verb. The HTTP probe above cannot test
the host's stdio or Unix-socket connection. For a server-socket problem,
`magus status --probe=readiness` checks whether this workspace is loaded on
the socket, but cannot confirm the host registered MCP. The host owns the
connection; an agent should not start a server merely to unlock a tool.

| Need                                      | MCP                                                             | Disconnected fallback                                                |
| ----------------------------------------- | --------------------------------------------------------------- | -------------------------------------------------------------------- |
| Find and inspect workspace entities       | `client` (`magus\query`, `explain`, `path`, `refs`, `describe`) | `magus query`, `explain`, `path`, `refs`, `describe`                 |
| Run and inspect a target                  | `client` (`magus\run`, `magus\output`)                          | `magus run`, `magus affected`, `magus query output <ref>`            |
| Keep a decision or coordinate a job       | `client` (`magus\memory`, `magus\job`)                          | `magus memory`, `magus job`                                          |
| Transform data already supplied by a tool | `buzz`                                                          | `magus buzz` with explicit input; the CLI has a broader host surface |

`client` is the magus module. Define `main(args: [str])`, `import "magus"`, and
return a JSON-encodable value. The return is under `json`; `std.print` text is
under `stdout`. Call `magus\describeModule("magus")` for the signatures. Also
importable: `std`, `math`, `crypto`, `serialize`, `buffer`, and the WASM host
modules except `env`. File imports, native FFI, and `fs`, `proc`, `http`, `os`,
`net`, `vcs`, and `env` are refused with
[MGS3034](../../reference/codes/sandbox/MGS3034.md), and `magus\cmd` and
`magus\pry` are not offered. Each call runs in its own process. Bounded at 10
minutes when called directly; a host that supports MCP tasks can run it as a
task without that bound. Filter a large result in the script before returning
it.

`buzz` is deliberately not a back door to the Magus API. It receives
JSON, runs a small set of Buzz standard modules, and returns JSON. Query or
change the workspace through `client`, then pass that result as `input`.
`import "magus"` explains this boundary with
[MGS3033](../../reference/codes/sandbox/MGS3033.md). The CLI's full
`magus buzz` interpreter remains available for scripts that intentionally
need host modules, file imports or native FFI; do not treat it as a
capability-equivalent fallback for an untrusted transform.

Today each MCP connection is bound to one opened workspace. A future
multi-workspace orchestrator should own several explicit workspace clients and
derive a scoped client for each delegated job. It should not add a `root`
parameter to every tool call or let `buzz` import a privileged client:
those would make a scoped call silently cross into another workspace.

The tools beside `client` keep distinct jobs: transforming supplied JSON
(`buzz`), the live pool (`status`), the resolved config
(`config`), review collaboration (`diff`), and a local console
link (`console`). The client does not offer `magus\cmd`. A new MCP tool should
expose an existing domain operation that an agent cannot use well through
the current tools. Do not add a second implementation or a thin alias for a
CLI spelling.

The magus module, through `client`:

| Call                                              | Purpose                                                                                    |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| `magus\describe`                                  | Describe a concept and list its entities: spells, targets, projects, workspaces, mcp_tools |
| `magus\describeFile`                              | Classify paths against declared globs: owning project and role                             |
| `magus\run`                                       | Run a target for one or more projects, with the same arguments as `magus run`              |
| `magus\clean`                                     | Remove declared outputs. Arguments are `magus clean`'s                                     |
| `magus\where`                                     | Which project contains a directory                                                         |
| `magus\affected`                                  | The affected project set                                                                   |
| `magus\impact`                                    | The blast radius: why each project is in that set                                          |
| `magus\output`                                    | Fetch one target execution's captured output by its `out...` ref                           |
| `magus\doctor`                                    | Validate workspace health (config, cache, cycles, tool availability)                       |
| `magus\insight`                                   | One report: hotspots, affinity, ownership, trend, volatility, unreferenced                 |
| `magus\query`, `explain`, `path`, `refs`, `stats` | Search the graph, one node, a path, a symbol's references, and the graph's shape           |
| `magus\memory`                                    | User-owned per-repo memory shared across worktrees                                         |
| `magus\job`                                       | The orchestrating agent's declared jobs; magus never enforces them                         |
| `magus\vcs.checkpoint`                            | The working state's identity: revision, branch, dirty, patch digest; writes nothing        |

Example:

```json
{"script": "import \"magus\"; fun main(args: [str]) > any !> str { return magus\\query(\"kind=spell\"); }"}
```

Operations no member covers:

| Tool      | Purpose                                                                                                           |
| --------- | ----------------------------------------------------------------------------------------------------------------- |
| `buzz`    | Transform supplied JSON with Buzz and return a JSON value                                                         |
| `status`  | Report the live proc-server pool                                                                                  |
| `config`  | Read the resolved workspace config (read-only)                                                                    |
| `diff`    | Join the review session a person has open. `magus\diff` reads the working tree and does not write to that session |
| `console` | Return a tokenless link to a local console surface when the user asks to see it                                   |

`buzz` transforms a result from another MCP tool without a shell. Pass that
result as `input`; `transform(input: any, args: [str])` returns the JSON value to
send back. For example:

```json
{
  "script": "fun transform(input: any, args: [str]) > any { return input; }",
  "args": ["spell"],
  "input": {"matches": [{"kind": "spell", "id": "spell:go"}]}
}
```

The reply carries the returned value under `json`. Text emitted through
`std\print` is captured separately under `stdout`:

```json
{"stdout":"","json":{"matches":[{"kind":"spell","id":"spell:go"}]}}
```

A compile or runtime error is a tool error carrying a diagnostic. This MCP
interpreter offers `std`, `math`, `crypto`, `serialize` and `buffer`, but no
file imports, native FFI or Magus host modules. An `import "magus"` error
points to the `client` tool: use that for workspace queries and
actions, then pass its result to `buzz`. The regular `magus buzz`
CLI retains its full host surface. Each MCP transform has a 30-second limit.

`diff` joins the review session a person has open: `op=state` (default)
returns the annotated changeset, and `comment`, `suggest`, and `resolve` write
to it, addressed by workspace-relative path and 0-based hunk digest.

`console` does not open a browser or hand a client a token. A compatible
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

- **Cursor** owns its MCP client config. `magus describe harness cursor`
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
  `harness_mcp`; `magus describe harness` prints a host CLI command sketch
  and/or a docs pointer only - Magus does not write host MCP client config.
  Resolve the ref with `magus\secret.read("MAGUS_MCP_TOKEN")` inside a
  magusfile when a spell needs the value; hosts read the env var directly.

  Clients whose connector UI only speaks OAuth (no static-header option) reach a
  loopback server through the `mcp-remote` stdio bridge:
  `npx -y mcp-remote http://127.0.0.1:7391/mcp --header "Authorization: Bearer <token>"`.

- **The Claude API "MCP connector"** cannot reach this server: it requires a
  public `https://` URL and rejects `http://` and loopback addresses. Front the
  server with a TLS tunnel first if you need that path.

The [server socket](#mcp-over-the-server-socket) reads no token; the kernel's report of
the peer's uid stands in for it, so anything running as you reaches it, just as it
could read your operator token.

Treat the token as **defense in depth**, and still keep the port closed. The server binds to `127.0.0.1` by default, refuses any other address without `mcp.insecure_bind: true`, and validates the `Host` and `Origin` headers on every `/mcp` request, returning `403 Forbidden` for non-loopback values to block browser-based DNS-rebinding attacks. Anyone who reads the token gains the same workspace access, so keep it local.

**Do not expose it over:**

- Tailscale, Zerotier, or similar overlay networks where other devices can reach it
- ngrok, localtunnel, or other public tunnels
- SSH `-L` port-forwards shared with others
- Kubernetes `port-forward` in shared clusters
- Any network ACL that admits untrusted hosts

If you need to drive magus remotely, run the CLI over SSH instead.
