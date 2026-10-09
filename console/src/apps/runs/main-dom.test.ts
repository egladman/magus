// main-dom.test.ts - the Runs app's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// grouping, filtering and faceting it draws are covered in logs/runindex.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the things this app
// exists to fix:
//
//   - THE FACETS ARE THE ANSWER to "what do I type". They list only values that occur, and clicking
//     one writes its term into the VISIBLE query box - that is what teaches the syntax, so a
//     refactor that applied the filter without showing it would defeat the app.
//   - THE EMPTY STATES STAY APART. "no server", "nothing kept yet" and "your filter matched
//     nothing" are three different facts and only the last is the reader's to fix; the last one
//     also gets a CONTROL, because the way out of an over-narrow query should not be text editing.
//   - THE THREE COUNTS AGREE. The header, the status facet and what a click leaves are all RUNS. An
//     earlier version counted outputs in one of the three, which read as the page lying.
//   - LINK, NOT RE-HOSTING. Opening output is a real link into the Log Viewer, so it keeps
//     middle-click and copy-link, and the two apps cannot drift on how a run renders.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { must } from "../../lib/guards";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";
import type { AppInstance } from "../../desktop/standalone";

const HOST = "127.0.0.1:7391";
const realFetch = globalThis.fetch;

let mounted: AppInstance | null = null;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
});

// The default-host cell is module state shared with every other DOM test in this process (the suite
// runs with --experimental-test-isolation=none), so it is restored rather than left pointing a
// sibling's app at a server that is not there. The mount is torn down for the same reason: it
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

