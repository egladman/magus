// main-dom.test.ts - the Jobs view's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// models it draws are covered next door in jobs.test.ts and run.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the things a later
// refactor would most plausibly break:
//
//   - ONE LIST, BOTH HOLDERS. The daemon's own maintenance and the work a session holds are the
//     same kind of thing here, told apart by the holder on the row - and only what the daemon holds
//     offers a Run control, which submits THAT row's job.
//   - The EMPTY STATES stay apart. Per source: "no daemon", "the daemon will not serve this", and
//     "there is no work" are three different facts, and only the last means nothing is happening.
//   - WHICH SOURCE OPENS is decided by the data. Jobs in hand means there is work to look at;
//     anything else hands the view to Targets, which is what a person doing plain work came for. An
//     explicit pick then sticks - the poll must not overrule it.
//   - The DRAWING IS NOT THE ACCESSIBLE SURFACE. The stage is aria-hidden and the node list beside
//     it carries the same nodes with their states in words.
//   - FOCUS IS NEVER TAKEN. Mounting and polling must leave the caret where it was; only an
//     explicit navigation command moves it. That includes a repaint: a poll that returns the same
//     answer must not rebuild the element a reader is standing on.
//   - A MOUNT IS ITS OWN SURFACE. Two panes can hold two Jobs views, so a read that answers late
//     must not paint over a source the reader has since switched, hiding one pane must not silence
//     the other, and closing one must not take the shared commands away from the one still open.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { dispatchCommand, listCommands } from "../commands";
import { activate, type JobsInstance } from "./main";

const HOST = "127.0.0.1:7391";
const JSON_HEADERS = new Headers({ "content-type": "application/json" });

const realFetch = globalThis.fetch;
// The job names RunJob was asked for, in order.
let submitted: string[] = [];

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
});

// The default-host cell is module state shared with every other DOM test in this process (the suite
// runs with --experimental-test-isolation=none), so it is restored after each test rather than left
// pointing a sibling's surface at a daemon that is not there.
afterEach(() => {
  setDefaultHost("");
  globalThis.fetch = realFetch;
  submitted = [];
});

// settle lets the two awaited reads in refresh() resolve. Turns rather than a delay: everything
// under test resolves on the microtask/macrotask queue, and a fixed sleep would be either flaky or
// slow.
async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// A job as the daemon serializes it: protobuf JSON, so an enum is its name and an int64 is a string.
function sessionJob(id: string, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    name: "jobs/" + id,
    id,
    holder: "JOB_HOLDER_SESSION",
    state: "declared",
    ...extra,
  };
}

function daemonJob(id: string, extra: Record<string, unknown> = {}): Record<string, unknown> {
  return { name: "jobs/" + id, id, holder: "JOB_HOLDER_DAEMON", state: "declared", ...extra };
}

type RunReply = { state: string } | { status: number; code: string; message: string };

