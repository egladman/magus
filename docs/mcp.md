# MCP: driving magus from agents

When the daemon is running, it also exposes an **MCP (Model Context Protocol) server** over Streamable HTTP. Agents and IDE plugins that speak MCP (Claude Desktop, Cursor, VS Code Copilot, and others) can call magus tools directly instead of shelling out.

Magus targets humans first. MCP is an optional layer you can omit from the binary with the `!mcp` build tag.

## Starting the daemon starts MCP

No separate process is needed. Start the daemon as usual:

```sh
magus server start
```

The MCP endpoint comes up alongside it:

```text
http://127.0.0.1:7391/mcp
```

`magus doctor` reports whether MCP is reachable and prints the endpoint URL.

## Available tools

| Tool                     | Purpose                                                              |
| ------------------------ | -------------------------------------------------------------------- |
| `magus_list_projects`    | List all projects discovered in the workspace                        |
| `magus_list_targets`     | List registered build targets for a project                          |
| `magus_where`            | Resolve a fuzzy project name to its absolute path                    |
| `magus_describe_project` | Explain why a project is in the affected set                         |
| `magus_run_target`       | Run a target (`build`, `test`, `lint`, ...) for one or more projects |
| `magus_run_affected`     | Run a target for all VCS-changed projects                            |
| `magus_doctor`           | Validate workspace health                                            |
| `magus_status`           | Inspect the live concurrency pool                                    |
| `magus_affected_plan`    | Emit a CI shard plan for the affected set                            |
| `magus_config_get`       | Read the resolved workspace config (read-only)                       |
| `magus_tail_log`         | Retrieve the captured build log for a project                        |

Config mutation is not exposed over MCP. Use the CLI for `magus config set` and related commands.

## Enabling and disabling

MCP is on by default when the binary is built with `-tags mcp` (the default). To disable it without rebuilding:

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

The endpoint requires a **bearer token**. The daemon generates one on first start and stores it `0600` at `$XDG_CONFIG_HOME/magus/mcp_token`; the secret never reaches the daemon log, so retrieve it with `magus config mcp token print`. Every `/mcp` request must carry `Authorization: Bearer <token>`; requests without it get `401 Unauthorized`. Manage the token with:

```text
magus config mcp token print      # show the current token
magus config mcp token generate   # mint a new one (--force to rotate)
magus config mcp token revoke     # delete it (daemon mints a fresh one on next start)
```

Configure your client with the header, e.g.:

```json
{
  "type": "streamable-http",
  "url": "http://127.0.0.1:7391/mcp",
  "headers": { "Authorization": "Bearer <token>" }
}
```

Treat the token as **defense in depth**, and still keep the port closed. The server binds to `127.0.0.1` by default and validates the `Host` and `Origin` headers on every `/mcp` request, returning `403 Forbidden` for non-loopback values to block browser-based DNS-rebinding attacks. Anyone who reads the token gains the same workspace access, so keep it local.

**Do not expose it over:**

- Tailscale, Zerotier, or similar overlay networks where other devices can reach it
- ngrok, localtunnel, or other public tunnels
- SSH `-L` port-forwards shared with others
- Kubernetes `port-forward` in shared clusters
- Any network ACL that admits untrusted hosts

If you need to drive magus remotely, run the CLI over SSH instead.
