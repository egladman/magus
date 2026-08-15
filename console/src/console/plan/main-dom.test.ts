// main-dom.test.ts - the Plan surface's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// models it draws are covered next door in ledger.test.ts and run.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the four things a
// later refactor would most plausibly break:
//
//   - The EMPTY STATES stay apart. Per source: "no daemon", "the daemon has no route", and "the
//     plan is empty" are three different facts, and only the last means no work exists. This
//     branch's daemon serves NEITHER route, so the middle one is what everybody sees first - it has
//     to name the route rather than showing a blank plan.
//   - WHICH SOURCE OPENS is decided by the data. A ledger with rows means an orchestration is in
//     flight and Declared opens; anything else hands the surface to Run, which is what a person
//     doing plain work came for. An explicit pick then sticks - the poll must not overrule it.
//   - The DRAWING IS NOT THE ACCESSIBLE SURFACE. The stage is aria-hidden and the node list beside
//     it carries the same nodes with their states in words.
//   - FOCUS IS NEVER TAKEN. Mounting and polling must leave the caret where it was; only an
//     explicit navigation command moves it.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";

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

// serve points the surface at a daemon that answers only the routes named here. Everything else
// (the status RPC, the run feed, the route left out) is refused, which is what a daemon that is not
// there looks like from the browser, and which the surface must survive without blanking the plan.
function serve(routes: { ledger?: () => unknown; plan?: (url: string) => unknown }): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/api/v1/ledger") && routes.ledger) {
      return Promise.resolve(routes.ledger() as Response);
    }
    if (url.includes("/api/v1/plan") && routes.plan) {
      return Promise.resolve(routes.plan(url) as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

function serveLedger(reply: () => unknown): void {
  serve({ ledger: reply });
}

function ok(units: unknown[]): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve({ units }) });
}

// okPlan answers /api/v1/plan with a run-plan body in the contract's shape.
function okPlan(body: Record<string, unknown>): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve(body) });
}

// The run plan every drawing test reads: build waits on generate, test waits on build.
//
// .:build is RUNNING and still carries a ref, which is the wire's actual behaviour rather than a
// convenience: a node's ref is its most recent captured output regardless of state, so a running
// node hands back the PREVIOUS run's log. A fixture where running implied no ref would let the
// mislabelling this pins for pass unnoticed.
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

// mount builds the surface into a fresh host and hands back the teardown. EVERY caller must run it:
// activate starts a poll interval, and an interval nobody clears keeps the test process alive.
function mount(): { host: HTMLElement; teardown: () => void } {
  const host = document.createElement("div");
  document.body.append(host);
  return { host, teardown: activate(host) };
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

// The case every reader on this branch hits: the endpoint lands with the sibling branch, so until
// then the route 404s. A blank plan here would read as "nothing was delegated", which is the one
// wrong answer that costs something - they stop looking. Reached by asking for Declared, because a
// ledger with no rows is exactly the case that hands the surface to Run.
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
    // And the detail sheet is now reading that unit rather than the pick-one hint.
    assert.match(host.querySelector(".console-plan-detail")?.textContent ?? "", /root/);
  } finally {
    teardown();
  }
});

test("selecting a unit shows its goal, checkpoint, tier, validation and owned paths", async () => {
  serveLedger(
    ok([
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
  );
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
    // Nothing stamps a unit onto the activity feeds yet, and the surface says so rather than
    // leaving a blank that reads as "this unit ran nothing".
    assert.match(detail, /No runs are attributed to this unit/);
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

// This is the state of this branch: neither route is served. The run plan is still what opens,
// because a secondary endpoint that is missing has no business taking the surface over.
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
// guess. The daemon named what it could not resolve; that is what reaches the screen.
test("an unknown target shows the daemon's own message, verbatim", async () => {
  serve({
    ledger: ok([]),
    plan: (url) =>
      url.includes("target=")
        ? {
            ok: false,
            status: 400,
            text: () => Promise.resolve('{"error":"unknown target \\"cli\\"; did you mean ci?"}'),
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
    assert.match(text(host), /unknown target "cli"; did you mean ci\?/);
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

// A node's ref is its most recent captured output INDEPENDENT of its state, so a running target
// links to the run BEFORE this one. Wording that link as this run's log would send a reader looking
// for live output into a finished log without telling them - the one misreading on this surface
// that a plausible-looking screen actively causes.
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
