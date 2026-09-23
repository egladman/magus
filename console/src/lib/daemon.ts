import { errMessage, errName } from "./guards";
import { reportFailure, type NotifyLink } from "./notifications";
// daemon.ts - the ONE audited module for addressing and talking to a magus daemon.
//
// Every surface (dashboard, graph explorer, log viewer, activity, the shell) imports
// these security-critical helpers, which used to be copy-pasted between them. They
// live here, imported once, so host resolution, the loopback lock, and the shared
// token keys have a single home.
//
// What this module owns:
//   - HOST RESOLUTION: resolveDaemonHost() is the single source of truth for "which
//     daemon origin do I talk to". It considers, in order, an explicit #port= attach,
//     own-origin adoption (a LAN-share viewer / the operator's daemon-origin console),
//     and the operator's configured default. daemonAttach() is the explicit-only
//     subset (no configured fallback) surfaces use to decide whether to enter live
//     mode at all.
//   - the LOOPBACK LOCK: validateLoopbackHost()/normalizeDaemonHost() - the pure host
//     checks that make the "your data never leaves your machine" claim verifiable. A
//     configured/entered host must be literal loopback; a #port= is loopback-implied
//     and expands to 127.0.0.1:<port>. Every loopback-mode fetch is built from a
//     normalized value, never a raw fragment string.
//   - the SHARED TOKEN: consumeLiveToken/getLiveToken over the "magus-live-token"
//     (session or, if remembered, local) and "magus-live-remember" keys - one
//     token a tool page reuses when it hands off to another.
//   - the STREAM CLIENTS: fetchSSE() (fetch-based SSE so the bearer token can ride
//     an Authorization header, which EventSource cannot send) and the ConnectRPC
//     transport helper (bearer interceptor + createConnectTransport). This module
//     does NOT own reconnect orchestration: each page hand-rolls its own loop on
//     top of fetchSSE (the dashboard's lives in dashboard/transport.ts), since
//     their backoff and teardown differ.
//
// Nothing here has a top-level side effect, so a page that imports only the
// primitives is tree-shaken clear of the ConnectRPC transport code the dashboard
// needs.

import { createConnectTransport } from "@connectrpc/connect-web";
import { Code, ConnectError, type Interceptor, type Transport } from "@connectrpc/connect";
import { getDefaultHost, getRememberedHost } from "./settings";

// isCapabilityDenied reports whether a Connect RPC error is the daemon DECLINING the capability to
// this client (not a transient outage). A read-only LAN-share session cannot reach TokenService
// or MemoryService: the LAN share listener never mounts them (Unimplemented/NotFound) and a share
// token cannot pass their guard (Unauthenticated/PermissionDenied). A capability-gated section
// keys its visibility on this so the SERVER decides what a client may see - never a client-side
// mode guess. A plain outage (Unavailable, network error) is NOT a denial; it keeps its empty state.
export function isCapabilityDenied(e: unknown): boolean {
  if (!(e instanceof ConnectError)) return false;
  return (
    e.code === Code.Unauthenticated ||
    e.code === Code.PermissionDenied ||
    e.code === Code.Unimplemented ||
    e.code === Code.NotFound
  );
}

// isUnreachable reports whether a request failed without any response to read: nothing listening,
// a connection the browser blocked, or a deadline that ran out. Those are the failures a connect
// prompt describes. An error the daemon SENT (a 500, a Connect status) is not one of them, and
// telling that reader to start a daemon sends them to fix the wrong thing.
//
// fetch reports "no response" as a TypeError, and Connect wraps it with the TypeError as its cause.
// A timed-out Connect call carries no cause, only Canceled or DeadlineExceeded.
export function isUnreachable(e: unknown): boolean {
  if (e instanceof TypeError) return true;
  if (e instanceof Error && e.name === "TimeoutError") return true;
  if (!(e instanceof ConnectError)) return false;
  return (
    e.cause instanceof TypeError || e.code === Code.Canceled || e.code === Code.DeadlineExceeded
  );
}

// ---- hash params -----------------------------------------------------------

export type HashParams = Record<string, string>;

