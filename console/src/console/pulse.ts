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
import { createDaemonTransport, getLiveToken } from "../lib/daemon";

export interface PulseView {
  running: number;
  queued: number;
}

// fetchPulse reads the daemon's live pool occupancy, or resolves null when there is nothing to show -
// no daemon, an old one, a network blip, a token the caller is not allowed to spend. Best-effort by
// contract: the rail hides the reading rather than reporting a zero it did not measure, so every
// failure has to be indistinguishable from "no answer" rather than from "nothing running".
export async function fetchPulse(host: string): Promise<PulseView | null> {
  if (!host) return null;
  try {
    const client = createClient(StatusService, createDaemonTransport(host, getLiveToken()));
    const resp = await client.getStatus({});
    const pool = resp.status?.pool;
    if (!pool) return null;
    return { running: pool.running, queued: pool.queued };
  } catch {
    return null;
  }
}
