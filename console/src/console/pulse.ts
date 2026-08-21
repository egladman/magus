// pulse.ts - the shell's live reading of the daemon pool, for the navigation rail.
//
// A unary read on the shell's existing 15s readiness interval, not a second SSE subscription: the
// dashboard already owns /api/v1/events and drives the connection dot from it, and the rail has to
// survive with no tab open, so it cannot use the dashboard's store.
import { createClient, Code, ConnectError } from "@connectrpc/connect";
import { StatusService } from "../gen/magus/status/v1alpha1/status_pb";
import { createDaemonTransport, getLiveToken } from "../lib/daemon";

export interface PulseView {
  running: number;
  queued: number;
  // Also feeds the shell's scope selector - the only place it learns more than one workspace exists.
  workspaces: string[];
  // savedMs is measured, not modeled: each cache entry records how long its run took and a hit sums
  // that figure. It covers this daemon's lifetime only, and understates - an entry written before
  // the field existed adds nothing. Never label it as the cache's all-time saving.
  cache: { hits: number; misses: number; savedMs: number } | null;
}

// Hosts whose daemon has no GetStatus route. Without the latch, a console pointed at a v0.2.0 daemon
// retries a 404 every 15s for the life of the page. Keyed by host, so a different daemon probes
// again; one upgraded in place stays latched until reload.
const routelessHosts = new Set<string>();

const TIMEOUT_MS = 3000;

// Only "no such route" latches. An auth failure must not: the shell trades the operator token for a
// console-scoped one after this poll can first run, so one early Unauthenticated would blank the
// rail for the life of the page even once the credential works.
function isRouteless(e: unknown): boolean {
  if (!(e instanceof ConnectError)) return false;
  return e.code === Code.Unimplemented || e.code === Code.NotFound;
}

async function getPool(host: string): Promise<PulseView | null> {
  const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
  const resp = await client.getStatus({}, { signal: AbortSignal.timeout(TIMEOUT_MS) });
  const pool = resp.status?.pool;
  if (!pool) return null;
  const c = pool.cache;
  return {
    running: pool.running,
    queued: pool.queued,
    workspaces: pool.workspaces.map((w) => w.root).filter((r) => r !== ""),
    cache: c
      ? { hits: Number(c.hits), misses: Number(c.misses), savedMs: Number(c.savedMs ?? 0) }
      : null,
  };
}

// Null means "no answer" - no daemon, an old one, a blip, a token that will not spend. Every failure
// has to look the same, because the rail hides the reading rather than reporting a zero it did not
// measure. `call` is injected only so tests can drive the failure classes.
export async function fetchPulse(
  host: string,
  call: (host: string) => Promise<PulseView | null> = getPool,
): Promise<PulseView | null> {
  if (!host || routelessHosts.has(host)) return null;
  try {
    return await call(host);
  } catch (e) {
    if (isRouteless(e)) routelessHosts.add(host);
    return null;
  }
}

// Exported for tests, which would otherwise leak one case's latch into the next.
export function resetRoutelessHosts(): void {
  routelessHosts.clear();
}