// parseHash reads the "#key=value&..." fragment into a map. A bare part (no "=",
// e.g. the log viewer's "L10-L20" line token) is kept with an empty value so a
// caller that rewrites the fragment (consumeLiveToken) can round-trip it.
export function parseHash(): HashParams {
  // A malformed percent-escape (e.g. a truncated shared link) makes
  // decodeURIComponent throw; keep the raw text rather than aborting boot, since
  // parseHash runs before any surface mounts.
  const decode = (s: string): string => {
    try {
      return decodeURIComponent(s);
    } catch {
      // not-a-failure: a truncated escape keeps its raw text, which is still the reader's link
      return s;
    }
  };
  const h = location.hash.replace(/^#/, "");
  const params: HashParams = {};
  for (const part of h.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    if (i < 0) {
      params[part] = "";
      continue;
    }
    params[decode(part.slice(0, i))] = decode(part.slice(i + 1));
  }
  return params;
}

// wantsDemo reports whether the fragment requested the daemon-free demo. The
// canonical form is a bare `#demo`; parseHash keeps a bare key with an empty value,
// so `!== undefined` matches both `#demo` and a stray `#demo=1`. One definition so
// every tool page (dashboard, graph explorer, log viewer) triggers its showcase on
// the identical fragment.
export function wantsDemo(params: HashParams): boolean {
  return params.demo !== undefined;
}

// mayLoadBundledDemo reports whether a surface may fetch demo data bundled next to the app
// (the graph explorer's knowledge-graph.json). Never under a daemon attach: that file is this
// repo's own graph, notes included, and falling back to it after a failed or tokenless attach
// shows workspace data nobody authenticated for. Synthetic in-source demos are not covered.
// Read it after adoptDaemonOrigin, or a daemon-origin page is not yet known to be attached.
export function mayLoadBundledDemo(params: HashParams = parseHash()): boolean {
  return daemonAttach(params) === null;
}

// ---- the loopback lock -----------------------------------------------------

// validateLoopbackHost: a configured/entered daemon host MUST be literally 127.0.0.1
// or [::1] with a port. localhost, hostnames, and other IPs are rejected before any
// network request. Parses hostPort as a REAL URL rather than splitting on the last
// ":" - a naive split lets a URL-userinfo "@" smuggle an attacker host past the check
// (e.g. "127.0.0.1:7391@evil.com" splits to host "127.0.0.1", but a browser fetching
// "http://127.0.0.1:7391@evil.com" actually connects to evil.com and would send it the
// bearer token). Returns the normalized "host:port" (brackets kept for IPv6) on
// success, or null on any rejection. The LAN share's same-origin host is NOT accepted
// here - that path resolves through resolveDaemonHost/location.host directly, so this
// pure loopback check makes the docs claim "data cannot leave your machine" verifiable.
export function validateLoopbackHost(hostPort: string): string | null {
  let u: URL;
  try {
    u = new URL("http://" + hostPort);
  } catch {
    // not-a-failure: an unparseable host is a rejected one; callers explain the null
    return null;
  }
  if (u.username || u.password) return null; // userinfo is never legitimate here
  if (u.pathname !== "/" || u.search || u.hash) return null; // no extra segments
  // Per the WHATWG URL spec, an IPv6 hostname serializes WITH brackets ("[::1]"),
  // not without - accept both spellings in case that ever changes.
  if (u.hostname === "127.0.0.1" || u.hostname === "::1" || u.hostname === "[::1]") return u.host;
  return null;
}

// expandPort validates a bare port (a plain integer, 1-65535) and expands it to the
// literal loopback "127.0.0.1:<port>", or null if it is not a valid port. LOOPBACK-
// IMPLIED by design: the only cross-origin attach a browser permits (a hosted HTTPS
// console reaching a local daemon) can ONLY be 127.0.0.1 - a LAN IP is blocked as mixed
// content - so a host is impossible and the port is the only variable. It never emits
// or resolves the name "localhost"; the literal IP is used so the loopback lock stays
// checkable.
function expandPort(port: string): string | null {
  if (!/^[0-9]+$/.test(port)) return null;
  const n = Number(port);
  if (!Number.isInteger(n) || n < 1 || n > 65535) return null;
  return "127.0.0.1:" + n;
}

// normalizeDaemonHost accepts either a BARE PORT ("8787" -> "127.0.0.1:8787", the same
// literal-IP expansion #port= uses) or a full loopback host:port ("127.0.0.1:8787",
// "[::1]:8787"), returning the canonical "host:port" or null. The Settings daemon-address
// field and the connection probe run typed input through this so a user can enter just a
// port.
export function normalizeDaemonHost(input: string): string | null {
  const s = input.trim();
  if (s === "") return null;
  if (/^[0-9]+$/.test(s)) return expandPort(s);
  return validateLoopbackHost(s);
}

// loopbackPort extracts the port from a loopback host:port ("127.0.0.1:7391" -> "7391",
// "[::1]:7391" -> "7391"), or null when the host is not loopback (a LAN-share
// origin). logsLink uses it to decide whether a log-viewer deep-link needs a #port=.
function loopbackPort(host: string): string | null {
  const norm = validateLoopbackHost(host);
  if (!norm) return null;
  // Parse for the port rather than splitting on the last ":" - a bracketed IPv6 host ("[::1]")
  // with no port would otherwise slice a colon from inside the brackets. URL.port is "" when the
  // host carries no explicit port.
  try {
    return new URL("http://" + norm).port || null;
  } catch {
    // not-a-failure: norm already parsed once; no port means no #port= to add
    return null;
  }
}

// logsLink builds a log-viewer deep-link ("../logs/#...") for the daemon the console is
// talking to. A LOOPBACK daemon needs its port so the viewer can re-attach (#port=<port>);
// a LAN-share (same-origin LAN) daemon needs none - the viewer resolves its own origin -
// so only the content params (e.g. { ref } or { inv }) ride the fragment. Pass the resolved
// daemon host (or null) plus the extra content params.
export function logsLink(host: string | null, extra: Record<string, string>): string {
  return surfaceLink("logs", host, extra);
}

// surfaceLink builds a deep-link to any console surface by its canonical
// /console/<surface>/ clean path, the form the shell's boot router opens. The re-attach
// param is the same one logsLink needs and for the same reason: a console attached by
// #port= has no stored host, so a link that drops it lands the reader on a surface that
// cannot find the daemon. Content params ride the fragment beside it.
export function surfaceLink(
  surface: string,
  host: string | null,
  extra: Record<string, string> = {},
): string {
  const parts: string[] = [];
  const port = host ? loopbackPort(host) : null;
  if (port) parts.push("port=" + port);
  for (const [k, v] of Object.entries(extra)) if (v) parts.push(k + "=" + encodeURIComponent(v));
  return "../" + surface + "/" + (parts.length ? "#" + parts.join("&") : "");
}

// ---- host resolution + read-only LAN share ---------------------------------

// ownOrigin: the console adopted the page's OWN origin as the daemon (it was served BY
// the daemon and carries a token). Covers BOTH a device on a LAN share and the operator's
// own daemon-origin console. readOnly narrows that to the look-only case (a non-loopback LAN
// origin: a phone, a TV, any device on the share); the operator's loopback console keeps full control.
let ownOrigin = false;
let readOnly = false;

// isReadOnly reports whether the console is a read-only viewer: a phone, a TV, or any
// device loaded from the daemon's LAN share origin (see adoptDaemonOrigin). The shell uses
// it to hide loopback-tier actions (Share itself, and any mutating control): a shared
// session is a look, not a touch.
export function isReadOnly(): boolean {
  return readOnly;
}

// adoptDaemonOrigin detects a page that must adopt its OWN origin as the daemon: a
// page carrying a #token= fragment (or a token already stashed from one) that was served
// BY the daemon. Two audiences reach it:
//   - a device that opened a LAN share link (NON-loopback page origin): a read-only "look,
//     not touch" view, so it ALSO becomes read-only and the shell hides every mutating
//     control.
//   - the operator's own console, opened by a minted daemon-origin link served from
//     127.0.0.1:<port>/console/ (LOOPBACK page origin): it adopts its origin as the daemon
//     too, but keeps FULL control - it is the operator's own console, not a shared viewer,
//     so it does NOT become read-only.
// It records the adoption (ownOrigin) so resolveDaemonHost returns location.host directly -
// no fragment synthesis - and consumes the token. A page carrying an explicit #port= is a
// cross-origin attach to a LOCAL daemon (the operator reaching past their origin), not
// own-origin adoption: it just consumes any token and keeps full control. On a page with no
// token and no #port it does nothing. Call it once, before anything reads the hash. Returns
// whether the console became READ-ONLY.
export function adoptDaemonOrigin(): boolean {
  if (typeof location === "undefined") return false;
  const params = parseHash();
  // An explicit #port attach reaches a loopback daemon from wherever the console is hosted:
  // full control, and the daemon is 127.0.0.1 (not this origin). Just consume any token.
  if (params.port !== undefined) {
    consumeLiveToken(params);
    return false;
  }
  if (params.token === undefined && getLiveToken() === null) return false; // not our flow

  ownOrigin = true;
  // Only a device on a NON-loopback LAN share origin becomes read-only; the
  // operator's own loopback console adopts its origin but keeps full control. localhost
  // counts as loopback here too - a page served from localhost is the operator's own machine.
  const hn = location.hostname;
  const loopback = hn === "127.0.0.1" || hn === "::1" || hn === "[::1]" || hn === "localhost";
  if (!loopback) readOnly = true;

  consumeLiveToken(params); // stash + strip the token
  return readOnly;
}

// daemonAttach resolves the daemon host for an EXPLICIT attach only, or null. This is the
// faithful replacement for the old `params.live ? validateLiveHost(params.live) : null`:
// surfaces that go live only on an explicit directive (the graph explorer, the log viewer)
// use it, so a mere configured default never forces them into live mode.
//   1. #port=<port>  -> 127.0.0.1:<port> (loopback-implied; wins over origin adoption, since
//      a hosted page carrying #port is deliberately reaching a local daemon).
//   2. own-origin adoption (a LAN-share viewer, or the operator's daemon-origin console) ->
//      location.host, the exact origin the console was served from. NEVER a loopback
//      expansion for a LAN-share viewer: it must keep talking to the LAN IP:port it loaded from.
export function daemonAttach(params: HashParams = parseHash()): string | null {
  if (params.port !== undefined) return expandPort(params.port);
  if (ownOrigin && typeof location !== "undefined") return location.host;
  return null;
}

// resolveDaemonHost is the single source of truth for "which daemon do I talk to": an
// explicit attach (daemonAttach) if there is one, else the operator's configured default
// (Settings, a loopback host). Surfaces that auto-connect to a configured daemon (readiness
// polling, the dashboard, activity, the version chip, sharing) use this; explicit-only
// surfaces use daemonAttach. Returns the daemon "host:port" or null when nothing resolves.
export function resolveDaemonHost(params: HashParams = parseHash()): string | null {
  const attach = daemonAttach(params);
  if (attach) return attach;
  const configured = getDefaultHost();
  // normalizeDaemonHost (not validateLoopbackHost) so a stored bare port resolves the same way the
  // Settings field accepts one - "8787" expands to 127.0.0.1:8787 rather than reading as unset.
  return configured ? normalizeDaemonHost(configured) : null;
}

// resolveDaemonHostOrRemembered is resolveDaemonHost, then the last daemon the dashboard reached
// (loopback only). Surfaces that read a daemon on mount use it, so a reader who has connected once
// is not asked again after a reload with no link in the URL.
export function resolveDaemonHostOrRemembered(params: HashParams = parseHash()): string | null {
  const resolved = resolveDaemonHost(params);
  if (resolved) return resolved;
  const remembered = getRememberedHost();
  return remembered ? validateLoopbackHost(remembered) : null;
}

// ---- reachability probe ----------------------------------------------------

export type ProbeResult = { ok: true; url: string } | { ok: false; reason: string };

// probeDaemon answers "is anything listening at this host:port?" for the Settings test-connection
// control. /livez is the daemon's only tokenless route (health checks are mounted unguarded so a
// kubelet can reach them). Current daemons also wrap /livez and /readyz in the same CORSAllow
// list as the console bridge, but this probe still uses mode: "no-cors": resolve-vs-reject is
// enough for "a server answered", and the body/status stay opaque by design. Success therefore
// means "a server answered", NOT "magus is healthy". The browser also refuses to distinguish a
// refused connection from a CORS/mixed-content block, so those collapse into one honest message.
export async function probeDaemon(hostPort: string, timeoutMs = 3000): Promise<ProbeResult> {
  const host = normalizeDaemonHost(hostPort);
  if (!host) {
    return {
      ok: false,
      reason:
        "Not a loopback address. Use a port (for example 8787) or 127.0.0.1 or [::1] with a port. Hostnames, localhost included, are not accepted.",
    };
  }
  const url = "http://" + host + "/livez";
  try {
    await fetch(url, {
      mode: "no-cors",
      cache: "no-store",
      signal: AbortSignal.timeout(timeoutMs),
    });
    return { ok: true, url };
  } catch (e) {
    if (errName(e) === "TimeoutError" || errName(e) === "AbortError") {
      return {
        ok: false,
        reason:
          "No response from " +
          url +
          " within " +
          Math.round(timeoutMs / 1000) +
          "s. Check the port, or something is dropping the connection.",
      };
    }
    // Deliberately no status code or errno: an opaque request surfaces one bare TypeError for every
    // network-layer failure (refused, CORS-blocked, mixed content). The browser withholds the detail, so
    // naming a cause here would be a guess.
    return {
      ok: false,
      reason:
        "Could not reach " + url + ". Is the daemon running? Start it with: magus server start",
    };
  }
}

// ---- readiness probe --------------------------------------------------------

// ReadinessComponent/ReadinessReport mirror the daemon's GET /readyz JSON body. Component names are
// currently "workspaces", "symbol_index", "services", "knowledge_graph"; status is one of
// "ok"|"degraded"|"down"|"idle"|"disabled". Kept as bare strings (not a union) because this is parsed
// from the network - a future daemon component or status value must not fail to typecheck against a
// stale frontend union, it should just render as unrecognized text.
export type ReadinessComponent = { name: string; status: string; detail: string };
export type ReadinessReport = { ready: boolean; components: ReadinessComponent[] };

// fetchReadiness reads GET /readyz for the daemon's own component-level health (workspaces, symbol
// index, services, knowledge graph) - richer than probeDaemon's bare "did anything answer". Unlike
// /livez, /readyz answers WITH CORS headers and a JSON body on current daemons, so this is a normal
// (cors-mode) fetch whose response is actually readable, not an opaque no-cors probe. No Authorization
// header rides along: the health routes are tokenless by design (a kubelet cannot supply a bearer
// token), and adding one would turn a simple GET into a needlessly CORS-preflighted request.
//
// WHY graceful degradation: the daemon the caller is pointed at may PREDATE this endpoint's CORS/JSON
// support. Every failure mode here - network error, timeout, an old daemon's opaque/non-CORS response,
// a non-200/503 status, a malformed body - must resolve to null quietly (no thrown error, no
// console.error), so a caller can fall back to the existing SSE-derived connection state without
// spamming the console for anyone still on an older release. 200 and 503 both carry a valid body (503
// just means "not ready yet"), so both are treated as a successful read.
export async function fetchReadiness(
  host: string,
  timeoutMs = 3000,
): Promise<ReadinessReport | null> {
  if (!host) return null;
  // Defense in depth: the caller passes an already-resolved host (resolveDaemonHost), but re-verify
  // it is literal loopback OR the page's OWN origin before attaching the bearer token, so a future
  // caller that ever passes a raw string can never send the token to a third-party host. The
  // LAN-share host is same-origin, so it passes here where validateLoopbackHost alone would not.
  const safe =
    validateLoopbackHost(host) ??
    (typeof location !== "undefined" && host === location.host ? host : null);
  if (!safe) return null;
  const url = "http://" + safe + "/readyz";
  try {
    const res = await fetch(url, { cache: "no-store", signal: AbortSignal.timeout(timeoutMs) });
    if (res.status !== 200 && res.status !== 503) return null;
    const body = await res.json();
    if (typeof body !== "object" || body === null || typeof body.ready !== "boolean") return null;
    const components: ReadinessComponent[] = Array.isArray(body.components)
      ? body.components.map((c: Record<string, unknown>) => ({
          name: String(c?.name ?? ""),
          status: String(c?.status ?? ""),
          detail: String(c?.detail ?? ""),
        }))
      : [];
    return { ready: body.ready, components };
  } catch {
    // not-a-failure: an older daemon has no JSON /readyz; reachability itself is reported by the
    // daemon transport and fetchSSE. A timeout, a refused connection and a non-JSON body all
    // collapse to the one "could not read" signal a caller falls back from.
    return null;
  }
}

// ---- the shared token ------------------------------------------------------

const TOKEN_KEY = "magus-live-token";
const REMEMBER_KEY = "magus-live-remember";
// SCOPED_KEY records that the stored bearer has already been exchanged for a
// console-scoped token, so the exchange runs once rather than minting a fresh token on
// every page load. It is CLEARED whenever a new token arrives, because a fresh paste may
// be the operator token again and would then need exchanging in its turn.
const SCOPED_KEY = "magus-live-scoped";

// consumeLiveToken stashes the bearer token from the URL fragment and strips ONLY
// the token from the fragment (keeping #port= and any other keys intact so a reload
// stays in live mode). Stored in sessionStorage by default, or
// localStorage when the user opted to remember it. The secret never lingers in
// the URL bar, history, or a copied link.
export function consumeLiveToken(params: HashParams): void {
  if (!params.token) return;
  const remembered = isRemembered();
  try {
    // Write one store and CLEAR the other. getLiveToken reads sessionStorage first, so a
    // token left behind there outranks a newer one written to localStorage: open a second
    // share link while "remember" is on and every request goes out signed with the dead
    // token, which comes back Unauthenticated and reads to the user as "not available".
    (remembered ? localStorage : sessionStorage).setItem(TOKEN_KEY, params.token);
    (remembered ? sessionStorage : localStorage).removeItem(TOKEN_KEY);
    // A new token is an unknown tier until something proves otherwise.
    sessionStorage.removeItem(SCOPED_KEY);
    localStorage.removeItem(SCOPED_KEY);
  } catch (e) {
    reportFailure(
      "Sign-in",
      "This browser refused to store the token from your link (" +
        errMessage(e) +
        "), so the console cannot sign in. Allow site data for this address and open the link again.",
      "token:storage",
    );
  }
  const kept: string[] = [];
  for (const k of Object.keys(params)) {
    if (k === "token") continue;
    // A bare fragment key (value "") is re-emitted bare so line tokens like "L10-L20" survive.
    kept.push(params[k] === "" ? k : k + "=" + encodeURIComponent(params[k]));
  }
  const next = kept.length ? "#" + kept.join("&") : "";
  history.replaceState(null, "", location.pathname + location.search + next);
}

export function getLiveToken(): string | null {
  try {
    return sessionStorage.getItem(TOKEN_KEY) || localStorage.getItem(TOKEN_KEY) || null;
  } catch {
    // reported: consumeLiveToken reports a store that refused the token; reading one finds nothing
    return null;
  }
}

// setLiveToken replaces the stored bearer. It writes the store isRemembered() selects and
// clears the other, for the same reason consumeLiveToken does: getLiveToken reads
// sessionStorage first, so a stale copy left there would outrank the token just written.
// Returns false when storage is unavailable, which the caller must treat as "not stored"
// rather than assuming the swap happened.
export function setLiveToken(token: string): boolean {
  try {
    const remembered = isRemembered();
    (remembered ? localStorage : sessionStorage).setItem(TOKEN_KEY, token);
    (remembered ? sessionStorage : localStorage).removeItem(TOKEN_KEY);
    return true;
  } catch {
    // reported: false is "not stored", which the caller reports
    return false;
  }
}

// clearLiveToken forgets the stored bearer and its exchange mark, in both stores.
export function clearLiveToken(): void {
  try {
    for (const store of [sessionStorage, localStorage]) {
      store.removeItem(TOKEN_KEY);
      store.removeItem(SCOPED_KEY);
    }
  } catch {
    // not-a-failure: storage is disabled, so there was no stored token to forget
  }
}

// hasScopedToken reports whether the stored bearer has already been exchanged. It is a
// cache, not a security check: a false negative costs one refused RPC, and the tier is
// enforced by the daemon either way.
export function hasScopedToken(): boolean {
  try {
    return (sessionStorage.getItem(SCOPED_KEY) || localStorage.getItem(SCOPED_KEY)) === "1";
  } catch {
    // not-a-failure: a cache miss costs one refused RPC; the daemon enforces the tier
    return false;
  }
}

// markScopedToken records a completed exchange, in the same store the token went to.
export function markScopedToken(): void {
  try {
    (isRemembered() ? localStorage : sessionStorage).setItem(SCOPED_KEY, "1");
  } catch {
    // not-a-failure: storage disabled, so the exchange simply reruns next load
  }
}

export function isRemembered(): boolean {
  try {
    return localStorage.getItem(REMEMBER_KEY) === "1";
  } catch {
    // not-a-failure: with storage disabled nothing is remembered, which is what this answers
    return false;
  }
}

// setRemembered promotes/demotes the token between session and local storage when
// the user toggles a "remember this daemon" control.
export function setRemembered(on: boolean): void {
  try {
    if (on) {
      localStorage.setItem(REMEMBER_KEY, "1");
      const t = getLiveToken();
      if (t) localStorage.setItem(TOKEN_KEY, t);
    } else {
      localStorage.removeItem(REMEMBER_KEY);
      localStorage.removeItem(TOKEN_KEY);
    }
  } catch (e) {
    reportFailure(
      "Settings",
      "Could not " + (on ? "remember" : "forget") + " this daemon's token: " + errMessage(e),
      "token:remember",
    );
  }
}

// authHeaders builds the Authorization header for a live-mode fetch, or {} when
// no token is stored (a daemon started without connector auth).
export function authHeaders(token: string | null = getLiveToken()): Record<string, string> {
  return token ? { Authorization: "Bearer " + token } : {};
}

// ---- fetch-based SSE reader ------------------------------------------------

export type SSEHeaders = Record<string, string>;

// fetchSSE: fetch-based Server-Sent Events reader. Does NOT use EventSource
// because EventSource cannot send an Authorization header. Reads response.body via
// TextDecoderStream, splits on \n\n, parses event:/data: lines. Calls onOpen()
// once the stream is confirmed open (200 response, before the first event) so the
// caller can reset reconnect backoff and refresh stale data. On stream end or
// error, calls onError(err) for the caller to schedule reconnect. An AbortError
// (a superseding connect, or teardown) is deliberately silent in both the initial
// fetch and the read loop - it is not a connection failure, and treating it as one
// would stack up redundant reconnect attempts.
//
// Every failure is also REPORTED here (reportFailure, keyed per stream so a reconnect loop reports
// it once), as is an onEvent that throws: a frame the page cannot decode is reported and skipped,
// and the stream keeps reading. A 401 signs the console out (signalAuthLost).
export async function fetchSSE(
  url: string,
  headers: SSEHeaders,
  onEvent: (type: string, data: string) => void,
  onError: (e: Error) => void,
  signal: AbortSignal,
  onOpen?: () => void,
): Promise<void> {
  const fail = (e: Error): void => {
    reportFailure(
      "Daemon",
      "Lost the live feed from " + url + " (" + e.message + "); retrying.",
      "sse:" + url + ":" + e.message,
    );
    onError(e);
  };
  let response: Response;
  try {
    response = await fetch(url, { headers, signal });
  } catch (e) {
    if (e instanceof Error && e.name === "AbortError") return;
    fail(e instanceof Error ? e : new Error(String(e)));
    return;
  }
  if (response.status === 401) {
    signalAuthLost(new URL(url).host);
    onError(new Error("HTTP 401"));
    return;
  }
  if (!response.ok) {
    fail(new Error("HTTP " + response.status));
    return;
  }
  if (onOpen) onOpen();
  if (!response.body) {
    fail(new Error("no stream body"));
    return;
  }
  const deliver = (type: string, data: string): void => {
    try {
      onEvent(type, data);
    } catch (e) {
      reportFailure(
        "Daemon",
        "Could not read a " + type + " event from " + url + ": " + errMessage(e),
        "sse:decode:" + url + ":" + type,
      );
    }
  };
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        fail(new Error("stream ended"));
        return;
      }
      buf += value;
      // A frame ends at the first blank line, spelled either "\n\n" (magus's
      // framing) or "\r\n\r\n" (CRLF framing). Split on whichever boundary comes
      // first so both are honored regardless of the producer's line endings.
      while (true) {
        const lf = buf.indexOf("\n\n");
        const crlf = buf.indexOf("\r\n\r\n");
        let boundary: number, sep: number;
        if (crlf >= 0 && (lf < 0 || crlf < lf)) {
          boundary = crlf;
          sep = 4;
        } else if (lf >= 0) {
          boundary = lf;
          sep = 2;
        } else break;
        const chunk = buf.slice(0, boundary);
        buf = buf.slice(boundary + sep);
        if (!chunk.trim()) continue;
        let eventType = "message";
        const dataLines: string[] = [];
        for (const line of chunk.split(/\r?\n/)) {
          // Tolerate both "event: status" (SSE convention, space after colon) and
          // "event:status" (no space): the SSE field parse only requires the colon.
          if (line.startsWith("event:")) eventType = line.slice(6).replace(/^ /, "").trim();
          // Per the SSE spec an event may carry multiple "data:" lines; collect
          // them all and join with "\n" (a single frame yields the same string as
          // before). Strip one leading space per line, no more.
          else if (line.startsWith("data:")) dataLines.push(line.slice(5).replace(/^ /, ""));
        }
        deliver(eventType, dataLines.join("\n"));
      }
    }
  } catch (e) {
    if (!(e instanceof Error) || e.name !== "AbortError")
      fail(e instanceof Error ? e : new Error(String(e)));
  }
}

