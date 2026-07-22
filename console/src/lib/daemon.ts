import { errName } from "./must";
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
//     own-origin adoption (shared phone view / the operator's daemon-origin console),
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
import { getDefaultHost } from "./settings";

// isCapabilityDenied reports whether a Connect RPC error is the daemon DECLINING the capability to
// this client (not a transient outage). A read-only phone-share session cannot reach TokenService
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

// ---- the loopback lock -----------------------------------------------------

// validateLoopbackHost: a configured/entered daemon host MUST be literally 127.0.0.1
// or [::1] with a port. localhost, hostnames, and other IPs are rejected before any
// network request. Parses hostPort as a REAL URL rather than splitting on the last
// ":" - a naive split lets a URL-userinfo "@" smuggle an attacker host past the check
// (e.g. "127.0.0.1:7391@evil.com" splits to host "127.0.0.1", but a browser fetching
// "http://127.0.0.1:7391@evil.com" actually connects to evil.com and would send it the
// bearer token). Returns the normalized "host:port" (brackets kept for IPv6) on
// success, or null on any rejection. Shared mode's same-origin LAN host is NOT accepted
// here - that path resolves through resolveDaemonHost/location.host directly, so this
// pure loopback check makes the docs claim "data cannot leave your machine" verifiable.
export function validateLoopbackHost(hostPort: string): string | null {
  let u: URL;
  try {
    u = new URL("http://" + hostPort);
  } catch {
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
// "[::1]:7391" -> "7391"), or null when the host is not loopback (a shared-mode LAN
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
    return null;
  }
}

// logsLink builds a log-viewer deep-link ("../logs/#...") for the daemon the console is
// talking to. A LOOPBACK daemon needs its port so the viewer can re-attach (#port=<port>);
// a shared-mode (same-origin LAN) daemon needs none - the viewer resolves its own origin -
// so only the content params (e.g. { ref } or { inv }) ride the fragment. Pass the resolved
// daemon host (or null) plus the extra content params.
export function logsLink(host: string | null, extra: Record<string, string>): string {
  const parts: string[] = [];
  const port = host ? loopbackPort(host) : null;
  if (port) parts.push("port=" + port);
  for (const [k, v] of Object.entries(extra)) if (v) parts.push(k + "=" + encodeURIComponent(v));
  return "../logs/" + (parts.length ? "#" + parts.join("&") : "");
}

// ---- host resolution + shared mode (share to phone) ------------------------

// ownOrigin: the console adopted the page's OWN origin as the daemon (it was served BY
// the daemon and carries a token). Covers BOTH a phone on a LAN share and the operator's
// own daemon-origin console. sharedMode is the read-only SUBSET (a phone on a non-loopback
// LAN origin); the operator's loopback console adopts its origin but keeps full control.
let ownOrigin = false;
let sharedMode = false;

// isSharedMode reports whether the console is running as a read-only phone viewer
// loaded from the daemon's LAN share origin (see enterSharedModeIfNeeded). The
// shell uses it to hide loopback-tier actions (Share to phone itself, and any
// mutating control) - a shared session is a look, not a touch.
export function isSharedMode(): boolean {
  return sharedMode;
}

// enterSharedModeIfNeeded detects a page that must adopt its OWN origin as the daemon: a
// page carrying a #token= fragment (or a token already stashed from one) that was served
// BY the daemon. Two audiences reach it:
//   - a phone that opened a LAN share link (NON-loopback page origin): a read-only "look,
//     not touch" view, so it ALSO enters shared mode and the shell hides every mutating
//     control.
//   - the operator's own console, opened by a minted daemon-origin link served from
//     127.0.0.1:<port>/console/ (LOOPBACK page origin): it adopts its origin as the daemon
//     too, but keeps FULL control - it is the operator's own console, not a shared phone
//     view, so it does NOT enter read-only shared mode.
// It records the adoption (ownOrigin) so resolveDaemonHost returns location.host directly -
// no fragment synthesis - and consumes the token. A page carrying an explicit #port= is a
// cross-origin attach to a LOCAL daemon (the operator reaching past their origin), not
// own-origin adoption: it just consumes any token and keeps full control. On a page with no
// token and no #port it does nothing. Call it once, before anything reads the hash. Returns
// whether READ-ONLY shared mode was entered.
export function enterSharedModeIfNeeded(): boolean {
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
  // Only a phone on a NON-loopback LAN share origin drops to read-only shared mode; the
  // operator's own loopback console adopts its origin but keeps full control. localhost
  // counts as loopback here too - a page served from localhost is the operator's own machine.
  const hn = location.hostname;
  const loopback = hn === "127.0.0.1" || hn === "::1" || hn === "[::1]" || hn === "localhost";
  if (!loopback) sharedMode = true;

  consumeLiveToken(params); // stash + strip the token
  return sharedMode;
}