// serve points the view at a daemon that answers only the routes named here. Everything else is
// refused, which is what a daemon that is not there looks like from the browser, and which the view
// must survive without blanking what it has.
function serve(routes: {
  jobs?: () => unknown;
  run?: RunReply;
  plan?: (url: string) => unknown;
  feeds?: boolean; // answer BOTH activity feeds - the status RPC and the run descriptors
}): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const ok = (body: unknown): Promise<Response> =>
      Promise.resolve({
        ok: true,
        status: 200,
        headers: JSON_HEADERS,
        json: () => Promise.resolve(body),
      } as unknown as Response);

    if (url.includes("JobService/ListJobs") && routes.jobs) {
      return Promise.resolve(routes.jobs() as Response);
    }
    if (url.includes("JobService/RunJob")) {
      // The transport serializes even a JSON request to bytes, so the body arrives as a Uint8Array.
      const raw = init?.body;
      const json = raw instanceof Uint8Array ? new TextDecoder().decode(raw) : String(raw ?? "{}");
      submitted.push((JSON.parse(json) as { name?: string }).name ?? "");
      const reply = routes.run ?? { state: "SUBMIT_STATE_SUBMITTED" };
      if ("state" in reply) return ok({ state: reply.state, invocationId: "inv-1" });
      return Promise.resolve({
        ok: false,
        status: reply.status,
        headers: JSON_HEADERS,
        json: () => Promise.resolve({ code: reply.code, message: reply.message }),
      } as unknown as Response);
    }
    if (url.includes("/api/v1/plan") && routes.plan) {
      return Promise.resolve(routes.plan(url) as Response);
    }
    if (routes.feeds && url.includes("StatusService")) {
      // A Connect unary response in the transport's JSON codec, carrying an EMPTY frame: what these
      // tests need from the status read is only that it SUCCEEDED, so there is no frame to build.
      return ok({});
    }
    if (routes.feeds && url.includes("ViewerService")) {
      // The run feed, over Connect. Empty for the same reason the status frame above is: these
      // tests need the read to SUCCEED, not to carry rows.
      return ok({ outputs: [] });
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

function okJobs(jobs: unknown[], overlaps: unknown[] = []): () => unknown {
  return () => ({
    ok: true,
    status: 200,
    headers: JSON_HEADERS,
    json: () => Promise.resolve({ jobs, overlaps }),
  });
}

// refused is the daemon DECLINING the service, which is a different fact from one that broke.
function refusedJobs(status: number, code: string, message: string): () => unknown {
  return () => ({
    ok: false,
    status,
    headers: JSON_HEADERS,
    json: () => Promise.resolve({ code, message }),
  });
}

// secondsAgo builds the unix-second stamp a job carries, so a test can put one far enough in the
// past to be stale without reaching for fake timers.
function secondsAgo(secs: number): string {
  return String(Math.floor(Date.now() / 1000) - secs);
}

// okPlan answers /api/v1/plan with a target-plan body in the contract's shape.
function okPlan(body: Record<string, unknown>): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

// The target plan every drawing test reads: build waits on generate, test waits on build.
//
// .:build is RUNNING and still carries a ref (run.ts's RunPlanNode.ref says why the wire does that).
// A fixture where running implied no ref would let the mislabelling this pins for pass unnoticed.
const RUN_BODY = {
  target: "ci",
  anchor: "running",
  nodes: [
    { id: ".:generate", project: ".", target: "generate", state: "pass", ref: "out1a2b3c" },
    { id: ".:build", project: ".", target: "build", state: "running", ref: "out7g8h9i" },
    { id: "console:test", project: "console", target: "test", state: "idle", ref: "" },
  ],
  edges: [
    { from: ".:generate", to: ".:build" },
    { from: ".:build", to: "console:test" },
  ],
};

// pickSource clicks a source toggle, which is also what retires the auto rule: from here on the
// reader has chosen and the poll may not move them.
function pickSource(host: HTMLElement, source: "jobs" | "targets"): void {
  host.querySelector<HTMLElement>(`.console-plan-source [data-source="${source}"]`)?.click();
}

function pressed(host: HTMLElement, source: "jobs" | "targets"): string {
  return (
    host
      .querySelector(`.console-plan-source [data-source="${source}"]`)
      ?.getAttribute("aria-pressed") ?? ""
  );
}

function summaryText(host: HTMLElement): string {
  return host.querySelector(".console-plan-summary")?.textContent ?? "";
}

function rows(host: HTMLElement): HTMLElement[] {
  return [...host.querySelectorAll<HTMLElement>(".console-plan-list__row")];
}

function rowFor(host: HTMLElement, id: string): HTMLElement | undefined {
  return rows(host).find(
    (r) => r.querySelector<HTMLElement>(".console-plan-list__item")?.dataset.id === id,
  );
}

function runState(row: HTMLElement): string {
  return row.querySelector(".console-plan-list__runstate")?.textContent ?? "";
}

// mount builds the view into a fresh host and hands back its controller. EVERY caller must run the
// teardown: activate starts a poll interval, and an interval nobody clears keeps the test process
// alive.
function mount(): { host: HTMLElement; instance: JobsInstance; teardown: () => void } {
  const host = document.createElement("div");
  document.body.append(host);
  const instance = activate(host);
  return { host, instance, teardown: () => instance.deactivate() };
}

function phase(host: HTMLElement): string {
  return host.querySelector<HTMLElement>(".console-plan-layout")?.dataset.phase ?? "";
}

function text(host: HTMLElement): string {
  return host.textContent ?? "";
}

test("with no daemon configured it says that, rather than showing an empty view", async () => {
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No daemon connected/);
  } finally {
    teardown();
  }
});

// A daemon that will not serve the job service is not a workspace with nothing happening in it.
// Reached by asking for Jobs, because a refusal is exactly the case that hands the view to Targets.
test("a daemon that refuses the job service says so, not that there is no work", async () => {
  serve({ jobs: refusedJobs(403, "permission_denied", "job control is off") });
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "jobs");
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /Jobs are not served here/);
    assert.match(text(host), /job control is off/);
  } finally {
    teardown();
  }
});

