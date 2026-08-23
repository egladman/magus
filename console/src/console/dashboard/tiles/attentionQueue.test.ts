// attentionQueue.test.ts - the queue's parser and its two row helpers.
//
// Pinned by test rather than by opening the board because the queue is EMPTY on a healthy repo,
// which is most of the time: "open it and look" verifies nothing on any particular day, and the
// rows only appear when somebody is already blocked and least wants to find a broken tile.
//
// The parser especially. It reads the network, and every field it lets through untyped becomes a
// row rendering "undefined" or an age computed from a string - on the one surface whose whole job
// is to say, accurately, that a person is being waited on.

import assert from "node:assert/strict";
import { test } from "node:test";
import { ageLabel, firstLine, parseRequests, parseStore } from "./attentionQueue";

test("parseRequests reads the documented shape", () => {
  const rows = parseRequests({
    requests: [
      {
        id: "att-0123456789ab",
        session: "agent-1",
        opened_ms: 1755300000000,
        outcome: "permission",
        severity: "high",
        source: "claude/Notification",
        where: "/repo [apps/web]",
        delegation: "unit-a",
        message: "may I push?",
      },
    ],
    store: "/state/magus/sessions/magus-abc123def456",
  });
  assert.equal(rows.length, 1);
  assert.deepEqual(rows[0], {
    id: "att-0123456789ab",
    session: "agent-1",
    opened_ms: 1755300000000,
    outcome: "permission",
    severity: "high",
    source: "claude/Notification",
    where: "/repo [apps/web]",
    delegation: "unit-a",
    message: "may I push?",
  });
  assert.equal(parseStore({ store: "/s" }), "/s");
});

// A row with no id cannot be closed: the dispose control would have nothing to name. Dropped
// rather than drawn as a request that refuses every attempt to answer it.
test("parseRequests drops a row with no usable id", () => {
  assert.equal(parseRequests({ requests: [{ message: "orphan" }, { id: "" }] }).length, 0);
});

// Total over anything, because this is parsed from the network. A tile that cannot read the
// queue says so; it does not take the board down with it.
test("parseRequests is total over a body that is not the documented shape", () => {
  assert.deepEqual(parseRequests(null), []);
  assert.deepEqual(parseRequests({}), []);
  assert.deepEqual(parseRequests({ requests: "nope" }), []);
  assert.deepEqual(parseRequests({ requests: [null, 7, "x"] }), []);
  assert.equal(parseStore(null), "");
});

// Coerced as a string, opened_ms reads as 0 and the row loses the single most useful thing about
// a queue - how long somebody has been waiting.
test("parseRequests keeps opened_ms a number and never coerces one", () => {
  const [row] = parseRequests({ requests: [{ id: "att-a", opened_ms: "1755300000000" }] });
  assert.equal(row.opened_ms, 0, "a string stamp is no stamp, not a stamp of zero characters");
  const [ok] = parseRequests({ requests: [{ id: "att-a", opened_ms: 5 }] });
  assert.equal(ok.opened_ms, 5);
});

test("ageLabel climbs from seconds to days", () => {
  const now = 1_000_000_000_000;
  assert.equal(ageLabel(now - 30_000, now), "30s");
  assert.equal(ageLabel(now - 5 * 60_000, now), "5m");
  assert.equal(ageLabel(now - 3 * 3_600_000, now), "3h");
  assert.equal(ageLabel(now - 2 * 86_400_000, now), "2d");
});

// An unstamped row is a fact about a daemon that sent no timestamp. Rendering "0s" would be a
// confident claim about a time nobody recorded.
test("ageLabel says nothing about a row carrying no timestamp", () => {
  assert.equal(ageLabel(0, 1_000), "");
  assert.equal(ageLabel(Number.NaN, 1_000), "");
});

// Two machines' clocks disagree, and "-4s" reads as a bug in the queue rather than as a skewed
// clock. Clamped so the row degrades to "just now" instead.
test("ageLabel clamps a stamp from the future rather than going negative", () => {
  assert.equal(ageLabel(2_000, 1_000), "0s");
});

// One row per request is what makes the queue scannable, and agent text is routinely a summary
// line followed by a transcript. The summary is what a person triages on.
test("firstLine takes the first non-empty line and collapses its whitespace", () => {
  assert.equal(
    firstLine("\n\n  needs   the deploy key \nstack trace follows"),
    "needs the deploy key",
  );
  assert.equal(firstLine("single line"), "single line");
  assert.equal(firstLine(""), "");
  assert.equal(firstLine("\n \n"), "");
});

// The store admits 4 KiB, because a producer is typically a hook forwarding an agent's text. One
// row that long would push every other request off the tile.
test("firstLine bounds a very long line", () => {
  const out = firstLine("x".repeat(500));
  assert.ok(out.length <= 140, "got " + out.length);
  assert.ok(out.endsWith("..."), "a cut line has to say it was cut");
});
