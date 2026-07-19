import test from "node:test";
import assert from "node:assert/strict";
import {
  validateLoopbackHost, normalizeDaemonHost, daemonAttach, enterSharedModeIfNeeded, isSharedMode,
} from "./daemon";

// The loopback lock and the #port/shared-mode grammar. validateLoopbackHost is the pure
// loopback check for a configured/entered host; normalizeDaemonHost adds bare-port expansion;
// daemonAttach resolves an explicit attach (#port -> loopback, or the page's own origin in
// shared mode). #live is gone entirely.

// withLocation stubs a page origin (host, hostname, hash) for the duration of fn, then restores it.
function withLocation<T>(loc: { host: string; hostname: string; hash?: string }, fn: () => T): T {
  const g = globalThis as unknown as { location?: unknown };
  const prev = g.location;
  g.location = { hash: "", ...loc };
  try {
    return fn();
  } finally {
    if (prev === undefined) delete g.location;
    else g.location = prev;
  }
}

test("validateLoopbackHost accepts literal loopback with a port", () => {
  assert.equal(validateLoopbackHost("127.0.0.1:7391"), "127.0.0.1:7391");
  assert.equal(validateLoopbackHost("[::1]:7391"), "[::1]:7391");
});

test("validateLoopbackHost rejects non-loopback hosts, including the page origin", () => {
  // Even when the page itself is on the LAN, a raw host string is only accepted when it is
  // literal loopback - the shared-mode origin is resolved through location.host, not here.
  withLocation({ host: "192.168.1.20:54321", hostname: "192.168.1.20" }, () => {
    assert.equal(validateLoopbackHost("192.168.1.20:54321"), null);
    assert.equal(validateLoopbackHost("192.168.1.99:54321"), null);
    assert.equal(validateLoopbackHost("evil.example.com:80"), null);
    assert.equal(validateLoopbackHost("localhost:7391"), null);
  });
});

test("validateLoopbackHost refuses userinfo smuggling", () => {
  // "127.0.0.1:7391@evil.com" parses to host evil.com, which is not loopback.
  assert.equal(validateLoopbackHost("127.0.0.1:7391@evil.com"), null);
});

test("normalizeDaemonHost expands a bare port to the literal loopback IP", () => {
  assert.equal(normalizeDaemonHost("8787"), "127.0.0.1:8787");
  assert.equal(normalizeDaemonHost(" 7391 "), "127.0.0.1:7391");
});

test("normalizeDaemonHost keeps a loopback host:port and rejects everything else", () => {
  assert.equal(normalizeDaemonHost("127.0.0.1:7391"), "127.0.0.1:7391");
  assert.equal(normalizeDaemonHost("[::1]:7391"), "[::1]:7391");
  assert.equal(normalizeDaemonHost("0"), null); // port out of range
  assert.equal(normalizeDaemonHost("70000"), null); // port out of range
  assert.equal(normalizeDaemonHost("example.com:80"), null);
  assert.equal(normalizeDaemonHost(""), null);
});

test("daemonAttach expands #port to the literal loopback IP, rejecting a bad port", () => {
  assert.equal(daemonAttach({ port: "8787" }), "127.0.0.1:8787");
  assert.equal(daemonAttach({ port: "70000" }), null);
  assert.equal(daemonAttach({ port: "abc" }), null);
  assert.equal(daemonAttach({}), null); // no attach directive, no origin adoption
});

// withBrowserGlobals stubs the minimal DOM surface enterSharedModeIfNeeded/consumeLiveToken touch
// (storage + history), plus the fuller location they read, for the duration of fn.
function withBrowserGlobals(loc: Record<string, string>, fn: () => void): void {
  const g = globalThis as unknown as Record<string, unknown>;
  const saved: Record<string, unknown> = {
    location: g.location, sessionStorage: g.sessionStorage, localStorage: g.localStorage, history: g.history,
  };
  const store = (): Storage => {
    const m = new Map<string, string>();
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
      clear: () => m.clear(),
      key: () => null,
      length: 0,
    } as unknown as Storage;
  };
  g.location = { pathname: "/console/dashboard/", search: "", hash: "", ...loc };
  g.sessionStorage = store();
  g.localStorage = store();
  g.history = { replaceState: () => {} };
  try {
    fn();
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete g[k];
      else g[k] = v;
    }
  }
}

// Kept LAST: enterSharedModeIfNeeded only ever sets the module shared/own-origin flags to
// true, so once shared mode is entered it stays entered for the rest of this module's tests.
test("shared mode resolves the daemon to the page's own LAN origin (not loopback)", () => {
  withBrowserGlobals({ host: "192.168.1.42:8787", hostname: "192.168.1.42", hash: "#token=abc123" }, () => {
    assert.equal(enterSharedModeIfNeeded(), true); // non-loopback origin + token -> read-only shared view
    assert.equal(isSharedMode(), true);
    // The phone must keep talking to the EXACT LAN IP:port it loaded from - never 127.0.0.1.
    assert.equal(daemonAttach({}), "192.168.1.42:8787");
  });
});
