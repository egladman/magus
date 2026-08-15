// main-dom.test.ts - the Plan surface's mount. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests. The
// model it draws is covered next door in ledger.test.ts.
//
// What is pinned HERE is what a reader ends up looking at, and specifically the three things a
// later refactor would most plausibly break:
//
//   - The EMPTY STATES stay apart. "No daemon", "the daemon has no ledger route", and "the plan is
//     empty" are three different facts, and only one of them means nothing was delegated. This
//     branch's daemon serves no /api/v1/ledger at all, so the middle one is what everybody sees
//     first - it has to name the route rather than showing a blank plan.
//   - The DRAWING IS NOT THE ACCESSIBLE SURFACE. The stage is aria-hidden and the unit list beside
//     it carries the same units with their states in words.
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

// serveLedger points the surface at a daemon whose ONLY answer is the ledger response given here.
// Every other request (the status RPC, the run feed) is refused, which is what a daemon that is not
// there looks like from the browser, and which the surface must survive without blanking the plan.
function serveLedger(reply: () => unknown): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("/api/v1/ledger")) return Promise.resolve(reply() as Response);
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

function ok(units: unknown[]): () => unknown {
  return () => ({ ok: true, status: 200, json: () => Promise.resolve({ units }) });
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
// wrong answer that costs something - they stop looking.
test("a daemon with no ledger route names the missing endpoint", async () => {
  serveLedger(() => ({ ok: false, status: 404 }));
  const { host, teardown } = mount();
  try {
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