test("a job service that errors blames the service, and says what it said", async () => {
  serve({ jobs: refusedJobs(500, "internal", "job store is closed") });
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "jobs");
    await settle();
    assert.match(text(host), /Could not read the jobs/);
    assert.match(text(host), /job store is closed/);
    assert.doesNotMatch(text(host), /Jobs are not served here/);
  } finally {
    teardown();
  }
});

test("a served but empty listing is a different sentence from a refusal", async () => {
  serve({ jobs: okJobs([]) });
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "jobs");
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No jobs/);
    assert.doesNotMatch(text(host), /Jobs are not served here/);
  } finally {
    teardown();
  }
});

test("the jobs draw one node and one list row each", async () => {
  serve({
    jobs: okJobs([
      sessionJob("root", { state: "running", goal: "ship it" }),
      sessionJob("b1", { parent: "root", state: "pass" }),
      sessionJob("b2", {
        parent: "root",
        state: "no_return",
        dependsOn: ["b1"],
        readOnly: true,
      }),
    ]),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "ready");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
    assert.equal(host.querySelectorAll(".console-plan-list__item").length, 3);
    // Two parent edges and one depends_on, and the kinds stay apart in the markup so the
    // stylesheet can draw them as the different relations they are.
    const kinds = [...host.querySelectorAll<SVGElement>(".console-plan-edge")].map(
      (e) => e.dataset.kind,
    );
    assert.deepEqual(kinds.sort(), ["depends_on", "parent", "parent"]);
    // no_return keeps its own state attribute all the way to the DOM: nothing along the way may
    // collapse it into fail.
    const states = [...host.querySelectorAll<SVGElement>(".console-plan-node")].map(
      (n) => n.dataset.state,
    );
    assert.deepEqual(states.sort(), ["no_return", "pass", "running"]);
    assert.equal(host.querySelectorAll(".console-plan-node[data-readonly]").length, 1);
  } finally {
    teardown();
  }
});

// ---- one list, both holders ------------------------------------------------

