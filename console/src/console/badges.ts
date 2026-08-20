// badges.ts - the counts the navigation rail hangs on a surface's row. One reading per surface, read
// on the shell's existing readiness interval, so a rail row can say how much is waiting in a surface
// without that surface's tab being open.
//
// Diff is the only one today, and deliberately: a badge earns its place when the number MOVES and the
// magnitude changes what you do (twelve changed files is a different afternoon from three). A count
// that never changes is decoration. Notes was the cheaper call and was rejected for exactly that.
//
// Same contract as pulse.ts, for the same reasons: best-effort, hidden rather than zeroed when there
// is no answer, and latched off per host once the daemon says it does not serve the route.
import { authHeaders, validateLoopbackHost } from "../lib/daemon";

// Hosts whose daemon has no diff route. A plain fetch 404 is NOT a ConnectError, so daemon.ts's
// isCapabilityDenied cannot classify it - the status code is the only signal, and it has to be read
// here. 404/501 mean the route cannot appear while this daemon runs; anything else (a 5xx, a dropped
// connection, a blocked cross-origin request) is an outage that must keep retrying, or one blip would
// blank the badge for the rest of the session.
const routeless = new Set<string>();

// The largest number the rail can render before it stops being a number and starts being "a lot". The
// collapsed rail is 44px wide, so a four-digit count would either overflow the column or shrink past
// legibility; past this the exact figure has stopped informing the decision anyway.
export const BADGE_MAX = 99;

// One surface's reading: how many, and what they ARE. The noun exists because the rail row's
// aria-label overrides its own contents, so a badge rendered as bare text is silent to a screen
// reader - the row has to fold the count into its name, and "Diff, 12" says less than it should.
export interface Badge {
  count: number;
  noun: string; // plural, lowercase: "changed files"
}

// badgeLabel caps the count for display. Exported so the cap is tested against the renderer that uses
// it rather than asserted twice.
export function badgeLabel(count: number): string {
  return count > BADGE_MAX ? BADGE_MAX + "+" : String(count);
}

// fetchDiffCount reads how many files the review session is carrying, or null when there is nothing to
// say. Zero is a real answer and returns 0 - the caller decides whether "nothing changed" is worth a
// badge, because that is a display question, not a measurement one.
export async function fetchDiffCount(
  host: string,
  call: (host: string) => Promise<number | null> = getDiffCount,
): Promise<number | null> {
  if (!host || routeless.has(host)) return null;
  try {
    return await call(host);
  } catch (e) {
    if (e instanceof RoutelessError) routeless.add(host);
    return null;
  }
}

// Thrown when the daemon answers "no such route", so fetchDiffCount can tell a permanent absence from
// a passing outage without the transport leaking into it.
export class RoutelessError extends Error {
  // Without this every stack trace and console line reads "Error", which is the one thing the class
  // exists to distinguish.
  override name = "RoutelessError";
}

// How long to wait before giving up on one read. Matches lib/daemon.ts's readiness probe, which rides
// the same interval: without it a hung connection leaves a request pending per tick, unbounded.
const TIMEOUT_MS = 3000;

async function getDiffCount(host: string): Promise<number | null> {
  // Defense in depth, mirroring lib/daemon.ts's fetchReadiness: the caller passes an already-resolved
  // host, but re-verify it is literal loopback OR the page's OWN origin before attaching the bearer
  // token, so a future caller that ever passes a raw string cannot send the token to a third party.
  // The LAN-share host is same-origin, so it passes here where validateLoopbackHost alone would not.
  const safe =
    validateLoopbackHost(host) ??
    (typeof location !== "undefined" && host === location.host ? host : null);
  if (!safe) return null;
  const res = await fetch(`http://${safe}/api/v1/diff/session`, {
    headers: authHeaders(),
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
  if (res.status === 404 || res.status === 501) throw new RoutelessError(String(res.status));
  if (!res.ok) throw new Error(String(res.status));
  const session: unknown = await res.json();
  const files = (session as { diff?: { files?: unknown } })?.diff?.files;
  // A shape change upstream must read as "no answer" rather than as a count: the alternative is a
  // badge asserting a number nobody measured.
  return Array.isArray(files) ? files.length : null;
}

// resetRoutelessHosts drops the latch. Exported for the tests, which would otherwise leak one case's
// verdict into the next.
export function resetRoutelessHosts(): void {
  routeless.clear();
}
