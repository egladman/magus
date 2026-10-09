// locks-dom.test.ts - the workspace-locks tile. What is pinned is that nothing on it lives only in a
// tooltip, a colour or a scrolling marquee: a stale holder says so in words, the holder's command
// and directory are text on the row, and a card with no locks keeps its place and says why.

import assert from "node:assert/strict";
import { test } from "node:test";
import { initialState, type LockView, type StatusView } from "../state";
import { locksTile } from "./locks";

const ago = (s: number) =>
  ({
    seconds: BigInt(Math.floor(Date.now() / 1000) - s),
    nanos: 0,
  }) as unknown as LockView["acquireTime"];

function frame(locks: LockView[]) {
  return { ...initialState(), status: { locks } as unknown as StatusView };
}

test("a board with no locks keeps the card and says so", () => {
  const tile = locksTile();
  tile.update(frame([]));
  assert.equal(tile.el.hidden, false, "the card does not vanish and reshape the board");
  assert.equal(tile.el.hasAttribute("data-empty"), true);
  assert.match(
    tile.el.querySelector("[data-empty-state]")?.textContent ?? "",
    /No workspace locks/,
  );
  assert.equal(tile.el.querySelector(".pf-v6-c-badge")?.textContent, "0 locks held");
  tile.destroy();
});

test("a stale holder is marked in words and a shape, not by its border alone", () => {
  const tile = locksTile();
  tile.update(
    frame([
      {
        project: "services/ledger",
        pid: 41,
        command: "magus run test services/ledger",
        dir: "/Users/eli/Repos/acme/.worktrees/deleted-branch",
        acquireTime: ago(3 * 3600),
        staleAfterSeconds: 600,
      },
      {
        project: ".",
        pid: 42,
        command: "magus run lint",
        dir: "/Users/eli/Repos/acme",
        acquireTime: ago(5),
        staleAfterSeconds: 600,
      },
    ]),
  );
  const rows = [...tile.el.querySelectorAll<HTMLElement>(".console-dashboard-row")];
  assert.equal(rows.length, 2);
  assert.equal(rows[0].dataset.stale, "true");
  const warn = rows[0].querySelector(".console-dashboard-row__warn");
  assert.match(warn?.textContent ?? "", /may be abandoned/);
  assert.ok(warn?.querySelector(".pf-v6-c-icon"), "a shape beside the sentence");
  assert.equal(rows[1].dataset.stale, undefined);
  assert.equal(rows[1].querySelector(".console-dashboard-row__warn"), null);
  tile.destroy();
});

test("the holder's command and directory are text, and nothing is a tooltip or a marquee", () => {
  const tile = locksTile();
  tile.update(
    frame([
      {
        project: "services/ledger",
        pid: 41,
        command: "magus run test services/ledger",
        dir: "/Users/eli/Repos/acme/.worktrees/a-very-long-branch-name",
        acquireTime: ago(5),
        staleAfterSeconds: 600,
      },
    ]),
  );
  const row = tile.el.querySelector(".console-dashboard-row");
  assert.equal(
    row?.querySelector(".console-dashboard-row__detail")?.textContent,
    "magus run test services/ledger",
  );
  assert.match(
    row?.querySelector(".console-dashboard-row__meta")?.textContent ?? "",
    /\/Users\/eli\/Repos\/acme\/\.worktrees\/a-very-long-branch-name/,
    "the whole path is on the row, wrapping rather than scrolling",
  );
  assert.equal(row?.querySelector("[title]"), null);
  assert.equal(tile.el.querySelector("[data-marquee]"), null);
  tile.destroy();
});
