// badges.test.ts - the cap, and how a rail count handles a daemon that will not answer. Same shape as
// pulse.test.ts, and for the same reason: the two failure classes have opposite correct responses.

import assert from "node:assert/strict";
import { test, beforeEach } from "node:test";
import {
  BADGE_MAX,
  badgeLabel,
  fetchDiffCount,
  resetRoutelessHosts,
  RoutelessError,
} from "./badges";

beforeEach(() => resetRoutelessHosts());

const HOST = "127.0.0.1:7391";

test("a count renders as itself until the cap, then as 'more'", () => {
  assert.equal(badgeLabel(0), "0");
  assert.equal(badgeLabel(BADGE_MAX), String(BADGE_MAX));
  assert.equal(badgeLabel(BADGE_MAX + 1), BADGE_MAX + "+");
  assert.equal(badgeLabel(4021), BADGE_MAX + "+");
});

test("a count passes straight through", async () => {
  assert.equal(await fetchDiffCount(HOST, async () => 7), 7);
});

// Zero is a real measurement and must survive the round trip - it is the RENDERER that decides an
// empty count is not worth a mark, and it cannot decide that if the fetch flattens 0 to null.
test("zero is a reading, not an absence", async () => {
  assert.equal(await fetchDiffCount(HOST, async () => 0), 0);
});

// A daemon without the diff route cannot grow one while it runs, so asking again every 15s for the
// life of the page is the console-spam lib/daemon.ts's readiness probe goes out of its way to avoid.
test("a daemon with no diff route is not asked twice", async () => {
  let calls = 0;
  const routeless = async (): Promise<never> => {
    calls++;
    throw new RoutelessError("404");
  };
  assert.equal(await fetchDiffCount(HOST, routeless), null);
  assert.equal(await fetchDiffCount(HOST, routeless), null);
  assert.equal(calls, 1, "the second poll should never have left the browser");
});

// The opposite case, which must NOT latch: a daemon coming back is normal, and one dropped request
// would otherwise blank the badge for the rest of the session.
test("an outage keeps being retried, and recovers", async () => {
  let calls = 0;
  const down = async (): Promise<never> => {
    calls++;
    throw new Error("500");
  };
  await fetchDiffCount(HOST, down);
  await fetchDiffCount(HOST, down);
  assert.equal(calls, 2);
  assert.equal(await fetchDiffCount(HOST, async () => 4), 4);
});

test("the latch does not spread to another daemon", async () => {
  await fetchDiffCount(HOST, async () => {
    throw new RoutelessError("404");
  });
  assert.equal(await fetchDiffCount("127.0.0.1:9999", async () => 2), 2);
});

test("no host is no request", async () => {
  let calls = 0;
  await fetchDiffCount("", async () => {
    calls++;
    return 1;
  });
  assert.equal(calls, 0);
});
