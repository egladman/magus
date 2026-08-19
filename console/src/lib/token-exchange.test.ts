import test from "node:test";
import assert from "node:assert/strict";
import { Code, ConnectError } from "@connectrpc/connect";
import { ensureScopedToken } from "./token-exchange";
import { getLiveToken, hasScopedToken } from "./daemon";

// withStorage stubs the two Web Storage objects the exchange reads and writes, seeded with
// an existing bearer. Async because the exchange is, unlike the sync harness in
// daemon.test.ts.
async function withStorage(seed: Record<string, string>, fn: () => Promise<void>): Promise<void> {
  const g = globalThis as unknown as Record<string, unknown>;
  const saved = { sessionStorage: g.sessionStorage, localStorage: g.localStorage };
  const store = (init: Record<string, string> = {}): Storage => {
    const m = new Map<string, string>(Object.entries(init));
    return {
      getItem: (k: string) => m.get(k) ?? null,
      setItem: (k: string, v: string) => void m.set(k, v),
      removeItem: (k: string) => void m.delete(k),
      clear: () => m.clear(),
      key: () => null,
      length: 0,
    } as unknown as Storage;
  };
  g.sessionStorage = store(seed);
  g.localStorage = store();
  try {
    await fn();
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete g[k];
      else g[k] = v;
    }
  }
}

test("a page holding no token has nothing to exchange", async () => {
  await withStorage({}, async () => {
    let called = false;
    const out = await ensureScopedToken(async () => {
      called = true;
      return "mgs_new";
    });
    assert.equal(out, "no-token");
    assert.equal(called, false, "an absent token must not reach the daemon");
  });
});

test("the exchange runs once, not on every load", async () => {
  await withStorage({ "magus-live-token": "mgs_console", "magus-live-scoped": "1" }, async () => {
    let called = false;
    const out = await ensureScopedToken(async () => {
      called = true;
      return "mgs_another";
    });
    assert.equal(out, "already-scoped");
    assert.equal(called, false, "a marked page must not mint a second token");
    assert.equal(getLiveToken(), "mgs_console", "the stored token is left alone");
  });
});

test("a successful mint replaces the operator token and marks the page", async () => {
  await withStorage({ "magus-live-token": "mgs_operator" }, async () => {
    const out = await ensureScopedToken(async () => "mgs_console");
    assert.equal(out, "exchanged");
    assert.equal(getLiveToken(), "mgs_console", "the page now holds the scoped token");
    assert.equal(hasScopedToken(), true);
  });
});

// The operator-only mount refuses a page that already holds a console token. That is the
// steady state, not a fault: mark it so the next load stops asking.
test("a refusal is recorded rather than retried, and keeps the token", async () => {
  await withStorage({ "magus-live-token": "mgs_console" }, async () => {
    const out = await ensureScopedToken(async () => {
      throw new ConnectError("token management not offered", Code.PermissionDenied);
    });
    assert.equal(out, "denied");
    assert.equal(getLiveToken(), "mgs_console", "a refusal must not disturb the credential");
    assert.equal(hasScopedToken(), true, "a refused page must stop asking");
  });
});

// The safety property. A daemon restarting mid-exchange must leave the page holding the
// credential it already had, and unmarked, so the swap is retried rather than lost. A bug
// here strands the console with no working token and no way back except a fresh paste.
test("a transport failure leaves the page exactly as it was", async () => {
  await withStorage({ "magus-live-token": "mgs_operator" }, async () => {
    const out = await ensureScopedToken(async () => {
      throw new ConnectError("connection refused", Code.Unavailable);
    });
    assert.equal(out, "failed");
    assert.equal(getLiveToken(), "mgs_operator", "the working credential must survive");
    assert.equal(hasScopedToken(), false, "an unmarked page retries on the next load");
  });
});

test("an empty secret is treated as a failure, not stored", async () => {
  await withStorage({ "magus-live-token": "mgs_operator" }, async () => {
    const out = await ensureScopedToken(async () => "");
    assert.equal(out, "failed");
    assert.equal(getLiveToken(), "mgs_operator");
    assert.equal(hasScopedToken(), false);
  });
});