test("a row says who holds the job, and the daemon's own carry what they maintain", async () => {
  serve({
    jobs: okJobs([
      daemonJob("clear-cache", {
        description: "Invalidate cached build entries",
        target: { sizeBytes: "2048", itemCount: "12" },
        lastRun: { endTime: new Date(Date.now() - 120_000).toISOString(), ok: true },
      }),
      sessionJob("docs-rename", { model: "opus" }),
    ]),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const daemon = rowFor(host, "clear-cache")?.textContent ?? "";
    assert.match(daemon, /daemon/);
    assert.match(daemon, /2\.0 KB, 12 items/);
    assert.match(daemon, /last run 2m ago/);
    const session = rowFor(host, "docs-rename")?.textContent ?? "";
    assert.match(session, /session/);
    assert.match(session, /opus/);
  } finally {
    teardown();
  }
});

// A session's job is held by that session: magus never starts one, so offering a control that
// cannot work would be describing a capability that does not exist.
test("only a job the daemon holds offers a Run control", async () => {
  serve({
    jobs: okJobs([daemonJob("clear-cache"), sessionJob("docs-rename")]),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.ok(rowFor(host, "clear-cache")?.querySelector(".console-plan-list__run"));
    assert.equal(rowFor(host, "docs-rename")?.querySelector(".console-plan-list__run"), null);
  } finally {
    teardown();
  }
});

// The one that carries the feature: the name comes off the RunJob request body, so disconnecting
// the control from the client - a listener dropped, a row built from the wrong job - fails it.
test("the Run action submits the job on the row", async () => {
  serve({ jobs: okJobs([daemonJob("rotate-activities"), daemonJob("clear-cache")]) });
  const { host, teardown } = mount();
  try {
    await settle();
    const row = rowFor(host, "clear-cache");
    row?.querySelector<HTMLButtonElement>(".console-plan-list__run")?.click();
    await settle();
    assert.deepEqual(submitted, ["jobs/clear-cache"], "the row's own job, by resource name");
    assert.equal(runState(row as HTMLElement), "started");
  } finally {
    teardown();
  }
});

// ALREADY_RUNNING is a success state on this contract - the daemon coalesced an identical in-flight
// job - so it reads as a fact on the row rather than as a failure.
test("a coalesced submit says already running", async () => {
  serve({
    jobs: okJobs([daemonJob("rotate-activities")]),
    run: { state: "SUBMIT_STATE_ALREADY_RUNNING" },
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const row = rowFor(host, "rotate-activities");
    row?.querySelector<HTMLButtonElement>(".console-plan-list__run")?.click();
    await settle();
    assert.deepEqual(submitted, ["jobs/rotate-activities"]);
    assert.equal(runState(row as HTMLElement), "already running");
  } finally {
    teardown();
  }
});

test("a refused run names the reason and leaves the control pressable", async () => {
  serve({
    jobs: okJobs([daemonJob("rotate-activities")]),
    run: { status: 503, code: "unavailable", message: "job: no daemon socket to submit to" },
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const row = rowFor(host, "rotate-activities") as HTMLElement;
    const btn = row.querySelector<HTMLButtonElement>(".console-plan-list__run");
    btn?.click();
    await settle();
    assert.match(runState(row), /could not run rotate-activities: .*no daemon socket/);
    assert.equal(btn?.disabled, false, "still pressable - the reader can retry");
  } finally {
    teardown();
  }
});

test("the overview line is the polite live region; the list is not", async () => {
  serve({ jobs: okJobs([sessionJob("root", { state: "no_return" })]) });
  const { host, teardown } = mount();
  try {
    await settle();
    const summary = host.querySelector(".console-plan-summary");
    assert.equal(summary?.getAttribute("aria-live"), "polite");
    assert.match(summary?.textContent ?? "", /1 job\. 1 no-return\./);
    assert.equal(
      host.querySelector(".console-plan-list")?.getAttribute("aria-live"),
      null,
      "a list rebuilt on a four-second poll must not re-announce every row",
    );
  } finally {
    teardown();
  }
});

// The same split the graph explorer makes between its canvas and its node cloud: a laid-out drawing
// has no reading order, so the list is what assistive tech is given.
test("the drawing is hidden from assistive tech and the job list is its twin", async () => {
  serve({ jobs: okJobs([sessionJob("root")]) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      host.querySelector(".console-plan-stage__svg")?.getAttribute("aria-hidden"),
      "true",
    );
    assert.equal(host.querySelector(".console-plan-tree")?.getAttribute("aria-label"), "Jobs");
    assert.match(host.querySelector(".console-plan-list__item")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("mounting does not move focus", async () => {
  serve({ jobs: okJobs([sessionJob("root")]) });
  const elsewhere = document.createElement("input");
  document.body.append(elsewhere);
  const { teardown } = mount();
  try {
    elsewhere.focus();
    await settle();
    assert.equal(
      document.activeElement,
      elsewhere,
      "a view that repaints on a timer must never pull the caret out of what someone is doing",
    );
  } finally {
    teardown();
  }
});

// Navigation is the ONE thing allowed to move focus, because that is what the reader asked for.
test("the next-job key selects a job and focuses its row", async () => {
  serve({ jobs: okJobs([sessionJob("root"), sessionJob("b1", { parent: "root" })]) });
  const { host, teardown } = mount();
  try {
    await settle();
    const root = host.querySelector<HTMLElement>(".console-plan-layout");
    root?.dispatchEvent(new KeyboardEvent("keydown", { key: "j", bubbles: true }));
    const first = host.querySelector<HTMLElement>(".console-plan-list__item");
    assert.equal(first?.getAttribute("aria-current"), "true");
    assert.equal(document.activeElement, first);
    assert.match(host.querySelector(".console-plan-detail")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("selecting a job shows its goal, checkpoint, model, check and paths", async () => {
  serve({
    jobs: okJobs([
      sessionJob("root", {
        goal: "draw the jobs",
        checkpoint: "after the stage lands",
        model: "opus",
        check: "console:test",
        writePaths: ["console/src/console/plan/"],
        denyPaths: ["internal/"],
        readPaths: ["docs/"],
      }),
    ]),
    feeds: true,
  });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /session job/);
    assert.match(detail, /draw the jobs/);
    assert.match(detail, /after the stage lands/);
    assert.match(detail, /opus/);
    assert.match(detail, /console:test/);
    assert.match(detail, /console\/src\/console\/plan\//);
    assert.match(detail, /internal\//);
    assert.match(detail, /docs\//);
    // Both feeds answered and carried nothing for this job: nothing stamps one onto them yet, and
    // the view says so rather than leaving a blank that reads as "this job ran nothing".
    assert.match(detail, /No runs are attributed to this job/);
  } finally {
    teardown();
  }
});

// The other half of that sentence, and the reason it is two sentences. Feeds that did not answer
// cannot attribute a run to ANY job, which is a fact about the daemon; reporting it as the one above
// would blame the job for a daemon that is not talking.
test("a job whose activity feeds could not be read says that instead", async () => {
  serve({ jobs: okJobs([sessionJob("root")]) });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /The activity feeds could not be read/);
    assert.match(detail, /stub: no network/, "and it carries what actually went wrong");
    assert.doesNotMatch(detail, /No runs are attributed to this job/);
  } finally {
    teardown();
  }
});

// ---- what the daemon reports about the work --------------------------------

// Two jobs claiming one path is a FACT the daemon derived from two declarations, and the view's job
// is to put it in front of a reader. Nothing is blocked, reordered, or failed by it - and it is
// drawn on BOTH rows, because either one is where the reader might be standing.
test("an overlap warns on both rows and names the other job in the detail", async () => {
  serve({
    jobs: okJobs(
      [
        sessionJob("a", { state: "running", writePaths: ["internal/job"] }),
        sessionJob("b", { writePaths: ["internal/job/store.go"] }),
        sessionJob("c", { state: "pass" }),
      ],
      [
        {
          jobA: "a",
          jobB: "b",
          pathsA: ["internal/job"],
          pathsB: ["internal/job/store.go"],
        },
      ],
    ),
    feeds: true,
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const warned = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")].filter(
      (el) => el.dataset.warn !== undefined,
    );
    assert.deepEqual(
      warned.map((el) => el.dataset.id),
      ["a", "b"],
      "the job with no reported overlap carries no warning",
    );
    // In words, not in color alone.
    assert.match(warned[0]?.textContent ?? "", /overlap/);
    assert.equal(host.querySelectorAll(".console-plan-node[data-warn]").length, 2);

    host.querySelector<HTMLElement>('.console-plan-list__item[data-id="a"]')?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /Overlaps/);
    assert.match(detail, /b: internal\/job\/store\.go/);
  } finally {
    teardown();
  }
});

// The heartbeat. A job is re-put on every state change, so a running one nobody has touched in a
// long time is worth pointing at - as a question for the reader, never as a state the store invented
// for them.
test("a running job nobody has touched reads as stale; a fresh one just reads its age", async () => {
  // Minutes and hours rather than seconds: the age is asserted verbatim, and a fixture a few
  // seconds old would tick over into the next second while the view was still settling.
  serve({
    jobs: okJobs([
      sessionJob("fresh", { state: "running", updated: secondsAgo(120) }),
      sessionJob("quiet", { state: "running", updated: secondsAgo(3600) }),
      sessionJob("done", { state: "pass", updated: secondsAgo(3600) }),
    ]),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const age = (id: string): HTMLElement | null =>
      host.querySelector<HTMLElement>(
        `.console-plan-list__item[data-id="${id}"] .console-plan-list__age`,
      );
    assert.equal(age("fresh")?.textContent, "2m");
    assert.equal(age("fresh")?.hasAttribute("data-stale"), false);
    assert.equal(age("quiet")?.textContent, "1h stale", "the word rides along with the number");
    assert.equal(age("quiet")?.hasAttribute("data-stale"), true);
    assert.equal(host.querySelectorAll(".console-plan-node[data-stale]").length, 1);
    // A finished job is not going to be touched again, so an age beside it would read as a problem
    // where there is none.
    assert.equal(age("done")?.textContent, "");
  } finally {
    teardown();
  }
});

// What the next agent inherits. A release with no digest would leave a waiting job unable to tell
// whether it is starting from the tree the last one left.
test("a released path shows with a short digest", async () => {
  serve({
    jobs: okJobs([
      sessionJob("a", {
        state: "running",
        writePaths: ["docs"],
        releases: [
          {
            path: "internal/job/store.go",
            digest: "sha256:1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b",
            releasedAt: secondsAgo(60),
          },
          { path: "gone.go", digest: "absent", releasedAt: secondsAgo(60) },
        ],
      }),
    ]),
    feeds: true,
  });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /Released/);
    assert.match(detail, /internal\/job\/store\.go/);
    assert.match(detail, /sha256:1a2b3c4d5e6f/, "short enough to compare, long enough to be one");
    assert.doesNotMatch(detail, /9e0f1a2b/, "the full digest would push the path off the sheet");
    // Not every digest is a hash: a path with nothing on disk says so in words rather than being
    // dressed up as one.
    assert.match(detail, /absent/);
  } finally {
    teardown();
  }
});

test("teardown empties the host", async () => {
  serve({ jobs: okJobs([sessionJob("root")]) });
  const { host, teardown } = mount();
  await settle();
  assert.ok(host.querySelector(".console-plan-layout"));
  teardown();
  assert.equal(host.childElementCount, 0);
});

// ---- which source opens ----------------------------------------------------

// The rule, and the reason for it: jobs in hand means there is work to look at, which is the more
// specific answer to "what is happening here". Everything else belongs to the person doing plain
// work.
test("jobs in hand keep the Jobs view", async () => {
  serve({ jobs: okJobs([sessionJob("root")]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "jobs"), "true");
    assert.equal(pressed(host, "targets"), "false");
    assert.match(host.querySelector(".console-plan-list__item")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("an empty listing hands the view to the targets", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "targets"), "true");
    assert.equal(phase(host), "ready");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// The targets are still what opens when the job service refuses: a service that will not answer has
// no business taking the view over.
test("a refused job service hands the view to the targets too", async () => {
  serve({
    jobs: refusedJobs(403, "permission_denied", "job control is off"),
    plan: okPlan(RUN_BODY),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "targets"), "true");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// The auto rule answers a question once. After the reader has answered it themselves, a poll four
// seconds later must not overrule them - which is the bug the latch exists to prevent.
test("an explicit pick survives the poll", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "targets"), "true");
    pickSource(host, "jobs");
    await settle();
    assert.equal(pressed(host, "jobs"), "true");
    assert.match(text(host), /No jobs/);
  } finally {
    teardown();
  }
});

// ---- the targets -----------------------------------------------------------

test("the target plan draws one node per target and one list row per target", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
    assert.equal(host.querySelectorAll(".console-plan-list__item").length, 3);
    assert.equal(host.querySelectorAll(".console-plan-edge").length, 2);
    const states = [...host.querySelectorAll<SVGElement>(".console-plan-node")].map(
      (n) => n.dataset.state,
    );
    assert.deepEqual(states.sort(), ["idle", "pass", "running"]);
    // The whole node is labeled project:target, which is the id the contract gives it.
    assert.match(host.querySelector(".console-plan-list__id")?.textContent ?? "", /\.:generate/);
  } finally {
    teardown();
  }
});

// no_return belongs to jobs alone. An engine that resolved a DAG knows what happened to every node
// in it, so there is nothing here for that state to describe.
test("the target view invents no no-return, in the overview or on a node", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.doesNotMatch(summaryText(host), /no-return/);
    assert.equal(host.querySelectorAll('.console-plan-node[data-state="no_return"]').length, 0);
  } finally {
    teardown();
  }
});

// The first thing a reader needs is whether they are watching work happen or reading a record of
// work that finished, so the line leads with it.
test("the overview leads with how the target view is anchored", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      summaryText(host),
      "following the running ci. 3 targets. 1 running, 1 pass. 0 fail.",
    );
  } finally {
    teardown();
  }
});

test("a daemon with no target plan route names the missing endpoint", async () => {
  serve({ jobs: okJobs([]), plan: () => ({ ok: false, status: 404 }) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No target plan endpoint/);
    assert.match(text(host), /lights up when the daemon serves \/api\/v1\/plan/);
  } finally {
    teardown();
  }
});

// Served-but-empty on the FOLLOWING read means the daemon had nothing to anchor to. That is a
// different fact from a missing route, and the sentence has to say so.
test("a served but empty target plan says nothing has run, not that the route is missing", async () => {
  serve({ jobs: okJobs([]), plan: okPlan({ target: "ci", anchor: "default", nodes: [] }) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /Nothing has run here yet/);
    assert.doesNotMatch(text(host), /No target plan endpoint/);
  } finally {
    teardown();
  }
});

// ---- the target override ---------------------------------------------------

// The default read names NO target: the daemon picks the anchor and the view follows the live run.
// Sending one by accident would silently turn a live view into a browse of a fixed target.
test("the default read names no target, and the override is what adds one", async () => {
  const asked: string[] = [];
  serve({
    jobs: okJobs([]),
    plan: (url) => {
      asked.push(url);
      return okPlan(RUN_BODY)();
    },
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.deepEqual(asked, ["http://127.0.0.1:7391/api/v1/plan"]);
    const input = host.querySelector<HTMLInputElement>(".console-plan-target input");
    assert.ok(input, "the override is offered on the target view");
    if (input) {
      input.value = "build";
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }
    await settle();
    assert.equal(asked.at(-1), "http://127.0.0.1:7391/api/v1/plan?target=build");
  } finally {
    teardown();
  }
});

// The console holds no list of the workspace's targets, so any sentence it wrote itself would be a
// guess. The daemon named what it could not resolve; that is what reaches the screen. The body is
// what the route actually sends - http.Error, so plain text, not a JSON envelope.
test("an unknown target shows the daemon's own message, verbatim", async () => {
  serve({
    jobs: okJobs([]),
    plan: (url) =>
      url.includes("target=")
        ? {
            ok: false,
            status: 400,
            text: () =>
              Promise.resolve('unknown target "cli"; run `magus describe targets` to list them\n'),
          }
        : okPlan(RUN_BODY)(),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const input = host.querySelector<HTMLInputElement>(".console-plan-target input");
    if (input) {
      input.value = "cli";
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /unknown target "cli"; run `magus describe targets` to list them/);
  } finally {
    teardown();
  }
});

// An override that RESOLVES but covers nothing is not the same fact as nothing having run at all.
test("an override that matches no target gets its own sentence", async () => {
  serve({
    jobs: okJobs([]),
    plan: (url) =>
      url.includes("target=")
        ? okPlan({ target: "docs", anchor: "explicit", nodes: [] })()
        : okPlan(RUN_BODY)(),
  });
  const { host, teardown } = mount();
  try {
    await settle();
    const input = host.querySelector<HTMLInputElement>(".console-plan-target input");
    if (input) {
      input.value = "docs";
      input.dispatchEvent(new Event("change", { bubbles: true }));
    }
    await settle();
    assert.match(text(host), /No targets answer to docs here\./);
    assert.doesNotMatch(text(host), /Nothing has run here yet/);
  } finally {
    teardown();
  }
});

// The override is not the entry point, so it is not offered where it would mean nothing.
test("the target override belongs to the target view alone", async () => {
  serve({ jobs: okJobs([sessionJob("root")]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(host.querySelector<HTMLElement>(".console-plan-target")?.hidden, true);
    pickSource(host, "targets");
    await settle();
    assert.equal(host.querySelector<HTMLElement>(".console-plan-target")?.hidden, false);
  } finally {
    teardown();
  }
});

// ---- the detail sheet ------------------------------------------------------

test("selecting a target shows its project, its target and a link to its captured output", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail");
    assert.match(detail?.textContent ?? "", /generate/);
    assert.match(detail?.textContent ?? "", /out1a2b3c/);
    const link = detail?.querySelector("a");
    assert.match(link?.getAttribute("href") ?? "", /^\.\.\/logs\/#/);
    assert.match(
      link?.getAttribute("href") ?? "",
      /ref=out1a2b3c/,
      "the deep link carries the ref the log viewer opens",
    );
    assert.match(
      link?.getAttribute("href") ?? "",
      /port=7391/,
      "and this daemon's port, so the viewer re-attaches here rather than wherever it was last",
    );
  } finally {
    teardown();
  }
});

// A running target links to the run BEFORE this one (run.ts's RunPlanNode.ref). Wording that link
// as this run's log would send a reader looking for live output into a finished log without telling
// them - the one misreading on this view that a plausible-looking screen actively causes.
test("the output link is worded as the last log, never as this run's", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const items = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    items.find((r) => r.dataset.id === ".:build")?.click();
    const copy = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(copy, /Last output/);
    assert.match(copy, /Open the last log/);
    assert.match(copy, /out7g8h9i/, "a running node still carries the previous run's ref");
    // And the gap is stated in words on the one state where it is a whole run wide.
    assert.match(copy, /running now, so the log above is from its previous run/);
  } finally {
    teardown();
  }
});

// A node that has not run has nothing to open, and a dead link is worse than no link.
test("a target with no captured output offers no link", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const items = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    items.find((r) => r.dataset.id === "console:test")?.click();
    const detail = host.querySelector(".console-plan-detail");
    assert.match(detail?.textContent ?? "", /console/);
    assert.equal(detail?.querySelector("a"), null);
  } finally {
    teardown();
  }
});

test("the target drawing is hidden from assistive tech and the target list is its twin", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      host.querySelector(".console-plan-stage__svg")?.getAttribute("aria-hidden"),
      "true",
    );
    assert.equal(host.querySelector(".console-plan-tree")?.getAttribute("aria-label"), "Targets");
  } finally {
    teardown();
  }
});

