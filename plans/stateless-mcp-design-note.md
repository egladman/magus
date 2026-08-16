# Design note: ad hoc and stateless MCP, daemon as warm cache

Captured 2026-08-15 from Eli, mid-session. Recorded only; not implementation
work yet.

## The question

Can MCP tool calls happen without the daemon running? Today: no. `magus mcp`
prints instructions (cmd/magus/mcp.go: "MCP is no longer a standalone
command - it is served by `magus server start`"); the endpoint is Streamable
HTTP on the daemon plus a bearer token. MCP was standalone once and was
deliberately consolidated. Without the daemon the CLI is the fallback, and
the run skill already says the daemon must not be a prerequisite for
completing work.

## What changed outside

The MCP 2026-07-28 specification (final July 28, 2026) made the protocol
core stateless: the initialize handshake and the protocol-level session are
removed, every request is self-contained. It also added an Extensions
framework, a Tasks extension for long-running work, MCP Apps, and
OAuth-aligned authorization hardening.

## The position to hold

The daemon becomes a warm cache and a coordination host, never a
requirement. Two modes over ONE handler stack (internal/handler/mcp already
takes a Magus instance; transport is the only difference):

- Ad hoc: `magus mcp` revived as a real server - stdio for a per-session
  client, or stateless Streamable HTTP - opening the workspace per
  request/session the way every CLI invocation already does, then exiting.
- Daemon: exactly today's shape, now purely an optimization (warm graph,
  watchers, background jobs, dashboard).

Why magus is unusually well-placed: almost every magus_* tool is a pure read
over workspace + on-disk cache; state lives on disk by design. The taxes and
their answers:

- Cold symbol index per ad hoc call: the honesty work already shipped -
  Reach nil-vs-zero and the UNRANKED banner - means a cold answer degrades
  visibly, not silently. Statelessness is survivable because the tools
  stopped lying about coldness.
- The diff session (the one stateful surface): Store already persists viewed
  marks to disk; finish the persistence (comments, suggestions, cursor,
  AsOf) and the session is request-addressable by workspace root, which is
  all the stateless spec requires. App-level state is allowed; it just
  cannot live in the transport.
- Long-running runs: magus_run_target maps naturally onto the new Tasks
  extension - start a run, return a task handle, poll - instead of holding a
  connection.
- Auth: the existing bearer token is fine locally; the OAuth alignment
  matters only if the endpoint ever leaves localhost.

## Interactions with other open work

- Attention requests (plans/attention-requests-design-note.md): this settles
  its open scoping question - pending requests should be workspace-scoped,
  disk-backed state, surviving daemon restarts and stateless serving alike.
- The daemon-forward finding from today (a forwarded `magus run` swallowed
  output and exit status): a stateless path is also a debugging escape hatch
  when the daemon misbehaves.

## Spec specifics that matter to magus (2026-07-28 changelog, read 2026-08-15)

- Protocol sessions and Mcp-Session-Id are REMOVED (SEP-2567). Cross-call
  state is explicit server-minted HANDLES passed as ordinary tool arguments.
  That blesses exactly the shape magus_diff already has (a session keyed by
  workspace root, joined by argument); finish the disk-backing and the diff
  session is spec-native.
- The initialize handshake is gone; version and capabilities ride in _meta
  per request, and servers MUST implement a server/discover RPC. Adopting
  the revision DELETES bookkeeping, it does not add any.
- Tasks moved to an official extension (polling tasks/get, unsolicited task
  handles allowed): the natural home for magus_run_target and every
  long-running op.
- Multi Round-Trip Requests: a tool may return resultType "input_required"
  with inputRequests, and the client retries with the answers. This is a
  protocol-level, non-blocking ask-the-human primitive - directly relevant
  to plans/attention-requests-design-note.md.
- tools/list SHOULD be deterministic and now carries ttlMs/cacheScope -
  check the registry iterates deterministically (it likely already does).
- Roots, Sampling, and Logging are deprecated; stderr/OTel is the suggested
  logging migration, which is where magus already stands.

## The library fork in the road

magus uses mark3labs/mcp-go v0.48.0 (community library; the handlers are
ours, mounted on its server). The four Tier 1 SDKs - including the OFFICIAL
Go SDK (modelcontextprotocol/go-sdk) - shipped 2026-07-28 betas alongside
the RC. Whether mark3labs has caught up is UNVERIFIED. Decision for Eli:
wait for mark3labs, or treat this revision as the moment to migrate to the
official SDK (day-one spec support, steering-committee maintained). The
adapter layer in internal/handler/mcp/mcp.go (adapt/wrap) is the seam a
migration would go through; the tool implementations would not change.

## Shared services: two moves, not one (Eli's question untangled)

Shared services are daemon-hosted with lifecycle, adoption, and dependent
counts in magus status. So "MCP as a built-in shared service" is NOT the
daemonless mode - it is the other half:

1. Ad hoc floor (daemonless): stdio or stateless HTTP, workspace opened per
   request/session, exits when done. What the new spec makes cheap.
2. Service-managed warmth (dogfood): when the daemon runs, MCP should be a
   shared service under the standard framework instead of the bespoke
   startMCPWithDaemon goroutine (cmd/magus/mcp.go:73). It then gets status
   visibility, dependent counts, and adoption semantics for free, and the
   services framework gains its first built-in consumer. That is real
   dogfooding of magus's own machinery.

Both, not either. And yes to the second dogfood axis: this repo's own agent
config should consume the ad hoc mode once it exists.

## Open questions for Eli

- Which ad hoc transport first: stdio (simplest for local agent hosts) or
  stateless HTTP (matches the new spec's direction)?
- Library: wait for mark3labs 2026-07-28 support, or migrate to the
  official Go SDK beta now?
- Does the spec's session removal change the client config story
  (docs/guides/mcp.md documents the three settings today)?
- Adopt the Tasks extension in the same move or later?

Sources: modelcontextprotocol.io/specification/2026-07-28/changelog;
blog.modelcontextprotocol.io posts 2026-07-28 and sdk-betas-2026-07-28;
github.com/mark3labs/mcp-go.
