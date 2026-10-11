import { test } from "node:test";
import assert from "node:assert/strict";
import { fetchContext, publish, reply } from "./session";

// The SAME golden vector internal/diff/session_test.go asserts.
//
// If these two drift the feature silently half-works: a hunk the person marked read in the
// browser still looks unread to an agent reading the same session, and neither side reports
// an error. Two tests over one literal turns that into a build failure.
// The digest tests that were here are gone with the code they covered. hunkDigest and
// patchDigest were TypeScript reimplementations of internal/diff's, kept in step by a golden
// vector pasted from Go - which is the arrangement that let the two readers drift in the first
// place. The server computes both now and ships them, so there is nothing here to pin.

test("context requests carry the reviewed patch identity", async () => {
  const realFetch = globalThis.fetch;
  let url = "";
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    url = String(input);
    return new Response(JSON.stringify({ path: "a.go", as_of: "snapshot-a", start: 2, lines: [] }));
  }) as typeof fetch;
  try {
    await fetchContext("127.0.0.1:7391", "a.go", "snapshot-a", 3, 3, new AbortController().signal);
    const query = new URL(url).searchParams;
    assert.equal(query.get("path"), "a.go");
    assert.equal(query.get("as_of"), "snapshot-a");
  } finally {
    globalThis.fetch = realFetch;
  }
});

async function withResponse<T>(res: Response, body: () => Promise<T>): Promise<T> {
  const real = globalThis.fetch;
  globalThis.fetch = (() => Promise.resolve(res)) as typeof fetch;
  try {
    return await body();
  } finally {
    globalThis.fetch = real;
  }
}

const noPullRequest = JSON.stringify({
  error: { code: 409, message: "MGS9012: no pull request for this branch", status: "ABORTED" },
});

test("publish throws the envelope's message, not the raw JSON", async () => {
  await withResponse(new Response(noPullRequest, { status: 409 }), () =>
    assert.rejects(
      publish("127.0.0.1:7391", "", "comment", new AbortController().signal),
      new Error("MGS9012: no pull request for this branch"),
    ),
  );
});

test("reply throws the envelope's message, and a plain body or none still shows", async () => {
  const signal = new AbortController().signal;
  const reasons: [string, number, string][] = [
    [noPullRequest, 409, "MGS9012: no pull request for this branch"],
    ["no credential\n", 403, "no credential"],
    ["", 502, "server answered 502"],
  ];
  for (const [body, status, message] of reasons) {
    await withResponse(new Response(body, { status }), () =>
      assert.rejects(reply("127.0.0.1:7391", "t1", "hi", signal), new Error(message)),
    );
  }
});

// The host threads a reply by its conversation's first comment. Sending any other comment's id
// would start a second conversation beside the one being answered.
test("reply names the conversation by its root", async () => {
  const real = globalThis.fetch;
  let sent: unknown;
  globalThis.fetch = (async (_url: RequestInfo | URL, init?: RequestInit) => {
    sent = JSON.parse(String(init?.body));
    return new Response("{}");
  }) as typeof fetch;
  try {
    await reply("127.0.0.1:7391", "t1", "one service", new AbortController().signal);
  } finally {
    globalThis.fetch = real;
  }
  assert.deepEqual(sent, { op: "reply", root: "t1", body: "one service" });
});