// ---- one mount is not the other --------------------------------------------

// The race the two-source design makes reachable: the job read is slow, the reader gives up on it
// and switches to Targets, and the listing then answers. Its rows describe the OTHER tenant, so
// painting them here would put jobs - a no-return among them, the one state a resolved target plan
// can never contain - onto the target view, and the reader would have no way to tell.
test("a listing that answers after the switch to Targets cannot paint it", async () => {
  // Held open on purpose: this is the read that was already in flight when the reader switched.
  const gate: { release: (r: unknown) => void } = { release: () => undefined };
  const held = new Promise<unknown>((r) => {
    gate.release = r;
  });
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("JobService/ListJobs")) return held as Promise<Response>;
    if (url.includes("/api/v1/plan")) return Promise.resolve(okPlan(RUN_BODY)() as Response);
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;

  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "targets");
    await settle();
    assert.equal(pressed(host, "targets"), "true");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);

    gate.release({
      ok: true,
      status: 200,
      headers: JSON_HEADERS,
      json: () => Promise.resolve({ jobs: [sessionJob("root", { state: "no_return" })] }),
    });
    await settle();

    assert.equal(pressed(host, "targets"), "true", "a late listing must not take the view back");
    assert.equal(
      host.querySelectorAll('.console-plan-node[data-state="no_return"]').length,
      0,
      "the target view invents no no-return, and a late read from the other source cannot lend it one",
    );
    assert.doesNotMatch(summaryText(host), /no-return/);
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// Visibility is per PANE - the console calls it on each pane's own controller - so one pane going
// quiet says nothing about another. A module-wide switch fans every call out to every mount, which
// shows up as the pane that came back ALSO refreshing the one that had not.
test("hiding one pane leaves the other alone", async () => {
  let plans = 0;
  serve({
    jobs: okJobs([]),
    plan: () => {
      plans++;
      return okPlan(RUN_BODY)();
    },
  });
  const a = mount();
  const b = mount();
  try {
    await settle();
    a.instance.setVisible(false);
    await settle();
    const before = plans;
    b.instance.setVisible(false);
    b.instance.setVisible(true);
    await settle();
    assert.equal(plans - before, 1, "only the pane that came back reads again");
  } finally {
    a.teardown();
    b.teardown();
  }
});

