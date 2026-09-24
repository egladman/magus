import test from "node:test";
import assert from "node:assert/strict";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  CONSOLE_TOKEN_TTL_MS,
  ensureConsoleToken,
  exchangeOperatorToken,
  redeemLinkCode,
} from "./token-exchange";
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

test("a page holding no token has nothing to exchange", async () => {
  await withStorage({}, async () => {
    let called = false;
    const out = await ensureConsoleToken(async () => {
      called = true;
      return "mgs_new";
    });
    assert.equal(out, "no-token");
    assert.equal(called, false, "an absent token must not reach the server");
  });
});

test("a successful mint replaces the operator token", async () => {
  await withStorage({ "magus-live-token": "mgo_operator" }, async () => {
    const out = await ensureConsoleToken(async () => "mgs_console");
    assert.equal(out, "exchanged");
    assert.equal(getLiveToken(), "mgs_console", "the page now holds the console token");
  });
});

// The prefix decides, with no stored marker: a stored or share token is never sent to the
// mint, on this load or any later one, so a console token cannot even ask for a second one.
test("a token that is not the operator's is never exchanged", async () => {
  for (const held of ["mgs_console", "mgl_share"]) {
    await withStorage({ "magus-live-token": held }, async () => {
      let called = false;
      const out = await ensureConsoleToken(async () => {
        called = true;
        return "mgs_another";
      });
      assert.equal(out, "already-scoped", held);
      assert.equal(called, false, "a scoped token must not reach the mint: " + held);
      assert.equal(getLiveToken(), held);
    });
  }
});

// A refusal is a failure like any other: the page still holds the operator token, so the
// caller says so, and the next load asks again rather than settling for it.
test("a refused exchange fails loudly and keeps asking", async () => {
  await withStorage({ "magus-live-token": "mgo_operator" }, async () => {
    for (const code of [Code.PermissionDenied, Code.Unimplemented, Code.Unavailable]) {
      let called = 0;
      const out = await ensureConsoleToken(async () => {
        called++;
        throw new ConnectError("refused", code);
      });
      assert.equal(out, "failed", Code[code]);
      assert.equal(called, 1);
      assert.equal(getLiveToken(), "mgo_operator", "a refusal must not disturb the credential");
    }
  });
});

test("an empty secret is treated as a failure, not stored", async () => {
  await withStorage({ "magus-live-token": "mgo_operator" }, async () => {
    const out = await ensureConsoleToken(async () => "");
    assert.equal(out, "failed");
    assert.equal(getLiveToken(), "mgo_operator");
  });
});

// Pinned on the wire: the request carries a console=write grant and an expiry at the server's
// ceiling, and the minted secret replaces the operator token.
test("the real exchange asks for an expiring console=write grant", async () => {
  await withStorage({ "magus-live-token": "mgo_operator" }, async () => {
    await withFetch(
      () =>
        new Response(JSON.stringify({ secret: "mgs_console" }), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      async (calls) => {
        const now = Date.UTC(2026, 8, 22);
        const out = await exchangeOperatorToken("127.0.0.1:7391", now);
        assert.equal(out, "exchanged");
        const body = JSON.parse(calls[0].body);
        assert.deepEqual(body.grant, { console: "LEVEL_WRITE" });
        assert.equal(body.scope, undefined, "the retired scope field is gone");
        assert.equal(Date.parse(String(body.expireTime)), now + CONSOLE_TOKEN_TTL_MS);
        assert.equal(getLiveToken(), "mgs_console");
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
        assert.equal(calls[0].url, "http://127.0.0.1:7391/api/v1/token/exchange");
        assert.deepEqual(JSON.parse(calls[0].body), { code: "mgx_code" });
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
