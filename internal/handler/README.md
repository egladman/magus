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

MCP is always compiled in - there are no build tags. Test files use the SAME package
as the code they test (`package status`, never `package status_test`).

## Deliberate non-mirror packages

One subpackage is not 1:1 with a single proto package, by design:

- `internal/handler/mcp` - the MCP request handlers (the tool implementations, the
  descriptor catalog in `registry.go`, the dispatch pipeline in `mcp.go`, and the
  transports in `transport.go`: the streamable-HTTP handler builder + stdio). It
  mirrors the agent-facing MCP tool surface, not a `.v1` proto package. Its bearer
  token store lives in `internal/auth`; the guards in `internal/httpx`.

## Layering

    transport    internal/httpx           (one loopback Server + middleware)
    handler      internal/handler/*       (this package - request -> domain -> wire)
    service      internal/service/*       (pure application logic - no http/proto)
    repository   internal/cache, knowledge  (data access)
    composition  internal/daemon          (assembles the daemon server)

Keep the arrows pointing down: a handler imports its service, httpx (to mount routes),
and the repositories; nothing in a repository or in httpx imports a handler.