// ---- ConnectRPC transport --------------------------------------------------

// makeBearerInterceptor stamps the shared bearer token on every ConnectRPC request.
function makeBearerInterceptor(token: string | null): Interceptor {
  return (next) => async (req) => {
    if (token) req.header.set("Authorization", "Bearer " + token);
    return await next(req);
  };
}

// The services a client may be DENIED by design (see isCapabilityDenied): a denial from one of these
// hides a section rather than failing anything, so it is the one error the transport does not report.
const CAPABILITY_GATED = new Set([
  "magus.token.v1alpha1.TokenService",
  "magus.memory.v1alpha1.MemoryService",
]);

// reportRpcFailure is the transport's half of the rule that every failure reaches the person. The
// caller still receives the error; this only makes sure it cannot vanish into an empty list.
export function reportRpcFailure(host: string, service: string, method: string, e: unknown): void {
  if (e instanceof ConnectError && e.code === Code.Canceled && !(e.cause instanceof TypeError))
    return; // the caller aborted: a superseded poll or a closed pane, not a failure
  if (CAPABILITY_GATED.has(service) && isCapabilityDenied(e)) return;
  if (e instanceof ConnectError && e.code === Code.Unauthenticated) {
    signalAuthLost(host);
    return;
  }
  if (isUnreachable(e)) {
    reportFailure(
      "Daemon",
      "Could not reach the daemon at " + host + ". Start it with `magus server start`.",
      "daemon:unreachable:" + host,
    );
    return;
  }
  const code = e instanceof ConnectError ? Code[e.code] : "Error";
  const detail = e instanceof ConnectError ? e.rawMessage : errMessage(e);
  const name = service.slice(service.lastIndexOf(".") + 1) + "." + method;
  reportFailure(
    "Daemon",
    name + " failed: " + detail,
    "rpc:" + service + "/" + method + ":" + code,
  );
}

