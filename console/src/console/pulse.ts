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
import { createClient } from "@connectrpc/connect";
import { StatusService } from "../gen/magus/status/v1alpha1/status_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../lib/daemon";

export interface PulseView {
  running: number;
  queued: number;
  // Every workspace this daemon has loaded, by root path. The shell's scope selector is built from
  // this - it rides the reading that is already being fetched rather than a second call, and it is
  // the only place the shell learns that more than one workspace exists at all.
  workspaces: string[];
}

// Daemons that answered "I do not serve this", by host. GetStatus is newer than the shipped releases
// (a v0.2.0 daemon 404s the route), and this poll runs for the page's whole life whether or not a tab
// is open - so without this, a console pointed at an older daemon retries a route that cannot appear
// every 15 seconds forever, which is the console-spamming that lib/daemon.ts's readiness probe is at
// pains to avoid. Keyed by HOST so pointing at a different daemon probes again; a daemon UPGRADED in
// place stays latched until the page reloads, which is the cheap half of the trade.
//
// Only a capability denial latches. A plain outage must not: the daemon coming back is the normal
// case, and latching on it would leave the rail permanently blank after one blip.
const deniedHosts = new Set<string>();

// The one RPC, split out so the latch below can be tested without a daemon. It THROWS on failure;
// classifying the failure is fetchPulse's job.
async function getPool(host: string): Promise<PulseView | null> {
  const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
  const resp = await client.getStatus({});
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
  if (!host || deniedHosts.has(host)) return null;
  try {
    return await call(host);
  } catch (e) {
    if (isCapabilityDenied(e)) deniedHosts.add(host);
    return null;
  }
}

// resetDeniedHosts drops the latch. Exported for the tests, which would otherwise leak one case's
// denial into the next.
export function resetDeniedHosts(): void {
  deniedHosts.clear();
}
