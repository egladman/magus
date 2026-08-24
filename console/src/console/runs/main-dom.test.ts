// main-dom.test.ts - the Runs surface's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// grouping, filtering and faceting it draws are covered in logs/runindex.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the things this surface
// exists to fix:
//
//   - THE FACETS ARE THE ANSWER to "what do I type". They list only values that occur, and clicking
//     one writes its term into the VISIBLE query box - that is what teaches the syntax, so a
//     refactor that applied the filter without showing it would defeat the surface.
//   - THE EMPTY STATES STAY APART. "no daemon", "nothing kept yet" and "your filter matched
//     nothing" are three different facts and only the last is the reader's to fix; the last one
//     also gets a CONTROL, because the way out of an over-narrow query should not be text editing.
//   - THE THREE COUNTS AGREE. The header, the status facet and what a click leaves are all RUNS. An
//     earlier version counted outputs in one of the three, which read as the page lying.
//   - HAND-OFF, NOT RE-HOSTING. Opening output is a real link into the Log Viewer, so it keeps
//     middle-click and copy-link, and the two surfaces cannot drift on how a run renders.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { must } from "../../lib/guards";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";
import type { SurfaceInstance } from "../standalone";

const HOST = "127.0.0.1:7391";
const realFetch = globalThis.fetch;

let mounted: SurfaceInstance | null = null;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
});

// The default-host cell is module state shared with every other DOM test in this process (the suite
// runs with --experimental-test-isolation=none), so it is restored rather than left pointing a
// sibling's surface at a daemon that is not there. The mount is torn down for the same reason: it
// holds an interval, and a leaked one keeps firing against another file's document.
afterEach(() => {
  mounted?.deactivate();
  mounted = null;
  setDefaultHost("");
  globalThis.fetch = realFetch;
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// The query box is debounced, so a turn-based settle does not reach it: those turns are
// setTimeout(0)s, which advance the queue without advancing the CLOCK the debounce waits on. This is
// the one place a real delay is the honest wait.
async function settleFilter(): Promise<void> {
  await new Promise((r) => setTimeout(r, 200));
  await settle();
}

// serve answers the two run feeds and refuses everything else, which is what a daemon that is not
// there looks like from the browser. The SSE stream is among the refusals on purpose: the surface
// must paint from the feeds alone, with the stream only keeping it current afterwards.
function serve(outputs: unknown[], runs: unknown[]): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/api/v1/outputs")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ outputs }),
      } as unknown as Response);
    }
    if (url.includes("/api/v1/runs")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ runs }),
      } as unknown as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

const NOW = Date.now();

function output(over: Record<string, unknown> = {}): unknown {
  return {
    ref: "out1111",
    project: "console",
    target: "build",
    inv: "invA",
    failed: false,
    timestamp_ms: NOW - 60_000,
    duration_ms: 2100,
    ...over,
  };
}

function runLog(over: Record<string, unknown> = {}): unknown {
  return {
    inv: "invA",
    arguments: ["run", "build", "console"],
    trigger: "run",
    started_ms: NOW - 62_000,
    finished_ms: NOW - 60_000,
    status: "pass",
    magus_version: "v0.3.0-test",
    ...over,
  };
}

async function mount(): Promise<HTMLElement> {
  const host = document.createElement("div");
  document.body.append(host);
  mounted = activate(host);
  await settle();
  return host;
}

// remount tears the current surface down before mounting the next, for a test that walks several
// daemon states in one go. The teardown lives here rather than inline because a surface left
// running keeps an interval alive against a document the next mount has replaced.
async function remount(): Promise<HTMLElement> {
  mounted?.deactivate();
  mounted = null;
  return mount();
}

function text(el: Element | null): string {
  return (el?.textContent ?? "").trim();
}

test("a run lists by the command that produced it, not by a ref", async () => {
  serve([output()], [runLog()]);
  const host = await mount();

  const rows = host.querySelectorAll(".console-runs__row");
  assert.equal(rows.length, 1);
  assert.equal(text(rows[0].querySelector(".console-runs__row-cmd")), "magus run build console");
  assert.match(text(rows[0].querySelector(".console-runs__row-meta")), /1 target/);
  assert.equal(text(host.querySelector(".console-runs__count")), "1 run");
});

test("the facets list only values that occur, and a click writes its term into the query box", async () => {
  serve(
    [output({ ref: "o1", inv: "invA" }), output({ ref: "o2", inv: "invB", project: "docs" })],
    [runLog({ inv: "invA" }), runLog({ inv: "invB", trigger: "ci" })],
  );
  const host = await mount();

  const labels = [...host.querySelectorAll(".console-runs__facet-head")].map((h) => text(h));
  assert.deepEqual(labels, ["Status", "Project", "Target", "Trigger"]);

  const projects = [...host.querySelectorAll(".console-runs__facet")]
    .find((f) => text(f.querySelector(".console-runs__facet-head")) === "Project")
    ?.querySelectorAll(".console-runs__facet-value");
  assert.equal(projects?.length, 2, "only the two projects that actually ran");

  const docs = [...(projects ?? [])].find((b) => text(b).startsWith("docs")) as HTMLElement;
  docs.click();
  await settle();

  const query = host.querySelector<HTMLInputElement>("input[type=search]");
  assert.equal(query?.value, "project:docs", "the click is a demonstration of the syntax");
  assert.equal(host.querySelectorAll(".console-runs__row").length, 1);
});