// serve answers the two run feeds and refuses everything else, which is what a server that is not
// there looks like from the browser. The SSE stream is among the refusals on purpose: the app
// must paint from the feeds alone, with the stream only keeping it current afterwards.
function serve(outputs: unknown[], runs: unknown[]): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    // Connect unary in the transport's JSON codec. Distinguished by PROCEDURE, not by path: both
    // feeds are ViewerService now, so matching the service name alone would answer either with the
    // other's body.
    if (url.includes("ViewerService/ListOutputs")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        json: () => Promise.resolve({ outputs }),
      } as unknown as Response);
    }
    if (url.includes("ViewerService/ListInvocations")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        json: () => Promise.resolve({ invocations: runs }),
      } as unknown as Response);
    }
    // A server with nothing kept still answers its liveness route, which is what separates it from
    // an address with nothing behind it.
    if (url.endsWith("/livez")) {
      return Promise.resolve({ ok: true, status: 200 } as unknown as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

// serveNothing is a configured server address with nothing listening behind it.
function serveNothing(): void {
  setDefaultHost(HOST);
  globalThis.fetch = (() => Promise.reject(new Error("stub: refused"))) as typeof fetch;
}

const NOW = Date.now();

// The wire shape, in protobuf JSON: a Timestamp is RFC3339, a Duration is a seconds string, and an
// enum is its declared name. Written as the server actually serializes it so a fixture cannot pass
// while the real feed would not parse.
function output(over: Record<string, unknown> = {}): unknown {
  return {
    ref: "out1111",
    project: "console",
    target: "build",
    invocation: "invA",
    failed: false,
    createTime: new Date(NOW - 60_000).toISOString(),
    duration: "2.100s",
    ...over,
  };
}

function runLog(over: Record<string, unknown> = {}): unknown {
  return {
    id: "invA",
    command: { arguments: ["run", "build", "console"], trigger: "TRIGGER_RUN" },
    startTime: new Date(NOW - 62_000).toISOString(),
    endTime: new Date(NOW - 60_000).toISOString(),
    status: "STATUS_PASS",
    magusVersion: "v0.3.0-test",
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

// remount tears the current app down before mounting the next, for a test that walks several
// server states in one go. The teardown lives here rather than inline because an app left
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
    [
      output({ ref: "o1", invocation: "invA" }),
      output({ ref: "o2", invocation: "invB", project: "docs" }),
    ],
    [
      runLog({ id: "invA" }),
      runLog({ id: "invB", command: { arguments: ["ci"], trigger: "TRIGGER_CI" } }),
    ],
  );
  const host = await mount();

  const labels = [...host.querySelectorAll(".console-runs__facet-head")].map((h) => text(h));
  assert.deepEqual(labels, ["Status", "Project", "Target", "Trigger"]);

  const projects = [...host.querySelectorAll(".console-runs__facet")]
    .find((f) => text(f.querySelector(".console-runs__facet-head")) === "Project")
    ?.querySelectorAll(".console-runs__facet-value");
  assert.equal(projects?.length, 2, "only the two projects that actually ran");

  const docs = [...(projects ?? [])].find((b) => text(b).startsWith("docs")) as HTMLElement;
  must(docs.querySelector<HTMLElement>("button")).click();
  await settle();

  const query = host.querySelector<HTMLInputElement>(".console-runs__field input");
  assert.equal(query?.value, "project:docs", "the click is a demonstration of the syntax");
  assert.equal(host.querySelectorAll(".console-runs__row").length, 1);
});

test("clicking the same facet again removes its term", async () => {
  serve([output()], [runLog()]);
  const host = await mount();
  const pass = host.querySelector<HTMLElement>(".console-runs__facet-value button");
  pass?.click();
  await settle();
  const query = host.querySelector<HTMLInputElement>(".console-runs__field input");
  assert.notEqual(query?.value, "");
  host.querySelector<HTMLElement>(".console-runs__facet-value.pf-m-selected button")?.click();
  await settle();
  assert.equal(query?.value, "", "a facet toggles rather than only ever narrowing");
});

// The header count, the status facet and what a click leaves are all RUNS. Counting outputs in one
// of the three put "2 runs" beside a facet reading "3", and a click that produced neither.
test("the header count and the status facet both count runs", async () => {
  serve(
    [
      output({ ref: "o1", invocation: "invA", target: "build" }),
      output({ ref: "o2", invocation: "invA", target: "test" }),
      output({ ref: "o3", invocation: "invB", failed: true }),
    ],
    [runLog({ id: "invA" }), runLog({ id: "invB", status: "STATUS_FAIL" })],
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
  serve([output({ error: "boom", failed: true })], [runLog({ status: "STATUS_FAIL" })]);
  const host = await mount();

  assert.equal(text(host.querySelector(".console-runs__detail-cmd")), "magus run build console");
  assert.equal(text(host.querySelector(".console-runs__pill")), "Failed");
  const labels = [...host.querySelectorAll(".console-runs__fact-label")].map((l) => text(l));
  assert.deepEqual(labels, ["When", "Duration", "Trigger", "magus", "Run id"]);
  assert.equal(text(host.querySelector(".console-runs__target-error")), "boom");

  // Real links, so middle-click and copy-link work and the viewer stays the one thing that renders
  // a run. "logs/" not "../logs/": every app page carries <base href="../">.
  const links = [...host.querySelectorAll<HTMLAnchorElement>(".console-runs__open")];
  assert.ok(links.length >= 2);
  assert.ok(
    links.every((a) => a.getAttribute("href")?.startsWith("logs/#")),
    links.map((a) => a.getAttribute("href")).join(" "),
  );
  assert.ok(links.some((a) => a.getAttribute("href")?.includes("inv=invA")));
  assert.ok(links.some((a) => a.getAttribute("href")?.includes("ref=out1111")));
});

test("no server, nothing kept, and nothing matching are three different empty states", async () => {
  // No server at all: the host never resolves, so nothing is fetched.
  const cold = await mount();
  assert.match(text(cold.querySelector(".console-runs__empty-title")), /No server connected/);

  serve([], []);
  const bare = await remount();
  assert.match(text(bare.querySelector(".console-runs__empty-title")), /No runs kept yet/);

  // The run feeds read a refused connection as an empty list; that must not pass for "nothing kept".
  serveNothing();
  const dead = await remount();
  assert.match(
    text(dead.querySelector(".console-runs__empty-title")),
    /Could not reach the server/,
  );
  const labels = [
    ...dead.querySelectorAll(".console-runs__empty [data-empty-way] .pf-v6-c-button"),
  ];
  assert.deepEqual(
    labels.map((b) => text(b)),
    ["Retry", "Change address", "Setup guide"],
  );
  // Retry against a server still down repaints the same prompt, and must keep the button the reader
  // pressed rather than drop their focus with a rebuilt one.
  (labels[0] as HTMLElement).click();
  await settle();
  assert.equal(
    dead.querySelector(".console-runs__empty [data-empty-way] .pf-v6-c-button"),
    labels[0],
  );

  serve([output()], [runLog()]);
  const full = await remount();
  const query = must(full.querySelector<HTMLInputElement>(".console-runs__field input"));
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

// The list's highlight and the detail pane read one value. They used to read two: the highlight
// followed the reader's choice while the detail fell back to the newest run, so a filter that hid the
// choice left a page showing one run and marking another (or marking none).
test("the highlighted row is the run the detail pane shows", async () => {
  serve(
    [
      output({ ref: "o1", invocation: "invA", project: "console" }),
      output({ ref: "o2", invocation: "invB", project: "docs" }),
    ],
    [
      runLog({ id: "invA", command: { arguments: ["run", "alpha"], trigger: "TRIGGER_RUN" } }),
      runLog({ id: "invB", command: { arguments: ["run", "beta"], trigger: "TRIGGER_RUN" } }),
    ],
  );
  const host = await mount();
  const current = (): string[] =>
    [...host.querySelectorAll<HTMLElement>('.console-runs__row[aria-current="true"]')].map((b) =>
      text(b.querySelector(".console-runs__row-cmd")),
    );

  assert.equal(current().length, 1, "something is highlighted before anything is chosen");
  assert.deepEqual(current(), [text(host.querySelector(".console-runs__detail-cmd"))]);

  const rows = [...host.querySelectorAll<HTMLElement>(".console-runs__row")];
  const other = must(rows.find((r) => r.getAttribute("aria-current") !== "true"));
  const otherInv = other.dataset.inv;
  other.click();
  await settle();
  assert.deepEqual(current(), [text(other.querySelector(".console-runs__row-cmd"))]);
  assert.deepEqual(current(), [text(host.querySelector(".console-runs__detail-cmd"))]);

  // A filter that hides the choice moves both to the newest run that is left, together.
  const query = must(host.querySelector<HTMLInputElement>(".console-runs__field input"));
  query.value = otherInv === "invB" ? "project:console" : "project:docs";
  query.dispatchEvent(new Event("input"));
  await settleFilter();
  assert.equal(current().length, 1);
  assert.deepEqual(current(), [text(host.querySelector(".console-runs__detail-cmd"))]);
});

// A run's outcome is a mark with a shape and a word, and a row is a list item holding one button
// whose selection is aria-current, not a class alone.
test("a row names its outcome in words and marks the current run for assistive tech", async () => {
  serve([output({ failed: true })], [runLog({ status: "STATUS_FAIL" })]);
  const host = await mount();

  const item = must(host.querySelector(".console-runs__list > li"));
  const row = must(item.querySelector<HTMLElement>("button.console-runs__row"));
  assert.equal(row.getAttribute("aria-current"), "true");
  assert.match(text(row), /Failed/, "the outcome is in the row's text, not only its colour");
  assert.ok(row.querySelector(".pf-v6-c-icon"), "and it has a shape");
});

// Every target in a run says "Open output"; a reader that lists links needs to know which.
test("each Open output link names its target", async () => {
  serve(
    [output({ ref: "o1", target: "build" }), output({ ref: "o2", target: "test" })],
    [runLog()],
  );
  const host = await mount();

  const names = [
    ...host.querySelectorAll<HTMLAnchorElement>(".console-runs__target .console-runs__open"),
  ].map((a) => a.getAttribute("aria-label"));
  assert.deepEqual(names, ["Open output of console:build", "Open output of console:test"]);
});

// The filter shows what it left, and clearing it is a control inside the box.
test("the filter counts its results and clears from the box", async () => {
  serve(
    [
      output({ ref: "o1", invocation: "invA", project: "console" }),
      output({ ref: "o2", invocation: "invB", project: "docs" }),
    ],
    [runLog({ id: "invA" }), runLog({ id: "invB" })],
  );
  const host = await mount();
  const query = must(host.querySelector<HTMLInputElement>(".console-runs__field input"));
  query.value = "project:docs";
  query.dispatchEvent(new Event("input"));
  await settleFilter();

  assert.equal(text(host.querySelector(".console-runs__field .console-filter__count")), "1 of 2");
  assert.match(text(host.querySelector(".console-runs__field [role=status]")), /1 of 2 runs match/);
  must(host.querySelector<HTMLElement>(".console-runs__field .console-filter__clear")).click();
  await settle();
  assert.equal(query.value, "");
  assert.equal(host.querySelectorAll(".console-runs__row").length, 2);
});