// reportFetchFailure is reportRpcFailure for a plain fetch to the daemon (the /api/v1 routes that
// are not Connect services). what names the read in the message: "the review session".
export function reportFetchFailure(host: string, what: string, e: unknown): void {
  if (errName(e) === "AbortError") return;
  if (isUnreachable(e)) {
    reportRpcFailure(host, "", "", e);
    return;
  }
  reportFailure("Daemon", "Could not read " + what + ": " + errMessage(e), "fetch:" + what);
}

// reportHttpStatus reports a plain fetch the daemon answered with a non-2xx status. A 401 signs the
// console out, as it does for a Connect call.
export function reportHttpStatus(host: string, what: string, status: number): void {
  if (status === 401) {
    signalAuthLost(host);
    return;
  }
  reportFailure(
    "Daemon",
    "Could not read " + what + ": the daemon answered HTTP " + status + ".",
    "http:" + what + ":" + status,
  );
}

// Refusal is what a person needs from a daemon error body: its message, and the Help link naming
// the MGS code's page when the body carries one.
export interface Refusal {
  message: string;
  help?: NotifyLink;
}

const helpDetailType = "type.googleapis.com/google.rpc.Help";

// parseRefusal reads a decoded /api/ error body in AIP-193's HTTP/1.1+JSON shape,
// {"error":{"code","message","status","details"}}. Null for any other shape, so the caller keeps
// its own message rather than showing a raw body.
export function parseRefusal(body: unknown): Refusal | null {
  if (typeof body !== "object" || body === null) return null;
  const err = (body as { error?: unknown }).error;
  if (typeof err !== "object" || err === null) return null;
  const { message, details } = err as { message?: unknown; details?: unknown };
  if (typeof message !== "string" || message === "") return null;
  const refusal: Refusal = { message };
  for (const d of Array.isArray(details) ? details : []) {
    if (
      typeof d !== "object" ||
      d === null ||
      (d as { "@type"?: unknown })["@type"] !== helpDetailType
    )
      continue;
    const links = (d as { links?: unknown }).links;
    const first: unknown = Array.isArray(links) ? links[0] : undefined;
    if (typeof first !== "object" || first === null) continue;
    const { description, url } = first as { description?: unknown; url?: unknown };
    if (typeof url === "string" && url !== "")
      refusal.help = {
        label: typeof description === "string" && description !== "" ? description : "Help",
        href: url,
      };
    break;
  }
  return refusal;
}