// daemonAttach resolves the daemon host for an EXPLICIT attach only, or null. This is the
// faithful replacement for the old `params.live ? validateLiveHost(params.live) : null`:
// surfaces that go live only on an explicit directive (the graph explorer, the log viewer)
// use it, so a mere configured default never forces them into live mode.
//   1. #port=<port>  -> 127.0.0.1:<port> (loopback-implied; wins over origin adoption, since
//      a hosted page carrying #port is deliberately reaching a local daemon).
//   2. own-origin adoption (shared phone view, or the operator's daemon-origin console) ->
//      location.host, the exact origin the console was served from. NEVER a loopback
//      expansion for a phone: it must keep talking to the LAN IP:port it loaded from.
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

// ---- reachability probe ----------------------------------------------------

export type ProbeResult = { ok: true; url: string } | { ok: false; reason: string };

// probeDaemon answers "is anything listening at this host:port?" for the Settings test-connection
// control. /livez is the daemon's only tokenless route (health checks are mounted unguarded so a kubelet
// can reach them), but it carries no CORS headers, so its RESPONSE is unreadable from another origin. A
// no-cors request still gets sent, and resolve-vs-reject reports whether the connection was answered -
// which is the question being asked. Success therefore means "a server answered", NOT "magus is healthy":
// the body, and even the status code, are opaque. The browser also refuses to distinguish a refused
// connection from a CORS/mixed-content block, so those collapse into one honest message.
export async function probeDaemon(hostPort: string, timeoutMs = 3000): Promise<ProbeResult> {
  const host = normalizeDaemonHost(hostPort);
  if (!host) {
    return {
      ok: false,
      reason:
        "Not a loopback address. Use a port (for example 8787) or 127.0.0.1 or [::1] with a port - hostnames (including localhost) are not accepted.",
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
  // shared-mode LAN host is same-origin, so it passes here where validateLoopbackHost alone would not.
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
    // Covers the timeout, a refused/CORS-blocked connection, and a response body that is not valid
    // JSON (an old daemon serving a plain-text /readyz, or none at all) - all collapse to the one
    // "could not read" signal a caller falls back from.
    return null;
  }
}

// ---- the shared token ------------------------------------------------------

const TOKEN_KEY = "magus-live-token";
const REMEMBER_KEY = "magus-live-remember";

// consumeLiveToken stashes the bearer token from the URL fragment and strips ONLY
// the token from the fragment (keeping #port= and any other keys intact so a reload
// stays in live mode). Stored in sessionStorage by default, or
// localStorage when the user opted to remember it. The secret never lingers in
// the URL bar, history, or a copied link.
export function consumeLiveToken(params: HashParams): void {
  if (!params.token) return;
  const store = isRemembered() ? localStorage : sessionStorage;
  try {
    store.setItem(TOKEN_KEY, params.token);
  } catch {
    /* storage disabled: token lives only for this call chain */
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
    return null;
  }
}

export function isRemembered(): boolean {
  try {
    return localStorage.getItem(REMEMBER_KEY) === "1";
  } catch {
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
  } catch {
    /* ignore */
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
export async function fetchSSE(
  url: string,
  headers: SSEHeaders,
  onEvent: (type: string, data: string) => void,
  onError: (e: Error) => void,
  signal: AbortSignal,
  onOpen?: () => void,
): Promise<void> {
  let response: Response;
  try {
    response = await fetch(url, { headers, signal });
  } catch (e) {
    if (e instanceof Error && e.name === "AbortError") return;
    onError(e instanceof Error ? e : new Error(String(e)));
    return;
  }
  if (!response.ok) {
    onError(new Error("HTTP " + response.status));
    return;
  }
  if (onOpen) onOpen();
  if (!response.body) {
    onError(new Error("no stream body"));
    return;
  }
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) {
        onError(new Error("stream ended"));
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
        onEvent(eventType, dataLines.join("\n"));
      }
    }
  } catch (e) {
    if (!(e instanceof Error) || e.name !== "AbortError")
      onError(e instanceof Error ? e : new Error(String(e)));
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

// createDaemonTransport points a browser-native Connect transport at the daemon
// origin, with the bearer interceptor pre-wired. Callers pass an already-resolved
// host (resolveDaemonHost/daemonAttach) - never a raw fragment string.
export function createDaemonTransport(
  host: string,
  token: string | null = getLiveToken(),
): Transport {
  return createConnectTransport({
    baseUrl: "http://" + host,
    interceptors: [makeBearerInterceptor(token)],
  });
}

// ---- connection state ------------------------------------------------------

// ConnState is the connection lifecycle a page reflects into its UI. Each page
// drives its own reconnect loop (see the module header); this is the shared
// vocabulary for the resulting state.
export type ConnState = "connecting" | "connected" | "disconnected";
