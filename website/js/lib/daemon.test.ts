// daemon.test.ts - the loopback lock's regression test. validateLiveHost() is the
// security boundary that makes "your data never leaves your machine" verifiable:
// every live-mode fetch is built from its normalized return, so if it ever accepts
// a non-loopback host the bearer token could be sent off-box. It is a PURE function
// (URL parsing only), so it is testable without a DOM.
//
// Run: `pnpm run test` (esbuild bundles this to node_modules/.test and runs it under
// node --test). The bundle tree-shakes daemon.ts down to validateLiveHost, so the
// ConnectRPC transport code and its browser-only imports never enter the node run.

import { test } from "node:test";
import assert from "node:assert/strict";
import { validateLiveHost } from "./daemon";

test("accepts the two loopback hosts with a port", () => {
  assert.equal(validateLiveHost("127.0.0.1:7391"), "127.0.0.1:7391");
  assert.equal(validateLiveHost("[::1]:7391"), "[::1]:7391");
});

test("rejects non-loopback hosts", () => {
  for (const bad of [
    "evil.com:7391",
    "localhost:7391",          // the hostname is NOT in connect-src; only the literal IPs are
    "0.0.0.0:7391",
    "10.0.0.5:7391",
    "192.168.1.10:7391",
    "example.com",
    "169.254.169.254:80",      // link-local metadata endpoint
  ]) {
    assert.equal(validateLiveHost(bad), null, bad + " must be rejected");
  }
});

test("rejects the userinfo smuggle that a naive split would miss", () => {
  // A last-colon split yields host "127.0.0.1", but a browser fetching this URL
  // actually connects to evil.com and would leak the token there.
  assert.equal(validateLiveHost("127.0.0.1:7391@evil.com"), null);
  assert.equal(validateLiveHost("user:pass@127.0.0.1:7391"), null);
});

test("rejects any path, query, or fragment tacked onto the host", () => {
  assert.equal(validateLiveHost("127.0.0.1:7391/api/v1/events"), null);
  assert.equal(validateLiveHost("127.0.0.1:7391?x=1"), null);
  assert.equal(validateLiveHost("127.0.0.1:7391#frag"), null);
});

test("rejects garbage that does not parse as a host", () => {
  assert.equal(validateLiveHost(""), null);
  assert.equal(validateLiveHost("http://127.0.0.1:7391"), null); // a scheme is not a host:port
  assert.equal(validateLiveHost("not a host"), null);
});
