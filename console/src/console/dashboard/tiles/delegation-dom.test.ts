import assert from "node:assert/strict";
import { test } from "node:test";
import { delegationTile } from "./delegation";
import { initialState } from "../state";

function response(units: unknown[]): Response {
  return {
    ok: true,
    status: 200,
    json: async () => ({ units, overlaps: [] }),
  } as Response;
}

test("summarizes the active ledger on the dashboard", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    ({
      ok: true,
      status: 200,
      json: async () => ({
        units: [
          { id: "dashboard", state: "running" },
          { id: "diff", parent: "dashboard", state: "declared" },
          { id: "docs", parent: "dashboard", state: "pass" },
        ],
        overlaps: [],
      }),
    }) as Response;

  const tile = delegationTile();
  try {
    tile.update({ ...initialState(), liveHost: "daemon.test" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(
      tile.el.querySelector(".console-dashboard-delegation__summary")?.textContent,
      "3 units. 1 declared, 1 running, 1 pass. 0 no-return.",
    );
    assert.equal(
      tile.el.querySelector(".console-dashboard-delegation__summary")?.getAttribute("aria-live"),
      "polite",
    );
    assert.deepEqual(
      [...tile.el.querySelectorAll(".console-dashboard-delegation__id")].map(
        (el) => el.textContent,
      ),
      ["dashboard", "diff"],
    );
  } finally {
    tile.destroy();
    globalThis.fetch = originalFetch;
  }
});

test("keeps intervention work visible in the shared demo", () => {
  const tile = delegationTile();
  try {
    tile.update({ ...initialState(), conn: { state: "demo" } });
    assert.deepEqual(
      [...tile.el.querySelectorAll(".console-dashboard-delegation__id")].map(
        (el) => el.textContent,
      ),
      ["dashboard-client", "verify-tests", "claims-audience", "docs-rename"],
    );
  } finally {
    tile.destroy();
  }
});

test("ignores a completed request from the previous daemon", async () => {
  const originalFetch = globalThis.fetch;
  const replies: ((value: Response) => void)[] = [];
  globalThis.fetch = () => new Promise<Response>((resolve) => replies.push(resolve));

  const tile = delegationTile();
  try {
    tile.update({ ...initialState(), liveHost: "old.test" });
    tile.update({ ...initialState(), liveHost: "new.test" });
    assert.equal(replies.length, 2);

    replies[0]?.(response([{ id: "old", state: "running" }]));
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(
      tile.el.querySelector(".console-dashboard-delegation__summary")?.textContent,
      "No active delegation.",
    );

    replies[1]?.(response([{ id: "new", state: "running" }]));
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.match(
      tile.el.querySelector(".console-dashboard-delegation__summary")?.textContent ?? "",
      /^1 unit\./,
    );
  } finally {
    tile.destroy();
    globalThis.fetch = originalFetch;
  }
});