// The command ids are shared by every mount, so unregistering them per teardown takes them away
// from a pane that is still on screen - with two panes open, closing either leaves the command bar
// with no Jobs commands at all.
test("closing one pane leaves the commands with the pane still open", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const a = mount();
  const b = mount();
  try {
    await settle();
    // What the console does when it focuses b's pane: the shared commands now act on b.
    b.instance.setVisible(true);
    a.teardown();

    assert.ok(
      listCommands().some((c) => c.id === "jobs.next"),
      "a Jobs pane is still open, so its commands are still registered",
    );
    assert.equal(dispatchCommand("jobs.next"), true);
    const items = [...b.host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    assert.equal(
      items[0]?.getAttribute("aria-current"),
      "true",
      "and the command acted on the pane the console made visible",
    );
  } finally {
    b.teardown();
  }
});

// And the last one out takes them with it, or the command bar keeps offering commands that dispatch
// into a torn-down view.
test("closing the last pane unregisters the commands", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { teardown } = mount();
  await settle();
  assert.ok(listCommands().some((c) => c.id === "jobs.refresh"));
  teardown();
  assert.equal(
    listCommands().some((c) => c.id.startsWith("jobs.")),
    false,
  );
});

// ---- repainting around the reader ------------------------------------------

// The detail sheet holds this view's only link, and the poll rebuilds the view every four seconds.
// Gating the sheet on the drawing's signature alone is not enough: the sheet draws from the SELECTED
// node, so it rebuilds on every tick and takes the focused anchor with it.
test("a repaint that changes nothing leaves the focused output link where it is", async () => {
  serve({ jobs: okJobs([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const items = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    items.find((r) => r.dataset.id === ".:build")?.click();
    const link = host.querySelector<HTMLAnchorElement>(".console-plan-detail a");
    assert.ok(link, "the running node offers its last log");
    link?.focus();
    assert.equal(document.activeElement, link);
    // The poll is what repaints this view; jobs.refresh drives the same path without waiting four
    // seconds for a timer. There is no Reload button to click - the view syncs on its own.
    dispatchCommand("jobs.refresh");
    await settle();
    assert.equal(
      document.activeElement,
      link,
      "a reader standing on the link must still be standing on it after a poll",
    );
  } finally {
    teardown();
  }
});

// The other half of the same gate: what a row SAYS is part of what decides a repaint. With meta left
// out of the signature, a job whose model changed under an unchanged state keeps drawing the old one.
test("a changed model repaints the row that carries it", async () => {
  let reads = 0;
  serve({
    jobs: () => {
      reads++;
      return okJobs([
        sessionJob("root", { state: "running", model: reads > 1 ? "sonnet" : "opus" }),
      ])();
    },
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.match(host.querySelector(".console-plan-list__meta")?.textContent ?? "", /opus/);
    dispatchCommand("jobs.refresh");
    await settle();
    assert.match(host.querySelector(".console-plan-list__meta")?.textContent ?? "", /sonnet/);
  } finally {
    teardown();
  }
});