// makeFailureInterceptor reports every failed call, a server stream's mid-stream failure included:
// that one surfaces while the caller iterates, after next() has already returned.
function makeFailureInterceptor(host: string): Interceptor {
  return (next) => async (req) => {
    const report = (e: unknown): void =>
      reportRpcFailure(host, req.service.typeName, req.method.name, e);
    try {
      const res = await next(req);
      if (!res.stream) return res;
      const inner = res.message;
      async function* guarded() {
        try {
          yield* inner;
        } catch (e) {
          report(e);
          throw e;
        }
      }
      return { ...res, message: guarded() };
    } catch (e) {
      report(e);
      throw e;
    }
  };
}

// createDaemonTransport points a browser-native Connect transport at the daemon
// origin, with the bearer interceptor pre-wired. Callers pass an already-resolved
// host (resolveDaemonHost/daemonAttach) - never a raw fragment string.
export function createDaemonTransport(
  host: string,
  token: string | null = getLiveToken(),
): Transport {
  return createConnectTransport({
    baseUrl: "http://" + host,
    interceptors: [makeFailureInterceptor(host), makeBearerInterceptor(token)],
  });
}

// ---- sign-in ---------------------------------------------------------------

// AUTH_LOST_EVENT fires on document when the daemon refuses the stored token. Every bundle shares
// document, so the shell's sign-in gate hears a 401 raised inside any surface.
export const AUTH_LOST_EVENT = "magus:auth-lost";

