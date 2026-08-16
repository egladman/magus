// main-dom.test.ts - the Plan surface's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// models it draws are covered next door in ledger.test.ts and run.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the things a later
// refactor would most plausibly break:
//
//   - The EMPTY STATES stay apart. Per source: "no daemon", "the daemon has no route", and "the
//     plan is empty" are three different facts, and only the last means no work exists. The middle
//     one is what a reader meets on any daemon predating the route, and it has to name the route
//     rather than showing a blank plan.
//   - WHICH SOURCE OPENS is decided by the data. A ledger with rows means an orchestration is in
//     flight and Declared opens; anything else hands the surface to Run, which is what a person
//     doing plain work came for. An explicit pick then sticks - the poll must not overrule it.
//   - The DRAWING IS NOT THE ACCESSIBLE SURFACE. The stage is aria-hidden and the node list beside
//     it carries the same nodes with their states in words.
//   - FOCUS IS NEVER TAKEN. Mounting and polling must leave the caret where it was; only an
//     explicit navigation command moves it. That includes a repaint: a poll that returns the same
//     plan must not rebuild the element a reader is standing on.
//   - A MOUNT IS ITS OWN SURFACE. Two panes can hold two Plan surfaces, so a read that answers late
//     must not paint over a source the reader has since switched, hiding one pane must not silence
//     the other, and closing one must not take the shared commands away from the one still open.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { dispatchCommand, listCommands } from "../commands";
import { activate, type PlanInstance } from "./main";

const HOST = "127.0.0.1:7391";

const realFetch = globalThis.fetch;

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
});

// settle lets the two awaited reads in refresh() resolve. Turns rather than a delay: everything
// under test resolves on the microtask/macrotask queue, and a fixed sleep would be either flaky or
// slow.
async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// serve points the surface at a daemon that answers only the routes named here. Everything else is
// refused, which is what a daemon that is not there looks like from the browser, and which the
// surface must survive without blanking the plan.
function serve(routes: {
  ledger?: () => unknown;
  plan?: (url: string) => unknown;
  feeds?: boolean; // answer BOTH activity feeds - the status RPC and the run descriptors
}): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/api/v1/ledger") && routes.ledger) {
      return Promise.resolve(routes.ledger() as Response);
    }
    if (url.includes("/api/v1/plan") && routes.plan) {
      return Promise.resolve(routes.plan(url) as Response);
    }
    if (routes.feeds && url.includes("StatusService")) {
      // A Connect unary response in the transport's JSON codec, carrying an EMPTY frame: what these
      // tests need from the status read is only that it SUCCEEDED, so there is no frame to build.
      return Promise.resolve({
        ok: true,
        status: 200,
        headers: new Headers({ "content-type": "application/json" }),
        json: () => Promise.resolve({}),
      } as unknown as Response);
    }
    if (routes.feeds && url.includes("/api/v1/outputs")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        json: () => Promise.resolve({ outputs: [] }),
      } as unknown as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

function serveLedger(reply: () => unknown): void {
  serve({ ledger: reply });
}

function ok(units: unknown[], overlaps: unknown[] = []): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve({ units, overlaps }) });
}

// secondsAgo builds the unix-second stamp a row carries, so a test can put a row far enough in the
// past to be stale without reaching for fake timers.
function secondsAgo(secs: number): number {
  return Math.floor(Date.now() / 1000) - secs;
}

// okPlan answers /api/v1/plan with a run-plan body in the contract's shape.
function okPlan(body: Record<string, unknown>): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

// The run plan every drawing test reads: build waits on generate, test waits on build.
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
function pickSource(host: HTMLElement, source: "declared" | "run"): void {
  host.querySelector<HTMLElement>(`.console-plan-source [data-source="${source}"]`)?.click();
}

function pressed(host: HTMLElement, source: "declared" | "run"): string {
  return (
    host
      .querySelector(`.console-plan-source [data-source="${source}"]`)
      ?.getAttribute("aria-pressed") ?? ""
  );
}

function summaryText(host: HTMLElement): string {
  return host.querySelector(".console-plan-summary")?.textContent ?? "";
}

// mount builds the surface into a fresh host and hands back its controller. EVERY caller must run
// the teardown: activate starts a poll interval, and an interval nobody clears keeps the test
// process alive.
function mount(): { host: HTMLElement; instance: PlanInstance; teardown: () => void } {
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

test("with no daemon configured it says that, rather than showing an empty plan", async () => {
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No daemon connected/);
  } finally {
    teardown();
  }
});

