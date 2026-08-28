# internal/handler

The presentation layer: the code that turns a request (an HTTP call from a browser,
or an MCP tool call from an agent) into a domain call and maps the result onto a
versioned wire contract. Transport (the loopback HTTP server, its bind, and the
CORS / loopback / DNS-rebind / bearer middleware) does NOT live here - it lives in
`internal/httpx`. Data access does NOT live here - handlers call the repositories.

## The rule: a handler subpackage mirrors its proto package

A wire-mapping subpackage `internal/handler/<name>` owns the over-the-wire concerns
of the protobuf package `magus.<name>.v1alpha1` and is NAMED to match it, so the two are
trivially correlated:

| handler subpackage        | proto package     | owns                                                                                         |
| ------------------------- | ----------------- | -------------------------------------------------------------------------------------------- |
| `internal/handler/viewer` | `magus.viewer.v1alpha1` | ViewerService (the run browser and one run's journal), domain-event -> proto mapping, fragment/SSE encode, the live SSE server |
| `internal/handler/status` | `magus.status.v1alpha1` | status-report -> proto mapping + encoder, the GET /api/v1/status and /api/v1/events handlers    |
| `internal/handler/graph`  | `magus.graph.v1alpha1`  | knowledge-graph -> proto mapping, the GET /api/v1/graph handler                              |

When you add a new wire contract `proto/magus/foo/v1alpha1`, its mapping goes in a new
`internal/handler/foo` package - same name, no exceptions for the wire packages.

Each console handler is an `http.Handler` receiver type holding a NARROW
consumer interface that is satisfied by the pure-logic `internal/service/console` service. The
service returns DOMAIN values; the handler owns the wire encoding.

Name that interface `<name>Source` and keep it UNEXPORTED: `graphSource`, `statusSource`,
`insightSource`. It is the consumer's statement of what it needs, not a type any caller has to
name - Go satisfies it structurally, so the service that implements it never mentions it. Export
it only when the composition root genuinely has to write the type down, which today is true of
exactly one (`plan.Source`, named in five places outside its package).

A handler that needs no service takes a concrete dependency instead and declares no interface at
all; `attention` is the example. Do not invent a one-implementation interface to match the shape
of its neighbours.

MCP is always compiled in - there are no build tags. Test files use the SAME package
as the code they test (`package status`, never `package status_test`).

## Packages that mirror a ROUTE instead of a proto

Some console data rides plain JSON `/api/v1/*` routes rather than a Connect service. Those
handlers have no proto package to be named after, so they are named after the route namespace
they serve, and the rule is otherwise the same: one package per namespace, and it owns that
namespace outright.

| handler subpackage           | serves                                            |
| ---------------------------- | ------------------------------------------------- |
| `internal/handler/diff`      | `/api/v1/diff` and everything under it            |
| `internal/handler/plan`      | `/api/v1/plan`                                    |
| `internal/handler/attention` | `/api/v1/attention`                               |
| `internal/handler/ledger`    | `/api/v1/ledger`                                  |

A route package is NOT a place to put whatever has no home yet. `handler/status` accumulated
five of these before they were split out, while its own doc still described one thing: mapping
the status report onto StatusService's two RPCs. The tell was in the constructor names - every
one of them read `NewDiff*` inside `package status`.

## Deliberate non-mirror packages

Two subpackages mirror neither a proto package nor a route:

- `internal/handler/mcp` - the MCP request handlers (the tool implementations, the
  descriptor catalog in `registry.go`, the dispatch pipeline in `mcp.go`, and the
  transports in `transport.go`: the streamable-HTTP handler builder + stdio). It
  mirrors the agent-facing MCP tool surface. Its bearer
  token store lives in `internal/auth`; the guards in `internal/httpx`.
- `internal/handler/trailrpc` - the audit interceptor for the Connect services, which
  records mutating unary RPCs to the activity trail by construction.

## Naming a handler package against its domain twin

Mirroring a proto or a route means a handler often shares its name with the domain package it
calls: `handler/graph` and `internal/graph`, `handler/ledger` and `internal/ledger`, and the same
for `memory` and `notes`. Go resolves that with an alias, and this tree spells it two ways
consistently:

- INSIDE a handler, the domain import is aliased `store`:
  `store "github.com/egladman/magus/internal/notes"`.
- In `internal/daemon`, which imports every handler at once, each is aliased `<name>handler`:
  `noteshandler "github.com/egladman/magus/internal/handler/notes"`.

Sharing the name with the domain package is expected and fine. Two packages sharing a name with
NO such relationship is not: `internal/diff` and `internal/interactive/diff` were peers claiming
one word, and callers invented a third name for one of them to tell them apart. That is what the
alias is telling you when it appears anywhere other than the two spellings above.

## Layering

    transport    internal/httpx           (one loopback Server + middleware)
    handler      internal/handler/*       (this package - request -> domain -> wire)
    service      internal/service/*       (pure application logic - no http/proto)
    repository   internal/cache, knowledge  (data access)
    composition  internal/daemon          (assembles the daemon server)

Keep the arrows pointing down: a handler imports its service, httpx (to mount routes),
and the repositories; nothing in a repository or in httpx imports a handler.
