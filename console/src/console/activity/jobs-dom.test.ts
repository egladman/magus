// jobs-dom.test.ts - the Activity surface's maintenance control (jobs.ts). document/window are
// registered globally by test-setup.mjs (node --import), so this runs under node:test like the other
// *-dom tests, and it drives the control through activate() rather than mountJobs directly: what is
// worth pinning is that the surface MOUNTS it, not that a function returns elements.
//
// The load is stubbed at fetch, so every assertion below is over the transport the surface really
// uses. The one that carries the feature is "the Run action submits the job on the row": it reads
// the name off the RunJob request body, so disconnecting the button from the client - a listener
// dropped, a row built from the wrong job - fails it. A rendering-only test would not.

import assert from "node:assert/strict";
import { test, afterEach } from "node:test";
import { activate } from "./main";
import type { SurfaceInstance } from "../standalone";

const realFetch = globalThis.fetch;
let mounted: SurfaceInstance | null = null;
let hostEl: HTMLElement | null = null;
// The job names RunJob was asked for, in order.
let submitted: string[] = [];

// No beforeEach, for the reason main-dom.test.ts gives: the DOM suite runs with
// --experimental-test-isolation=none, so a file-scope hook fires for every *-dom test in the
// process. mount() does the per-test setup instead.
afterEach(() => {
  mounted?.deactivate();
  mounted = null;
  hostEl?.remove();
  hostEl = null;
  globalThis.fetch = realFetch;
  location.hash = "";
  submitted = [];
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// The registry as the daemon serializes it: protobuf JSON, so an int64 is a string and a Timestamp
// is RFC3339. Two jobs, because a control that runs "the first one" passes a one-row fixture.
function registry(): unknown {
  return {
    jobs: [
      {
        name: "jobs/rotate-activities",
        description: "Trim the activity trail to its cap",
        target: { sizeBytes: "2048", itemCount: "12" },
        lastRun: { endTime: new Date(Date.now() - 60_000).toISOString(), ok: true },
      },
      {
        name: "jobs/clear-cache",
        description: "Invalidate cached build entries",
        running: true,
        target: { sizeBytes: "1048576" },
      },
    ],
  };
}

type RunReply = { state: string } | { status: number; code: string; message: string };

// serve answers the trail (empty - the control does not depend on it) plus the two JobService
// procedures. run is what RunJob replies with.
function serve(jobs: unknown, run: RunReply): void {
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const headers = new Headers({ "content-type": "application/json" });
    const ok = (body: unknown): Promise<Response> =>
      Promise.resolve({
        ok: true,
        status: 200,
        headers,
        json: () => Promise.resolve(body),
      } as unknown as Response);

    if (url.includes("ActivityService/ListActivityEvents")) {
      return ok({ events: [], nextPageToken: "" });
    }
    if (url.includes("JobService/ListJobs")) return ok(jobs);
    if (url.includes("JobService/RunJob")) {
      // The transport serializes even a JSON request to bytes, so the body arrives as a Uint8Array.
      const raw = init?.body;
      const json = raw instanceof Uint8Array ? new TextDecoder().decode(raw) : String(raw ?? "{}");
      submitted.push((JSON.parse(json) as { name?: string }).name ?? "");
      if ("state" in run) return ok({ state: run.state, invocationId: "inv-1" });
      return Promise.resolve({
        ok: false,
        status: run.status,
        headers,
        json: () => Promise.resolve({ code: run.code, message: run.message }),
      } as unknown as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

// "#port=" is what puts the surface in live mode against a loopback daemon (the stub); "#demo" the
// synthesized trail.
async function mount(hash = "#port=7391"): Promise<HTMLElement> {
  location.hash = hash;
  const host = document.createElement("div");
  document.body.append(host);
  hostEl = host;
  mounted = activate(host);
  await settle();
  return host;
}

function rows(host: HTMLElement): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>(".console-activity-jobs__row")];
}

function runButton(row: HTMLElement): HTMLButtonElement {
  const btn = row.querySelector<HTMLButtonElement>("button");
  assert.ok(btn, "the row carries a run control");
  return btn;
}

function stateText(row: HTMLElement): string {
  return row.querySelector(".console-activity-jobs__state")?.textContent ?? "";
}

test("the registered jobs render with their size and last run", async () => {
  serve(registry(), { state: "SUBMIT_STATE_SUBMITTED" });
  const host = await mount();

  const list = rows(host);
  assert.equal(list.length, 2, "one row per registered job");
  assert.equal(
    list[0].querySelector(".console-activity-jobs__name")?.textContent,
    "rotate-activities",
    "the bare id, which is what `magus server job <name>` takes",
  );
  const meta = list[0].querySelector(".console-activity-jobs__meta")?.textContent ?? "";
  assert.match(meta, /2\.0 KB/);
  assert.match(meta, /12 items/);
  assert.match(meta, /last run/);
  // A job in flight says so on the row rather than through a class, and stays pressable: a second
  // submit coalesces onto the running one.
  assert.equal(list[1].dataset.state, "running");
  assert.equal(stateText(list[1]), "running");
  assert.equal(runButton(list[1]).disabled, false);
});

test("the Run action submits the job on the row", async () => {
  serve(registry(), { state: "SUBMIT_STATE_SUBMITTED" });
  const host = await mount();

  runButton(rows(host)[1]).click();
  await settle();

  assert.deepEqual(submitted, ["jobs/clear-cache"], "the row's own job, by resource name");
  assert.equal(stateText(rows(host)[1]), "started");
});

// ALREADY_RUNNING is a success state on this contract - the daemon coalesced an identical in-flight
// job - so it reads as a fact on the row rather than as a failure.
test("a coalesced submit says already running", async () => {
  serve(registry(), { state: "SUBMIT_STATE_ALREADY_RUNNING" });
  const host = await mount();

  runButton(rows(host)[0]).click();
  await settle();

  assert.deepEqual(submitted, ["jobs/rotate-activities"]);
  assert.equal(stateText(rows(host)[0]), "already running");
});

test("a refused run names the reason and leaves the button pressable", async () => {
  serve(registry(), {
    status: 503,
    code: "unavailable",
    message: "job: no daemon socket to submit to",
  });
  const host = await mount();

  const row = rows(host)[0];
  runButton(row).click();
  await settle();

  assert.match(stateText(row), /could not run rotate-activities: .*no daemon socket/);
  assert.equal(runButton(row).disabled, false, "still pressable - the reader can retry");
});

test("a daemon that refuses the job service says so instead of hiding", async () => {
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    const headers = new Headers({ "content-type": "application/json" });
    if (url.includes("ActivityService/ListActivityEvents")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        headers,
        json: () => Promise.resolve({ events: [], nextPageToken: "" }),
      } as unknown as Response);
    }
    return Promise.resolve({
      ok: false,
      status: 403,
      headers,
      json: () => Promise.resolve({ code: "permission_denied", message: "job control is off" }),
    } as unknown as Response);
  }) as typeof fetch;
  const host = await mount();

  assert.equal(rows(host).length, 0);
  const note = host.querySelector(".console-activity-jobs__note")?.textContent ?? "";
  assert.match(note, /could not list jobs: .*job control is off/);
});

// The demo has no daemon behind it, so a Run action could only fail - the same gate the payload
// expansion is under.
test("the demo trail carries no jobs control", async () => {
  serve(registry(), { state: "SUBMIT_STATE_SUBMITTED" });
  const host = await mount("#demo");

  assert.equal(host.querySelector(".console-activity-jobs"), null);
  assert.deepEqual(submitted, []);
});