// What a reader meets against any daemon predating the delegation ledger. A blank plan here would
// read as "nothing was delegated", which is the one wrong answer that costs something: they stop
// looking. Reached by asking for Declared, because a ledger with no rows is exactly the case that
// hands the surface to Run.
test("a daemon with no ledger route names the missing endpoint", async () => {
  serveLedger(() => ({ ok: false, status: 404 }));
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "declared");
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No delegation ledger endpoint/);
    assert.match(text(host), /lights up when the daemon serves \/api\/v1\/ledger/);
  } finally {
    teardown();
  }
});

test("a served but empty ledger is a different sentence from a missing one", async () => {
  serveLedger(ok([]));
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "declared");
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /Nothing delegated/);
    assert.doesNotMatch(text(host), /No delegation ledger endpoint/);
  } finally {
    teardown();
  }
});

test("a read that fails for any other reason blames neither the plan nor the daemon alone", async () => {
  serveLedger(() => ({ ok: false, status: 500 }));
  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "declared");
    await settle();
    assert.match(text(host), /Could not read the delegation ledger/);
    assert.match(text(host), /HTTP 500/);
  } finally {
    teardown();
  }
});

test("a plan draws one node per unit and one list row per unit", async () => {
  serveLedger(
    ok([
      { id: "root", state: "running", goal: "ship it" },
      { id: "b1", parent: "root", state: "pass" },
      { id: "b2", parent: "root", state: "no_return", depends_on: ["b1"], read_only: true },
    ]),
  );
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

test("the overview line is the polite live region; the unit list is not", async () => {
  serveLedger(ok([{ id: "root", state: "no_return" }]));
  const { host, teardown } = mount();
  try {
    await settle();
    const summary = host.querySelector(".console-plan-summary");
    assert.equal(summary?.getAttribute("aria-live"), "polite");
    assert.match(summary?.textContent ?? "", /1 unit - 1 no-return/);
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
test("the drawing is hidden from assistive tech and the unit list is its twin", async () => {
  serveLedger(ok([{ id: "root" }]));
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      host.querySelector(".console-plan-stage__svg")?.getAttribute("aria-hidden"),
      "true",
    );
    const nav = host.querySelector(".console-plan-tree");
    assert.equal(nav?.getAttribute("aria-label"), "Delegation units");
    assert.match(host.querySelector(".console-plan-list__item")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("mounting does not move focus", async () => {
  serveLedger(ok([{ id: "root" }]));
  const elsewhere = document.createElement("input");
  document.body.append(elsewhere);
  const { host, teardown } = mount();
  try {
    elsewhere.focus();
    await settle();
    assert.equal(
      document.activeElement,
      elsewhere,
      "a surface that repaints on a timer must never pull the caret out of what someone is doing",
    );
  } finally {
    teardown();
  }
});

// Navigation is the ONE thing allowed to move focus, because that is what the reader asked for.
test("the next-unit key selects a unit and focuses its row", async () => {
  serveLedger(ok([{ id: "root" }, { id: "b1", parent: "root" }]));
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

test("selecting a unit shows its goal, checkpoint, tier, validation and owned paths", async () => {
  serve({
    ledger: ok([
      {
        id: "root",
        goal: "render the ledger",
        checkpoint: "after the stage lands",
        tier: "opus",
        validation: "console:test",
        owned_paths: ["console/src/console/plan/"],
        forbidden_paths: ["internal/"],
      },
    ]),
    feeds: true,
  });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /render the ledger/);
    assert.match(detail, /after the stage lands/);
    assert.match(detail, /opus/);
    assert.match(detail, /console:test/);
    assert.match(detail, /console\/src\/console\/plan\//);
    assert.match(detail, /internal\//);
    // Both feeds answered and carried nothing for this unit: nothing stamps a unit onto them yet,
    // and the surface says so rather than leaving a blank that reads as "this unit ran nothing".
    assert.match(detail, /No runs are attributed to this unit/);
  } finally {
    teardown();
  }
});

// The other half of that sentence, and the reason it is two sentences. Feeds that did not answer
// cannot attribute a run to ANY unit, which is a fact about the daemon; reporting it as the one
// above would blame the ledger for a daemon that is not talking.
test("a unit whose activity feeds could not be read says that instead", async () => {
  serveLedger(ok([{ id: "root" }]));
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /The activity feeds could not be read/);
    assert.match(detail, /stub: no network/, "and it carries what actually went wrong");
    assert.doesNotMatch(detail, /No runs are attributed to this unit/);
  } finally {
    teardown();
  }
});

// ---- what the ledger reports about itself ----------------------------------

// Two units claiming one path is a FACT the route derived from two rows an agent wrote, and the
// surface's job is to put it in front of a reader. Nothing is blocked, reordered, or failed by it -
// and it is drawn on BOTH rows, because either one is where the reader might be standing.
test("an overlap warns on both rows and names the other unit in the detail", async () => {
  serve({
    ledger: ok(
      [
        { id: "a", state: "running", owned_paths: ["internal/ledger"] },
        { id: "b", state: "declared", owned_paths: ["internal/ledger/store.go"] },
        { id: "c", state: "pass" },
      ],
      [{ a: "a", b: "b", paths: ["internal/ledger", "internal/ledger/store.go"] }],
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
      "the unit with no reported overlap carries no warning",
    );
    // In words, not in colour alone.
    assert.match(warned[0]?.textContent ?? "", /overlap/);
    assert.equal(host.querySelectorAll(".console-plan-node[data-warn]").length, 2);

    host.querySelector<HTMLElement>('.console-plan-list__item[data-id="a"]')?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /Overlaps/);
    assert.match(detail, /b: internal\/ledger, internal\/ledger\/store\.go/);
  } finally {
    teardown();
  }
});

// The heartbeat. A unit re-puts its row on every state change, so a running row nobody has touched
// in a long time is worth pointing at - as a question for the reader, never as a state the store
// invented for them.
test("a running row nobody has touched reads as stale; a fresh one just reads its age", async () => {
  // Minutes and hours rather than seconds: the age is asserted verbatim, and a fixture a few
  // seconds old would tick over into the next second while the surface was still settling.
  serveLedger(
    ok([
      { id: "fresh", state: "running", updated: secondsAgo(120) },
      { id: "quiet", state: "running", updated: secondsAgo(3600) },
      { id: "done", state: "pass", updated: secondsAgo(3600) },
    ]),
  );
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
    // A finished unit is not going to be touched again, so an age beside it would read as a
    // problem where there is none.
    assert.equal(age("done")?.textContent, "");
  } finally {
    teardown();
  }
});

// What the next agent inherits. A release with no digest would leave a waiting unit unable to tell
// whether it is starting from the tree the releaser left.
test("a released path shows with a short digest", async () => {
  serve({
    ledger: ok([
      {
        id: "a",
        state: "running",
        owned_paths: ["docs"],
        releases: [
          {
            path: "internal/ledger/store.go",
            digest: "sha256:1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b",
            released_at: secondsAgo(60),
          },
          { path: "gone.go", digest: "absent", released_at: secondsAgo(60) },
        ],
      },
    ]),
    feeds: true,
  });
  const { host, teardown } = mount();
  try {
    await settle();
    host.querySelector<HTMLElement>(".console-plan-list__item")?.click();
    const detail = host.querySelector(".console-plan-detail")?.textContent ?? "";
    assert.match(detail, /Released/);
    assert.match(detail, /internal\/ledger\/store\.go/);
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
  serveLedger(ok([{ id: "root" }]));
  const { host, teardown } = mount();
  await settle();
  assert.ok(host.querySelector(".console-plan-layout"));
  teardown();
  assert.equal(host.childElementCount, 0);
});

// ---- which source opens ----------------------------------------------------

// The rule, and the reason for it: a ledger with rows means an orchestration is in flight, which is
// the more specific answer to "what is happening here". Everything else belongs to the person doing
// plain work.
test("a ledger with rows keeps the declared view", async () => {
  serve({ ledger: ok([{ id: "root" }]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "declared"), "true");
    assert.equal(pressed(host, "run"), "false");
    assert.match(host.querySelector(".console-plan-list__item")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("an empty ledger hands the surface to the run plan", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "run"), "true");
    assert.equal(phase(host), "ready");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// The run plan is still what opens when the ledger route is missing: a secondary endpoint that is
// not there has no business taking the surface over.
test("a ledger route that is not there hands the surface to the run plan too", async () => {
  serve({ ledger: () => ({ ok: false, status: 404 }), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "run"), "true");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// The auto rule answers a question once. After the reader has answered it themselves, a poll four
// seconds later must not overrule them - which is the bug the latch exists to prevent.
test("an explicit pick survives the poll", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(pressed(host, "run"), "true");
    pickSource(host, "declared");
    await settle();
    assert.equal(pressed(host, "declared"), "true");
    assert.match(text(host), /Nothing delegated/);
  } finally {
    teardown();
  }
});

// ---- the run plan ----------------------------------------------------------

test("the run plan draws one node per target and one list row per target", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
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
    // The whole node is labelled project:target, which is the id the contract gives it.
    assert.match(host.querySelector(".console-plan-list__id")?.textContent ?? "", /\.:generate/);
  } finally {
    teardown();
  }
});

// no_return belongs to the delegation ledger alone. An engine that resolved a DAG knows what
// happened to every node in it, so there is nothing here for that state to describe.
test("the run view invents no no-return, in the overview or on a node", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
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
test("the overview leads with how the view is anchored", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      summaryText(host),
      "following the running ci - 3 targets - 1 running, 1 pass - 0 fail",
    );
    assert.equal(
      host.querySelector(".console-plan-summary")?.getAttribute("aria-live"),
      "polite",
      "the overview is the live region; the list rebuilt on a four-second poll is not",
    );
  } finally {
    teardown();
  }
});

test("a daemon with no run plan route names the missing endpoint", async () => {
  serve({ ledger: ok([]), plan: () => ({ ok: false, status: 404 }) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /No run plan endpoint/);
    assert.match(text(host), /lights up when the daemon serves \/api\/v1\/plan/);
  } finally {
    teardown();
  }
});

// Served-but-empty on the FOLLOWING read means the daemon had nothing to anchor to. That is a
// different fact from a missing route, and the sentence has to say so.
test("a served but empty run plan says nothing has run, not that the route is missing", async () => {
  serve({ ledger: ok([]), plan: okPlan({ target: "ci", anchor: "default", nodes: [] }) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(phase(host), "empty");
    assert.match(text(host), /Nothing has run here yet/);
    assert.doesNotMatch(text(host), /No run plan endpoint/);
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
    ledger: ok([]),
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
    assert.ok(input, "the override is offered in run mode");
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
    ledger: ok([]),
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
    ledger: ok([]),
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
test("the target override belongs to the run view alone", async () => {
  serve({ ledger: ok([{ id: "root" }]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(host.querySelector<HTMLElement>(".console-plan-target")?.hidden, true);
    pickSource(host, "run");
    await settle();
    assert.equal(host.querySelector<HTMLElement>(".console-plan-target")?.hidden, false);
  } finally {
    teardown();
  }
});

// ---- the detail sheet ------------------------------------------------------

test("selecting a target shows its project, its target and a link to its captured output", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
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
// them - the one misreading on this surface that a plausible-looking screen actively causes.
test("the output link is worded as the last log, never as this run's", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const rows = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    rows.find((r) => r.dataset.id === ".:build")?.click();
    const detail = host.querySelector(".console-plan-detail");
    const copy = detail?.textContent ?? "";
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
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const rows = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    rows.find((r) => r.dataset.id === "console:test")?.click();
    const detail = host.querySelector(".console-plan-detail");
    assert.match(detail?.textContent ?? "", /console/);
    assert.equal(detail?.querySelector("a"), null);
    assert.doesNotMatch(detail?.textContent ?? "", /Open in log viewer/);
  } finally {
    teardown();
  }
});

test("the run drawing is hidden from assistive tech and the target list is its twin", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.equal(
      host.querySelector(".console-plan-stage__svg")?.getAttribute("aria-hidden"),
      "true",
    );
    assert.equal(
      host.querySelector(".console-plan-tree")?.getAttribute("aria-label"),
      "Plan targets",
    );
  } finally {
    teardown();
  }
});

// ---- one mount is not the other --------------------------------------------

// The race the two-source design makes reachable: the ledger read is slow, the reader gives up on it
// and switches to Run, and the ledger then answers. Its rows describe the OTHER tenant, so painting
// them here would put declared nodes - a no-return among them, the one state a resolved run plan can
// never contain - onto the run view, and the reader would have no way to tell.
test("a ledger that answers after the switch to Run cannot paint the run view", async () => {
  // Held open on purpose: this is the read that was already in flight when the reader switched.
  const ledgerGate: { release: (r: unknown) => void } = { release: () => undefined };
  const heldLedger = new Promise<unknown>((r) => {
    ledgerGate.release = r;
  });
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/api/v1/ledger")) return heldLedger as Promise<Response>;
    if (url.includes("/api/v1/plan")) return Promise.resolve(okPlan(RUN_BODY)() as Response);
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;

  const { host, teardown } = mount();
  try {
    await settle();
    pickSource(host, "run");
    await settle();
    assert.equal(pressed(host, "run"), "true");
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);

    ledgerGate.release({
      ok: true,
      status: 200,
      json: () => Promise.resolve({ units: [{ id: "root", state: "no_return" }] }),
    });
    await settle();

    assert.equal(pressed(host, "run"), "true", "a late ledger must not take the surface back");
    assert.equal(
      host.querySelectorAll('.console-plan-node[data-state="no_return"]').length,
      0,
      "the run view invents no no-return, and a late read from the other source cannot lend it one",
    );
    assert.doesNotMatch(summaryText(host), /no-return/);
    assert.equal(host.querySelectorAll(".console-plan-node").length, 3);
  } finally {
    teardown();
  }
});

// Visibility is per PANE - the console calls it on each pane's own controller - so one Plan pane
// going quiet says nothing about another. A module-wide switch fans every call out to every mount,
// which shows up as the pane that came back ALSO refreshing the one that had not.
test("hiding one Plan pane leaves the other alone", async () => {
  let plans = 0;
  serve({
    ledger: ok([]),
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
    assert.equal(plans - before, 1, "only the pane that came back reads the plan again");
  } finally {
    a.teardown();
    b.teardown();
  }
});

// The command ids are shared by every mount, so unregistering them per teardown takes them away
// from a pane that is still on screen - with two Plan panes open, closing either leaves the command
// bar with no Plan commands at all.
test("closing one Plan pane leaves the commands with the pane still open", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const a = mount();
  const b = mount();
  try {
    await settle();
    // What the console does when it focuses b's pane: the shared commands now act on b.
    b.instance.setVisible(true);
    a.teardown();

    assert.ok(
      listCommands().some((c) => c.id === "plan.unit.next"),
      "a Plan pane is still open, so its commands are still registered",
    );
    assert.equal(dispatchCommand("plan.unit.next"), true);
    const rows = [...b.host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    assert.equal(
      rows[0]?.getAttribute("aria-current"),
      "true",
      "and the command acted on the pane the console made visible",
    );
  } finally {
    b.teardown();
  }
});

// And the last one out takes them with it, or the command bar keeps offering Plan commands that
// dispatch into a torn-down surface.
test("closing the last Plan pane unregisters the commands", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { teardown } = mount();
  await settle();
  assert.ok(listCommands().some((c) => c.id === "plan.refresh"));
  teardown();
  assert.equal(
    listCommands().some((c) => c.id.startsWith("plan.")),
    false,
  );
});

// ---- repainting around the reader ------------------------------------------

// The detail sheet holds this surface's only link, and the poll rebuilds the surface every four
// seconds. Gating the sheet on the plan's signature alone is not enough: the sheet draws from the
// SELECTED node, so it rebuilds on every tick and takes the focused anchor with it.
test("a repaint that changes nothing leaves the focused output link where it is", async () => {
  serve({ ledger: ok([]), plan: okPlan(RUN_BODY) });
  const { host, teardown } = mount();
  try {
    await settle();
    const rows = [...host.querySelectorAll<HTMLElement>(".console-plan-list__item")];
    rows.find((r) => r.dataset.id === ".:build")?.click();
    const link = host.querySelector<HTMLAnchorElement>(".console-plan-detail a");
    assert.ok(link, "the running node offers its last log");
    link?.focus();
    assert.equal(document.activeElement, link);
    host.querySelector<HTMLElement>(".console-plan-refresh")?.click();
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
// out of the signature, a unit whose tier changed under an unchanged state keeps drawing the old one.
test("a changed tier repaints the row that carries it", async () => {
  let reads = 0;
  serve({
    ledger: () => {
      reads++;
      return ok([{ id: "root", state: "running", tier: reads > 1 ? "sonnet" : "opus" }])();
    },
  });
  const { host, teardown } = mount();
  try {
    await settle();
    assert.match(host.querySelector(".console-plan-list__meta")?.textContent ?? "", /opus/);
    host.querySelector<HTMLElement>(".console-plan-refresh")?.click();
    await settle();
    assert.match(host.querySelector(".console-plan-list__meta")?.textContent ?? "", /sonnet/);
  } finally {
    teardown();
  }
});
