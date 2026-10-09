// reading-dom.test.ts - the reading mark's wire call, and the toast a refusal raises. A toast is a
// DOM event, so this lives with the other *-dom tests.

import assert from "node:assert/strict";
import { test, afterEach } from "node:test";
import { NOTIFY_EVENT } from "../../lib/notifications";
import { setReading } from "./session";

const realFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = realFetch;
});

// withToasts collects the toasts a call raised, through the event the shell listens on.
async function withToasts<T>(body: () => Promise<T>): Promise<{ result: T; toasts: string[] }> {
  const toasts: string[] = [];
  const listen = (e: Event): void => {
    toasts.push(String((e as CustomEvent<{ message: string }>).detail.message));
  };
  document.addEventListener(NOTIFY_EVENT, listen);
  try {
    return { result: await body(), toasts };
  } finally {
    document.removeEventListener(NOTIFY_EVENT, listen);
  }
}

function answer(res: Response): void {
  globalThis.fetch = (() => Promise.resolve(res)) as typeof fetch;
}

test("setReading sends the op and returns the mark, the command included", async () => {
  let sent: unknown;
  globalThis.fetch = (async (_url: RequestInfo | URL, init?: RequestInit) => {
    sent = JSON.parse(String(init?.body));
    return new Response(
      JSON.stringify({ reading: true, since: 1700000000000, command: "gh pr comment 7" }),
    );
  }) as typeof fetch;

  const { result, toasts } = await withToasts(() =>
    setReading("127.0.0.1:7391", true, new AbortController().signal),
  );

  assert.deepEqual(sent, { op: "reading", on: true });
  assert.deepEqual(result, { reading: true, since: 1700000000000, command: "gh pr comment 7" });
  assert.deepEqual(toasts, [], "a mark that took raises nothing");
});

// 502 (no review open) and 409 (already merged) both leave the mark as it was, and both say why.
test("setReading toasts the server's reason for a 502 and a 409, and returns null", async () => {
  const refusals: [number, string][] = [
    [502, "reading: no pull request for this branch"],
    [409, "review 7 has already merged, so there is nothing left to hold"],
  ];
  for (const [status, message] of refusals) {
    answer(
      new Response(JSON.stringify({ error: { code: status, message, status: "X" } }), { status }),
    );
    const { result, toasts } = await withToasts(() =>
      setReading("127.0.0.1:7391", true, new AbortController().signal),
    );
    assert.equal(result, null);
    assert.deepEqual(toasts, [message]);
  }
});

test("setReading says the status when a refusal carries no words", async () => {
  answer(new Response("", { status: 502 }));
  const { result, toasts } = await withToasts(() =>
    setReading("127.0.0.1:7391", false, new AbortController().signal),
  );
  assert.equal(result, null);
  assert.equal(toasts.length, 1);
  assert.match(toasts[0] ?? "", /HTTP 502/);
});

test("setReading toasts a server that cannot be reached", async () => {
  globalThis.fetch = (() => {
    throw new TypeError("fetch failed");
  }) as typeof fetch;
  const { result, toasts } = await withToasts(() =>
    setReading("127.0.0.1:7391", true, new AbortController().signal),
  );
  assert.equal(result, null);
  assert.equal(toasts.length, 1);
});
