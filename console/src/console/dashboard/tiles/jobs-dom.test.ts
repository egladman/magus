import assert from "node:assert/strict";
import { test } from "node:test";
import { jobsTile } from "./jobs";
import { initialState } from "../state";

const JSON_HEADERS = new Headers({ "content-type": "application/json" });

// The listing as the daemon serializes it: protobuf JSON over Connect, so an enum is its name and
// an int64 is a string.
function listing(jobs: unknown[]): Response {
  return {
    ok: true,
    status: 200,
    headers: JSON_HEADERS,
    json: async () => ({ jobs, overlaps: [] }),
  } as unknown as Response;
}

function summary(tile: { el: HTMLElement }): string {
  return tile.el.querySelector(".console-dashboard-jobs__summary")?.textContent ?? "";
}

function ids(tile: { el: HTMLElement }): (string | null)[] {
  return [...tile.el.querySelectorAll(".console-dashboard-jobs__id")].map((el) => el.textContent);
}

test("summarizes the jobs on the dashboard, saying who holds each one", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    listing([
      {
        name: "jobs/clear-cache",
        id: "clear-cache",
        holder: "JOB_HOLDER_DAEMON",
        state: "running",
      },
      { name: "jobs/diff", id: "diff", holder: "JOB_HOLDER_SESSION", state: "declared" },
      { name: "jobs/docs", id: "docs", holder: "JOB_HOLDER_SESSION", state: "pass" },
    ]);

  const tile = jobsTile();
  try {
    tile.update({ ...initialState(), liveHost: "127.0.0.1:7391" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(summary(tile), "3 jobs. 1 declared, 1 running, 1 pass. 0 no-return.");
    assert.equal(
      tile.el.querySelector(".console-dashboard-jobs__summary")?.getAttribute("aria-live"),
      "polite",
    );
    assert.deepEqual(ids(tile), ["clear-cache", "diff"]);
    // The holder rides beside the state: a daemon job and a session's job read the same at a
    // glance otherwise, and on the board that is where they are most easily confused.
    assert.equal(
      tile.el.querySelector(".console-dashboard-jobs__state")?.textContent,
      "daemon, running",
    );
  } finally {
    tile.destroy();
    globalThis.fetch = originalFetch;
  }
});

test("keeps intervention work visible in the shared demo", () => {
  const tile = jobsTile();
  try {
    tile.update({ ...initialState(), conn: { state: "demo" } });
    assert.deepEqual(ids(tile), [
      "dashboard-client",
      "verify-tests",
      "clear-cache",
      "claims-audience",
    ]);
  } finally {
    tile.destroy();
  }
});

test("ignores a completed request from the previous daemon", async () => {
  const originalFetch = globalThis.fetch;
  const replies: ((value: Response) => void)[] = [];
  globalThis.fetch = () => new Promise<Response>((resolve) => replies.push(resolve));

  const tile = jobsTile();
  try {
    tile.update({ ...initialState(), liveHost: "127.0.0.1:7391" });
    tile.update({ ...initialState(), liveHost: "127.0.0.1:7392" });
    assert.equal(replies.length, 2);

    replies[0]?.(
      listing([{ name: "jobs/old", id: "old", holder: "JOB_HOLDER_SESSION", state: "running" }]),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(summary(tile), "No jobs.");

    replies[1]?.(
      listing([{ name: "jobs/new", id: "new", holder: "JOB_HOLDER_SESSION", state: "running" }]),
    );
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.match(summary(tile), /^1 job\./);
  } finally {
    tile.destroy();
    globalThis.fetch = originalFetch;
  }
});

// A daemon that declines the job service is not a workspace with no work in it, and the tile has to
// say which it met.
test("a refused job service says so rather than reading as an idle workspace", async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = async () =>
    ({
      ok: false,
      status: 403,
      headers: JSON_HEADERS,
      json: async () => ({ code: "permission_denied", message: "job control is off" }),
    }) as unknown as Response;

  const tile = jobsTile();
  try {
    tile.update({ ...initialState(), liveHost: "127.0.0.1:7391" });
    await new Promise((resolve) => setTimeout(resolve, 0));
    assert.equal(summary(tile), "This daemon does not serve jobs.");
    assert.deepEqual(ids(tile), []);
  } finally {
    tile.destroy();
    globalThis.fetch = originalFetch;
  }
});
