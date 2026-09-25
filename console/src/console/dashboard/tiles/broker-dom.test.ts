import assert from "node:assert/strict";
import { test } from "node:test";
import { create } from "@bufbuild/protobuf";
import { StatusSchema } from "@wire/status/v1alpha1/status_pb";
import { initialState, mapStatus } from "../state";
import { brokerTile } from "./broker";

function status(withLine: boolean) {
  return mapStatus(
    create(StatusSchema, {
      brokerPolicy: "required",
      broker: {
        pid: 48213,
        order: withLine ? "fifo-backfill" : "",
        backfillLimit: withLine ? 4 : 0,
        capacity: {
          budgetSlots: 10,
          heldSlots: 10,
          holders: [{ project: "api", target: "test", pid: 48190, slots: 10 }],
        },
        waiting: withLine
          ? [
              {
                position: 1,
                claim: {
                  project: "web",
                  target: "build",
                  pid: 48412,
                  slots: 2,
                  memoryMb: 8 * 1024,
                },
                blockedBy: [{ pid: 48190 }, { pid: 48377 }],
              },
              {
                position: 2,
                claim: { project: ".", target: "lint", pid: 48500 },
                ownRun: true,
                ahead: [{ pid: 48412 }],
              },
              { position: 3, claim: { project: "web", target: "test", pid: 48500 }, ownRun: true },
            ]
          : [],
      },
    }),
  );
}

function waitRows(tile: { el: HTMLElement }): HTMLElement[] {
  return [...tile.el.querySelectorAll<HTMLElement>("[aria-label='waiting for capacity'] > li")];
}

function fact(tile: { el: HTMLElement }, label: string): string | undefined {
  for (const row of tile.el.querySelectorAll(".console-dashboard-row")) {
    if (row.querySelector(".console-dashboard-row__cmd")?.textContent === label) {
      return row.querySelector(".console-dashboard-row__meta")?.textContent ?? "";
    }
  }
  return undefined;
}

test("lists each waiter with its place and what keeps it out, as magus status does", () => {
  const tile = brokerTile();
  try {
    tile.update({ ...initialState(), status: status(true) });
    assert.equal(
      tile.el.querySelector(".pf-v6-c-label__content")?.textContent,
      "running, 3 waiting",
    );
    assert.equal(fact(tile, "line"), "fifo-backfill, passed over at most 4 times");
    const rows = waitRows(tile);
    assert.deepEqual(
      rows.map((r) => r.textContent),
      [
        "web:buildwaiting, place 1 - 2 slots - 8.0 GB - pid 48412 - blocked by pid 48190, 48377",
        ".:lintwaiting, place 2 - 1 slot - pid 48500 - behind pid 48412",
        "web:testwaiting, place 3 - 1 slot - pid 48500 - blocked by its own run",
      ],
    );
    assert.deepEqual(
      rows.map((r) => r.dataset.blocked),
      ["others", "ahead", "own-run"],
    );
  } finally {
    tile.destroy();
  }
});

test("a broker with no line shows no line fact and no waiters", () => {
  const tile = brokerTile();
  try {
    tile.update({ ...initialState(), status: status(false) });
    assert.equal(tile.el.querySelector(".pf-v6-c-label__content")?.textContent, "running");
    assert.equal(fact(tile, "line"), undefined, "a broker that predates the line names no order");
    assert.equal(waitRows(tile).length, 0);
  } finally {
    tile.destroy();
  }
});
