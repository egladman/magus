// pulse.test.ts - how the rail's pool read handles a daemon that will not answer. The RPC itself is
// injected; what is under test is the classification, because the two failure classes have opposite
// correct responses and telling them apart is the whole reason this file has any logic in it.

import assert from "node:assert/strict";
import { test, beforeEach } from "node:test";
import { Code, ConnectError } from "@connectrpc/connect";
import { fetchPulse, resetDeniedHosts } from "./pulse";

beforeEach(() => resetDeniedHosts());

const HOST = "127.0.0.1:7391";

test("a pool reading passes straight through", async () => {
  const got = await fetchPulse(HOST, async () => ({ running: 2, queued: 1, workspaces: [] }));
  assert.deepEqual(got, { running: 2, queued: 1, workspaces: [] });
});

// Verified against a real v0.2.0 daemon, which 404s the route entirely: GetStatus is newer than the
// shipped releases. Retrying a route that cannot appear, every 15s for the life of the page, is the
// console-spam lib/daemon.ts's readiness probe goes out of its way to avoid.
test("a daemon that does not serve the route is not asked twice", async () => {
  let calls = 0;
  const denied = async (): Promise<never> => {
    calls++;
    throw new ConnectError("no such method", Code.Unimplemented);
  };
  assert.equal(await fetchPulse(HOST, denied), null);
  assert.equal(await fetchPulse(HOST, denied), null);
  assert.equal(calls, 1, "the second poll should never have left the browser");
});

// The opposite case, and the one that must NOT latch: a daemon coming back is normal, and latching on
// a blip would leave the rail blank for the rest of the session over one dropped request.
test("an outage keeps being retried", async () => {
  let calls = 0;
  const down = async (): Promise<never> => {
    calls++;
    throw new ConnectError("connection refused", Code.Unavailable);
  };
  await fetchPulse(HOST, down);
  await fetchPulse(HOST, down);
  assert.equal(calls, 2);

  // And it recovers: the earlier failures left nothing behind that blocks a later good answer.
  assert.deepEqual(
    await fetchPulse(HOST, async () => ({ running: 1, queued: 0, workspaces: [] })),
    {
      running: 1,
      queued: 0,
      workspaces: [],
    },
  );
});

// A non-ConnectError (a raw TypeError from a blocked cross-origin fetch, which is exactly what a
// browser hands back) is an outage, not a denial - the browser blocked it before any status came back,
// so it says nothing about what the daemon serves.
test("a blocked fetch is an outage, not a denial", async () => {
  let calls = 0;
  const blocked = async (): Promise<never> => {
    calls++;
    throw new TypeError("Failed to fetch");
  };
  await fetchPulse(HOST, blocked);
  await fetchPulse(HOST, blocked);
  assert.equal(calls, 2);
});

// The latch is per-host so that pointing the console at a different daemon probes it properly rather
// than inheriting the verdict on the one before it.
test("the latch does not spread to another daemon", async () => {
  await fetchPulse(HOST, async () => {
    throw new ConnectError("no such method", Code.Unimplemented);
  });
  const other = await fetchPulse("127.0.0.1:9999", async () => ({
    running: 4,
    queued: 0,
    workspaces: [],
  }));
  assert.deepEqual(other, { running: 4, queued: 0, workspaces: [] });
});

test("no host is no request", async () => {
  let calls = 0;
  await fetchPulse("", async () => {
    calls++;
    return { running: 1, queued: 0, workspaces: [] };
  });
  assert.equal(calls, 0);
});
