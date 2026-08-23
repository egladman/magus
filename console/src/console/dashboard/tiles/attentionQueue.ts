// attentionQueue.ts - the attention queue's wire shape, its two reads, and the pure text
// helpers the tile renders rows with. DOM-free on purpose, so what a reader ends up seeing is
// decided by code a test can run without a browser (attentionQueue.test.ts). The tile next
// door owns the markup, the poll and the composer.
//
// The queue is the durable list of blocks agents have raised that are waiting on a PERSON:
// `magus session notify` opens a request when an event's outcome is waiting or permission, and
// nothing closes one but somebody deciding to. That is why this module has a write at all -
// see disposeAttention - and why it has exactly one.
//
// A daemon predating the endpoint 404s GET /api/v1/attention, and that is a FIRST-CLASS
// outcome here rather than an error: loadAttention reports "absent" so the tile can say the
// route is missing instead of showing an empty queue, which would read as "nobody is blocked".

import { authHeaders } from "../../../lib/daemon";

// ---- the wire shape --------------------------------------------------------

// AttentionRequest mirrors one row of GET /api/v1/attention's JSON, which is the same shape
// `magus session attention -o json` prints. Hand-written rather than generated because it rides the
// plain /api routes, the same as the delegation ledger beside it.
//
// Every field but the id is optional here even where the route always sends it: this is parsed
// from the network, and a missing outcome must render as a blank cell rather than the string
// "undefined".
export interface AttentionRequest {
  readonly id: string;
  // The AGENT session that raised the block, not the one that will close it.
  readonly session: string;
  // Unix MILLISECONDS, as the route serves them - numbers, never strings. Coerced as a string
  // the field reads as 0, and the tile then cannot tell a block raised an hour ago from one
  // raised a moment ago, which is the single most useful thing about a queue.
  readonly opened_ms: number;
  readonly outcome: string;
  readonly severity: string;
  readonly source: string;
  readonly where: string;
  // The delegation the RAISING session was launched under: which slice of a fleet's work is
  // blocked. Attribution, never identity - the request id does not key on it, so a fleet
  // re-partitioning does not re-key the row a person was about to close. Empty for anything a
  // person started by hand, which is not an error.
  readonly delegation: string;
  readonly message: string;
}

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function num(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

// parseRequests normalizes the response body into rows this module's model can be TOTAL over:
// every field is the type it claims, and a row with no usable id is dropped rather than
// becoming a row whose dispose control could not name what it closes. Anything that is not the
// documented shape yields an empty list, not a throw - a tile that cannot read the queue says
// so, it does not break the board.
export function parseRequests(body: unknown): AttentionRequest[] {
  const list = (body as { requests?: unknown } | null)?.requests;
  if (!Array.isArray(list)) return [];
  const out: AttentionRequest[] = [];
  for (const raw of list) {
    if (typeof raw !== "object" || raw === null) continue;
    const r = raw as Record<string, unknown>;
    const id = str(r.id);
    if (!id) continue;
    out.push({
      id,
      session: str(r.session),
      opened_ms: num(r.opened_ms),
      outcome: str(r.outcome),
      severity: str(r.severity),
      source: str(r.source),
      where: str(r.where),
      delegation: str(r.delegation),
      message: str(r.message),
    });
  }
  return out;
}

// parseStore reads the store path the route reports, which is what the tile tells a reader to
// go look at when it has nothing to show. "" when the route sent none.
export function parseStore(body: unknown): string {
  return str((body as { store?: unknown } | null)?.store);
}

// ---- pure text -------------------------------------------------------------

// ageLabel is how long a request has been waiting, at the coarsest granularity that still
// answers the question - the same ladder the delegation surface reads ages on, over
// MILLISECONDS rather than seconds because that is the unit this route serves.
//
// "" when the row carries no timestamp, so the caller renders nothing rather than a confident
// "0s" about a time nobody recorded. A stamp in the future clamps to "0s" instead of going
// negative: two machines' clocks disagree, and "-4s waiting" reads as a bug in the queue.
export function ageLabel(openedMs: number, nowMs: number): string {
  if (!Number.isFinite(openedMs) || openedMs <= 0) return "";
  const secs = Math.max(0, Math.round((nowMs - openedMs) / 1000));
  if (secs < 60) return secs + "s";
  if (secs < 3600) return Math.floor(secs / 60) + "m";
  if (secs < 86400) return Math.floor(secs / 3600) + "h";
  return Math.floor(secs / 86400) + "d";
}

// MESSAGE_MAX is how much of a message one row shows. The store admits up to 4 KiB, because a
// producer is typically a hook forwarding an agent's text; a row that long would push every
// other request off the tile, which is the opposite of what a queue is for.
const MESSAGE_MAX = 140;

// firstLine flattens a message onto the row it belongs to.
//
// An agent writes whatever it likes, newlines included, and ONE row per request is what makes
// the queue scannable. The first non-empty line is taken rather than the whole message joined,
// because agent text is routinely a summary line followed by a transcript, and the summary is
// the part a person triages on. Nothing is lost: the full message is what `magus session
// attention -o json` carries, and the row's title attribute holds it too.
export function firstLine(message: string): string {
  for (const raw of message.split("\n")) {
    const line = raw.trim().replace(/\s+/g, " ");
    if (!line) continue;
    return line.length > MESSAGE_MAX ? line.slice(0, MESSAGE_MAX - 3) + "..." : line;
  }
  return "";
}

// ---- the reads -------------------------------------------------------------

// AttentionRead is the three answers the endpoint can give, kept apart because they mean
// different things to a reader staring at an empty tile: nobody is blocked, the route is not
// there, or the daemon could not be read. Collapsing them into one "no data" is how a missing
// feature comes to look like a calm one - and on THIS tile that mistake reads as "you are not
// needed", which is the one thing the queue exists to never say by accident.
export type AttentionRead =
  | { readonly kind: "ok"; readonly requests: AttentionRequest[]; readonly store: string }
  | { readonly kind: "absent" }
  | { readonly kind: "unreadable"; readonly detail: string };

// loadAttention reads GET /api/v1/attention under the same bearer + no-store rules as the
// delegation ledger beside it. 404 and 501 are "absent", not failures: on any daemon predating
// the route that is the honest answer, and the tile says so by name.
//
// A cross-origin console (a hosted page reaching a loopback daemon over #port=) cannot always
// TELL the two apart - a 404 carrying no CORS headers surfaces to the browser as a network
// error with no status at all - so the unreadable copy names the missing route as a possible
// cause too rather than blaming the connection.
export async function loadAttention(host: string, signal?: AbortSignal): Promise<AttentionRead> {
  let res: Response;
  try {
    res = await fetch("http://" + host + "/api/v1/attention", {
      headers: authHeaders(),
      cache: "no-store",
      signal,
    });
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
  if (res.status === 404 || res.status === 501) return { kind: "absent" };
  if (!res.ok) return { kind: "unreadable", detail: "HTTP " + res.status };
  try {
    const body = await res.json();
    return { kind: "ok", requests: parseRequests(body), store: parseStore(body) };
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
}

// DisposeResult separates a REFUSAL from a failure. The daemon refuses a disposal for three
// reasons a person can act on - the id names nothing, it names several, or somebody already
// closed it - and each of those is the daemon working correctly. Reporting them as errors
// would train a reader to retry the one thing that will never succeed.
export type DisposeResult =
  | { readonly kind: "ok" }
  | { readonly kind: "refused"; readonly detail: string }
  | { readonly kind: "unreadable"; readonly detail: string };

// disposeAttention closes one request. It is the console's half of a rule magus states
// explicitly: an event whose whole meaning is "blocked on a person" stops meaning that the
// moment the tool answers it, so nothing here closes a request on anyone's behalf. This
// function runs because somebody clicked, typed a reason and pressed Enter, and for no other
// reason - there is deliberately no dispose-all and no caller that loops.
export async function disposeAttention(
  host: string,
  id: string,
  reason: string,
  signal?: AbortSignal,
): Promise<DisposeResult> {
  let res: Response;
  try {
    res = await fetch("http://" + host + "/api/v1/attention", {
      method: "POST",
      headers: { ...authHeaders(), "Content-Type": "application/json" },
      body: JSON.stringify({ id, reason }),
      cache: "no-store",
      signal,
    });
  } catch (e) {
    return { kind: "unreadable", detail: e instanceof Error ? e.message : String(e) };
  }
  if (res.ok) return { kind: "ok" };
  // The daemon's own sentence, which names the candidates on an ambiguous prefix and says who
  // closed an already-closed request. Replacing it with a status code would throw away the
  // only part a person can act on.
  const detail = (await res.text().catch(() => "")).trim() || "HTTP " + res.status;
  if (res.status >= 400 && res.status < 500) return { kind: "refused", detail };
  return { kind: "unreadable", detail };
}
