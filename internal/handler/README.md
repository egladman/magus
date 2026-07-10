# internal/handler

The presentation layer: the code that turns a request (an HTTP call from a browser,
or an MCP tool call from an agent) into a domain call and maps the result onto a
versioned wire contract. Transport (the loopback HTTP server, its bind, and the
CORS / loopback / DNS-rebind / bearer middleware) does NOT live here - it lives in
`internal/httpx`. Data access does NOT live here - handlers call the repositories.

## The rule: a handler subpackage mirrors its proto package

A wire-mapping subpackage `internal/handler/<name>` owns the over-the-wire concerns
of the protobuf package `magus.<name>.v1` and is NAMED to match it, so the two are
trivially correlated:

| handler subpackage             | proto package             | owns                                              |
|--------------------------------|---------------------------|---------------------------------------------------|
| `internal/handler/viewer`      | `magus.viewer.v1`         | domain-event -> proto mapping, fragment/SSE encode, the log-viewer live SSE server |
| `internal/handler/status`      | `magus.status.v1`         | status-report -> proto mapping + encoder, the GET /api/v1/status and /api/v1/events handlers |
| `internal/handler/graph`       | `magus.graph.v1`          | knowledge-graph -> proto mapping, the GET /api/v1/graph handler |

When you add a new wire contract `proto/magus/foo/v1`, its mapping goes in a new
`internal/handler/foo` package - same name, no exceptions for the wire packages.

Each browser-bridge handler is an `http.Handler` receiver type holding a NARROW
consumer interface (e.g. `graphSource`, `statusSource`) that is satisfied by the
pure-logic `internal/service/console` service. The service returns DOMAIN values; the
handler owns the wire encoding.

Wire-mapping packages are build-tag-free (they are pure mapping; a CLI path may use
them). Handlers that only run inside the daemon are `//go:build mcp`.

## Deliberate non-mirror packages

One subpackage is not 1:1 with a single proto package, by design:

- `internal/handler/mcp` (`//go:build mcp`) - the MCP request handlers (the tool
  implementations, the descriptor catalog, and the dispatch pipeline in `mcp.go`),
  plus the streamable-HTTP transport that mounts onto `httpx.Server`. It mirrors the
  agent-facing MCP tool surface, not a `.v1` proto package. Its `auth/` and `origin/`
  leaves are build-tag-free so the composition root can read them.

## Layering

    transport   internal/httpx            (one loopback Server + middleware)
    presentation internal/handler/*       (this package - request -> domain -> wire)
    repositories internal/cache, /knowledge, ... (data access)

Keep the arrows pointing down: handlers import httpx (to mount routes) and the
repositories (to read/write); nothing in a repository imports a handler, and nothing
in httpx imports a handler.
