// pulse.ts - the shell's one live reading of the daemon: how many units of work are running and how
// many are waiting for a slot. The navigation rail shows it, so it has to survive with no tab open -
// which rules out the dashboard's store, whose bundle only exists while its tab does.
//
// A UNARY read on the shell's existing readiness interval, not a second subscription. The dashboard
// holds the /api/v1/events SSE and its open/close is what drives the connection dot; a second stream
// would put two owners on one signal. StatusService.GetStatus answers the same Status message one
// question at a time, and the shell already polls this daemon every 15s for /readyz, so this rides an
// interval that is already running rather than introducing background traffic.
//
// Deliberately just the pool. "Failing" is a richer derivation the dashboard owns, and a rail that
// guessed at it would disagree with the board that computes it properly.
import { createClient, Code, ConnectError } from "@connectrpc/connect";
import { StatusService } from "../gen/magus/status/v1alpha1/status_pb";
import { createDaemonTransport, getLiveToken } from "../lib/daemon";

export interface PulseView {
  running: number;
  queued: number;
  // Every workspace this daemon has loaded, by root path. The shell's scope selector is built from
  // this - it rides the reading that is already being fetched rather than a second call, and it is
  // the only place the shell learns that more than one workspace exists at all.
  workspaces: string[];
}

// Hosts whose daemon has no GetStatus route, by host. The route is newer than the shipped releases (a
// v0.2.0 daemon 404s it), and this poll runs for the page's whole life whether or not a tab is open -
// so without this, a console pointed at an older daemon retries a route that cannot appear every 15
// seconds forever, which is the console-spamming that lib/daemon.ts's readiness probe is at pains to
// avoid. Keyed by HOST so pointing at a different daemon probes again; a daemon UPGRADED in place
// stays latched until the page reloads, which is the cheap half of the trade.
const routelessHosts = new Set<string>();

// How long to wait before giving up on one poll. Matches lib/daemon.ts's readiness probe, which rides
// the same interval: without it a hung connection leaves a request pending per tick, unbounded.
const TIMEOUT_MS = 3000;

// Only the daemon saying the route does not exist latches. An auth failure must NOT: the shell trades
// the operator token for a console-scoped one AFTER this poll can first run, so a single early
// Unauthenticated would blank the rail for the life of the page even once the credential works. This
// is deliberately narrower than daemon.ts's isCapabilityDenied, which folds auth in because it answers
// a different question - what a LAN-share session may SEE, where a denial is a durable property of the
// session rather than a passing state.
function isRouteless(e: unknown): boolean {
  if (!(e instanceof ConnectError)) return false;
  return e.code === Code.Unimplemented || e.code === Code.NotFound;
}

// The one RPC, split out so the latch below can be tested without a daemon. It THROWS on failure;
// classifying the failure is fetchPulse's job.
async function getPool(host: string): Promise<PulseView | null> {
  const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
  const resp = await client.getStatus({}, { signal: AbortSignal.timeout(TIMEOUT_MS) });
  const pool = resp.status?.pool;
  if (!pool) return null;
  return {
    running: pool.running,
    queued: pool.queued,
    workspaces: pool.workspaces.map((w) => w.root).filter((r) => r !== ""),
  };
}

// fetchPulse reads the daemon's live pool occupancy, or resolves null when there is nothing to show -
// no daemon, an old one, a network blip, a token the caller is not allowed to spend. Best-effort by
// contract: the rail hides the reading rather than reporting a zero it did not measure, so every
// failure has to be indistinguishable from "no answer" rather than from "nothing running".
//
// `call` is injected only so the tests can drive the failure classes; every caller uses the default.
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

// resetRoutelessHosts drops the latch. Exported for the tests, which would otherwise leak one case's
// verdict into the next.
export function resetRoutelessHosts(): void {
  routelessHosts.clear();
}