// signalAuthLost forgets a token the daemon refused (expired or revoked) and says so. Keeping it
// would sign every later request with a credential that can only fail, and each surface would read
// that as an empty page.
export function signalAuthLost(host: string): void {
  clearLiveToken();
  if (typeof document !== "undefined")
    document.dispatchEvent(new CustomEvent(AUTH_LOST_EVENT, { detail: { host } }));
  reportFailure(
    "Sign-in",
    "The daemon at " +
      host +
      " refused this console's token: it expired or was revoked. Sign in again.",
    "auth:lost:" + host,
  );
}

// signInCommand is the shell line that opens url signed in. The token is a substitution the reader's
// shell expands, so the page never holds or displays it. The opener follows the browser's platform,
// which is the machine a loopback daemon runs on. Windows gets PowerShell's opener, since cmd.exe
// never expands $(...).
export function signInCommand(url: string, platform = browserPlatform()): string {
  const sep = url.includes("#") ? "&" : "#";
  const opener = /mac/i.test(platform)
    ? "open"
    : /win/i.test(platform)
      ? "Start-Process"
      : "xdg-open";
  return opener + ' "' + url + sep + 'token=$(magus config token print)"';
}

function browserPlatform(): string {
  return typeof navigator === "undefined" ? "" : navigator.platform || navigator.userAgent || "";
}

// ---- connection state ------------------------------------------------------

// ConnState is the connection lifecycle a page reflects into its UI. Each page
// drives its own reconnect loop (see the module header); this is the shared
// vocabulary for the resulting state.
export type ConnState = "connecting" | "connected" | "disconnected";
