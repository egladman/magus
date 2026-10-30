import test from "node:test";
import assert from "node:assert/strict";
import { redeemLinkCode } from "./token-exchange";
import { getLiveToken } from "./server";

// withStorage stubs the two Web Storage objects the exchange reads and writes, seeded with
// an existing bearer. Async because the exchange is, unlike the sync harness in
// server.test.ts.
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

// withFetch stubs fetch for one call site, recording what was sent.
async function withFetch(
  respond: (url: string, init?: RequestInit) => Response,
  fn: (calls: { url: string; body: string }[]) => Promise<void>,
): Promise<void> {
  const realFetch = globalThis.fetch;
  const calls: { url: string; body: string }[] = [];
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = init?.body;
    calls.push({
      url: String(input),
      body: typeof raw === "string" ? raw : new TextDecoder().decode(raw as Uint8Array),
    });
    return respond(String(input), init);
  }) as typeof fetch;
  try {
    await fn(calls);
  } finally {
    globalThis.fetch = realFetch;
  }
}

// A server that answered a code with an operator token gets the same refusal.
test("a redeemed operator token is refused, not stored", async () => {
  await withStorage({}, async () => {
    await withFetch(
      () => new Response(JSON.stringify({ token: "mgo_operator" }), { status: 200 }),
      async () => {
        assert.equal(await redeemLinkCode("127.0.0.1:7391", "mgx_code"), "failed");
        assert.equal(getLiveToken(), null);
      },
    );
  });
});

// A link's code goes to the server that served the page, in the body and nowhere else, and the
// token it is traded for is what the page stores.
test("a link code is redeemed once, in the body, for the token it stands for", async () => {
  await withStorage({}, async () => {
    await withFetch(
      () =>
        new Response(JSON.stringify({ token: "mgs_console", expires_at: "2026-09-24T00:00:00Z" }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      async (calls) => {
        const out = await redeemLinkCode("127.0.0.1:7391", "mgx_code");
        assert.equal(out, "exchanged");
        assert.equal(calls.length, 1);
        assert.equal(calls[0]?.url, "http://127.0.0.1:7391/api/v1/token/exchange");
        assert.deepEqual(JSON.parse(calls[0]?.body), { code: "mgx_code" });
        assert.equal(getLiveToken(), "mgs_console");
      },
    );
  });
});

test("a used or expired code, or a server that is down, stores nothing", async () => {
  for (const respond of [
    () => new Response('{"error":{"code":401}}', { status: 401 }),
    () => new Response("not json", { status: 200 }),
    () => new Response('{"token":""}', { status: 200 }),
    (): Response => {
      // platform double: fetch
      throw new TypeError("connection refused");
    },
  ]) {
    await withStorage({}, async () => {
      await withFetch(respond, async () => {
        assert.equal(await redeemLinkCode("127.0.0.1:7391", "mgx_code"), "failed");
        assert.equal(getLiveToken(), null);
      });
    });
  }
});
