// daemon.ts - the ONE audited module for talking to a loopback magus daemon.
//
// Two tool pages (the dashboard and the graph explorer) import these security-
// critical helpers, which used to be copy-pasted between them (validateLiveHost,
// consumeLiveToken, getLiveToken, fetchSSE). They live here, imported once, so the
// loopback lock and the shared token keys have a single home.
//
// What this module owns:
//   - the LOOPBACK LOCK: validateLiveHost() - the pure host check that makes the
//     "your data never leaves your machine" claim verifiable. Every live-mode
//     fetch is built from its normalized return, never from the raw fragment.
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
import type { Interceptor, Transport } from "@connectrpc/connect";

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
    try { return decodeURIComponent(s); } catch { return s; }
  };
  const h = location.hash.replace(/^#/, "");
  const params: HashParams = {};
  for (const part of h.split("&")) {
    if (!part) continue;
    const i = part.indexOf("=");
    if (i < 0) { params[part] = ""; continue; }
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

// validateLiveHost: the host in #live= MUST be literally 127.0.0.1 or [::1].
// localhost, hostnames, and other IPs are rejected before any network request.
// Parses hostPort as a REAL URL rather than splitting on the last ":" - a naive
// split lets a URL-userinfo "@" smuggle an attacker host past the check (e.g.
// "127.0.0.1:7391@evil.com" splits to host "127.0.0.1", but a browser fetching
// "http://127.0.0.1:7391@evil.com" actually connects to evil.com and would send
// it the bearer token). Returns the normalized "host:port" (brackets kept for
// IPv6) on success, or null on any rejection. Every subsequent live-mode fetch
// is built from this normalized value, never from the raw fragment string, so
// this check is what makes the docs claim "data cannot leave your machine" verifiable.
export function validateLiveHost(hostPort: string): string | null {
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
  if (u.hostname !== "127.0.0.1" && u.hostname !== "::1" && u.hostname !== "[::1]") return null;
  return u.host;
}

// ---- the shared token ------------------------------------------------------

const TOKEN_KEY = "magus-live-token";
const REMEMBER_KEY = "magus-live-remember";

// consumeLiveToken stashes the bearer token from the URL fragment and strips ONLY
// the token from the fragment (keeping #live=host:port and any other keys intact
// so a reload stays in live mode). Stored in sessionStorage by default, or
// localStorage when the user opted to remember it. The secret never lingers in
// the URL bar, history, or a copied link.
export function consumeLiveToken(params: HashParams): void {
  if (!params.token) return;
  const store = isRemembered() ? localStorage : sessionStorage;
  try { store.setItem(TOKEN_KEY, params.token); } catch { /* storage disabled: token lives only for this call chain */ }
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
  try { return localStorage.getItem(REMEMBER_KEY) === "1"; } catch { return false; }
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
  } catch { /* ignore */ }
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
  if (!response.body) { onError(new Error("no stream body")); return; }
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let buf = "";
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) { onError(new Error("stream ended")); return; }
      buf += value;
      // A frame ends at the first blank line, spelled either "\n\n" (magus's
      // framing) or "\r\n\r\n" (CRLF framing). Split on whichever boundary comes
      // first so both are honored regardless of the producer's line endings.
      while (true) {
        const lf = buf.indexOf("\n\n");
        const crlf = buf.indexOf("\r\n\r\n");
        let boundary: number, sep: number;
        if (crlf >= 0 && (lf < 0 || crlf < lf)) { boundary = crlf; sep = 4; }
        else if (lf >= 0) { boundary = lf; sep = 2; }
        else break;
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
    if (!(e instanceof Error) || e.name !== "AbortError") onError(e instanceof Error ? e : new Error(String(e)));
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

// createDaemonTransport points a browser-native Connect transport at the loopback
// daemon origin, with the bearer interceptor pre-wired. Callers pass an
// already-validated host (validateLiveHost) - never a raw fragment string.
export function createDaemonTransport(host: string, token: string | null = getLiveToken()): Transport {
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