test("clicking the same facet again removes its term", async () => {
  serve([output()], [runLog()]);
  const host = await mount();
  const pass = host.querySelector<HTMLElement>(".console-runs__facet-value");
  pass?.click();
  await settle();
  const query = host.querySelector<HTMLInputElement>("input[type=search]");
  assert.notEqual(query?.value, "");
  host.querySelector<HTMLElement>(".console-runs__facet-value.pf-m-selected")?.click();
  await settle();
  assert.equal(query?.value, "", "a facet toggles rather than only ever narrowing");
});

// The header count, the status facet and what a click leaves are all RUNS. Counting outputs in one
// of the three put "2 runs" beside a facet reading "3", and a click that produced neither.
test("the header count and the status facet both count runs", async () => {
  serve(
    [
      output({ ref: "o1", inv: "invA", target: "build" }),
      output({ ref: "o2", inv: "invA", target: "test" }),
      output({ ref: "o3", inv: "invB", failed: true }),
    ],
    [runLog({ inv: "invA" }), runLog({ inv: "invB", status: "fail" })],
  );
  const host = await mount();

  assert.equal(text(host.querySelector(".console-runs__count")), "2 runs, 1 failed");
  const status = [...host.querySelectorAll(".console-runs__facet")].find(
    (f) => text(f.querySelector(".console-runs__facet-head")) === "Status",
  );
  const counts = [...(status?.querySelectorAll(".console-runs__facet-count") ?? [])].map((c) =>
    text(c),
  );
  assert.deepEqual(counts, ["1", "1"], "two runs, one of each outcome - not three outputs");
});

test("the detail pane names the run's facts and links its targets into the viewer", async () => {
  serve([output({ error: "boom", failed: true })], [runLog({ status: "fail" })]);
  const host = await mount();

  assert.equal(text(host.querySelector(".console-runs__detail-cmd")), "magus run build console");
  assert.equal(text(host.querySelector(".console-runs__pill")), "failed");
  const labels = [...host.querySelectorAll(".console-runs__fact-label")].map((l) => text(l));
  assert.deepEqual(labels, ["When", "Duration", "Trigger", "magus", "Run id"]);
  assert.equal(text(host.querySelector(".console-runs__target-error")), "boom");

  // Real links, so middle-click and copy-link work and the viewer stays the one thing that renders
  // a run. "logs/" not "../logs/": every surface page carries <base href="../">.
  const links = [...host.querySelectorAll<HTMLAnchorElement>(".console-runs__open")];
  assert.ok(links.length >= 2);
  assert.ok(
    links.every((a) => a.getAttribute("href")?.startsWith("logs/#")),
    links.map((a) => a.getAttribute("href")).join(" "),
  );
  assert.ok(links.some((a) => a.getAttribute("href")?.includes("inv=invA")));
  assert.ok(links.some((a) => a.getAttribute("href")?.includes("ref=out1111")));
});

test("no daemon, nothing kept, and nothing matching are three different empty states", async () => {
  // No daemon at all: the host never resolves, so nothing is fetched.
  const cold = await mount();
  assert.match(text(cold.querySelector(".console-runs__empty-title")), /No daemon connected/);

  serve([], []);
  const bare = await remount();
  assert.match(text(bare.querySelector(".console-runs__empty-title")), /No runs kept yet/);

  serve([output()], [runLog()]);
  const full = await remount();
  const query = must(full.querySelector<HTMLInputElement>("input[type=search]"));
  query.value = "project:nothing-matches-this";
  query.dispatchEvent(new Event("input"));
  await settleFilter();
  assert.match(text(full.querySelector(".console-runs__empty-title")), /No runs match/);

  // A control, not just a sentence: the way out of an over-narrow query is one click.
  const clear = full.querySelector<HTMLElement>(".console-runs__empty .pf-v6-c-button");
  assert.ok(clear, "a filtered-to-nothing list offers a way back");
  clear.click();
  await settle();
  assert.equal(query.value, "");
  assert.equal(full.querySelectorAll(".console-runs__row").length, 1);
});

// Relative labels age on a page nobody is touching, so they carry the instant they were computed
// from and a ticker rewrites them in place. Without the stamp there is nothing for it to find.
test("every relative time carries the instant it was computed from", async () => {
  serve([output()], [runLog()]);
  const host = await mount();
  const stamped = [...host.querySelectorAll<HTMLElement>("[data-time]")];
  assert.ok(stamped.length >= 2, "the row's when, and the detail pane's gloss");
  for (const el of stamped) {
    assert.match(el.dataset.time ?? "", /^\d+$/);
    assert.match(text(el), /ago|\d\d:\d\d/);
  }
});
