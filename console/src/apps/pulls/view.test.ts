import assert from "node:assert/strict";
import { test } from "node:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import {
  AuthorFilterSchema,
  OverlapSchema,
  PullSchema,
  SyncStateSchema,
} from "@wire/pulls/v1alpha1/pulls_pb";
import { ageText, authorsLine, blockedText, clockText, overlapText, syncLine } from "./view";

const NOW = 1_700_000_000_000;

test("ageText rounds down to the largest whole unit", () => {
  assert.equal(ageText(12_000), "12s");
  assert.equal(ageText(125_000), "2m");
  assert.equal(ageText(2 * 3_600_000 + 5), "2h");
  assert.equal(ageText(3 * 86_400_000), "3d");
  assert.equal(ageText(-5), "0s");
});

test("syncLine reads age, countdown, reason and rate", () => {
  const sync = create(SyncStateSchema, {
    syncedAt: timestampFromMs(NOW - 12_000),
    nextSyncAt: timestampFromMs(NOW + 48_000),
    cadenceReason: "checks running: every 1m",
    rateLimit: 5000,
    rateRemaining: 4210,
  });
  assert.equal(
    syncLine(sync, NOW),
    "synced 12s ago · next in 48s · checks running: every 1m · 4210/5000 left",
  );
});

test("syncLine on failure names the error and the board's time", () => {
  const syncedAt = NOW - 120_000;
  const sync = create(SyncStateSchema, {
    syncedAt: timestampFromMs(syncedAt),
    failedAt: timestampFromMs(NOW - 120_000),
    syncError: "rate limited",
  });
  assert.equal(
    syncLine(sync, NOW),
    `sync failed 2m ago: rate limited; showing the board from ${clockText(syncedAt)}`,
  );
});

test("syncLine says so when nothing has synced", () => {
  assert.equal(syncLine(undefined, NOW), "not synced yet");
});

test("authorsLine names the default, a chosen list, and every author", () => {
  const mk = (d: object) => create(AuthorFilterSchema, d);
  assert.equal(
    authorsLine(mk({ authors: ["egladman"], isDefault: true })),
    "authors: egladman (you, default)",
  );
  assert.equal(authorsLine(mk({ authors: ["a", "b"] })), "authors: a, b");
  assert.equal(authorsLine(mk({ everyAuthor: true })), "every author");
});

test("blockedText joins the reasons as sentences", () => {
  const p = create(PullSchema, { blocked: ["conflicts with main", "draft"] });
  assert.equal(blockedText(p), "conflicts with main; draft");
});

test("overlapText counts files", () => {
  const p = create(PullSchema, {
    overlaps: [
      create(OverlapSchema, { number: 640, files: ["a", "b", "c"] }),
      create(OverlapSchema, { number: 641, files: ["a"] }),
    ],
  });
  assert.deepEqual(overlapText(p), ["shares 3 files with #640", "shares 1 file with #641"]);
});
