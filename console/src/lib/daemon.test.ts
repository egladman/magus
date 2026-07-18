import test from "node:test";
import assert from "node:assert/strict";
import { validateLiveHost } from "./daemon";

// validateLiveHost is the loopback lock. These tests pin the "share to phone"
// relaxation: it additionally accepts the PAGE'S OWN origin (so a phone can talk
// to the daemon it loaded the console from), while still rejecting every other
// non-loopback host - the property that keeps the token from leaking to a third
// party.

// withLocation stubs a page origin for the duration of fn, then restores it.
function withLocation<T>(host: string, fn: () => T): T {
  const g = globalThis as unknown as { location?: { host: string } };
  const prev = g.location;
  g.location = { host };
  try {
    return fn();
  } finally {
    if (prev === undefined) delete g.location;
    else g.location = prev;
  }
}

test("validateLiveHost accepts loopback regardless of page origin", () => {
  withLocation("example.com", () => {
    assert.equal(validateLiveHost("127.0.0.1:7391"), "127.0.0.1:7391");
    assert.equal(validateLiveHost("[::1]:7391"), "[::1]:7391");
  });
});

test("validateLiveHost accepts the page's own non-loopback origin (shared mode)", () => {
  withLocation("192.168.1.20:54321", () => {
    assert.equal(validateLiveHost("192.168.1.20:54321"), "192.168.1.20:54321");
  });
});

test("validateLiveHost rejects a third-party non-loopback host", () => {
  withLocation("192.168.1.20:54321", () => {
    // A different LAN host, a public host, and a hostname are all refused even
    // when the page itself is on the LAN.
    assert.equal(validateLiveHost("192.168.1.99:54321"), null);
    assert.equal(validateLiveHost("evil.example.com:80"), null);
    assert.equal(validateLiveHost("10.0.0.1:8080"), null);
  });
});

test("validateLiveHost still refuses userinfo smuggling", () => {
  withLocation("192.168.1.20:54321", () => {
    // The classic "127.0.0.1:7391@evil.com" trick must not slip through either
    // branch: URL parsing puts evil.com as the host, which is neither loopback
    // nor the page origin.
    assert.equal(validateLiveHost("127.0.0.1:7391@evil.com"), null);
    assert.equal(validateLiveHost("192.168.1.20:54321@evil.com"), null);
  });
});
