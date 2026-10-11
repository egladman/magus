// attention-dom.test.ts - the attention hero's failure readout: which targets are failing, what you
// can do about each one, and the commands offered for doing it.
//
// These are pinned by test rather than by looking at the board because a failure is INTERMITTENT in
// the demo feed - it depends on a flaky target being scheduled and then losing a coin flip - so
// "open it and check" verifies nothing on any particular run. The commands especially: a wrong
// `magus run` line is copied, pasted into a terminal, and fails there, which is worse than not
// offering one.

import assert from "node:assert/strict";
import { test } from "node:test";
import {
  attentionTile,
  countFailing,
  failingTargets,
  inspectCommand,
  reproduceCommand,
  attentionVerdict,
  verdictStatus,
} from "./attention";
import type { AttentionRequest } from "./attentionQueue";
import { initialState, type StatusView } from "../state";

// request builds one open queue row. Only opened_ms varies across these tests; the rest satisfy
// the wire shape.
function request(openedMs: number): AttentionRequest {
  return {
    id: "att-0123456789ab",
    invocation: "inv-1",
    opened_ms: openedMs,
    outcome: "waiting",
    severity: "",
    source: "harness/Notification",
    where: "/repo",
    lease: "",
    files: [],
    message: "needs the deploy key",
  };
}

// statusWith builds the minimum StatusView the hero reads. Only the fields under test are
// meaningful; the rest satisfy the type.
function statusWith(targets: { label: string; state: string; ref?: string }[]): StatusView {
  return {
    health: { label: "healthy", cls: "ok" },
    pool: { capacity: 8, running: 1, queued: 0 },
    cache: { hits: 0, misses: 0, errors: 0, hitRate: null, sizeBytes: 0 },
    runningTargets: [],
    runs: [
      {
        inv: "inv123",
        trigger: "ci",
        targets: targets.map((t) => ({
          project: t.label.includes(":") ? t.label.split(":")[0] : "",
          target: t.label.includes(":") ? t.label.split(":")[1] : t.label,
          label: t.label,
          state: t.state as never,
          terminal: t.state !== "running" && t.state !== "queued",
          startMs: 1,
          endMs: 2,
          outputRef: t.ref ?? "",
          durationMs: 1,
        })),
      },
    ],
    workspaces: [],
    services: [],
    locks: [],
    magusVersion: "",
    ownerVersion: "",
    broker: null,
    server: null,
    brokerPolicy: "best-effort",
  };
}

test("failingTargets names every failure and carries its handles", () => {
  const s = statusWith([
    { label: "svc/api:test", state: "failed", ref: "out1a2b" },
    { label: "web/app:build", state: "passed", ref: "out9z9z" },
    { label: "lib/core:test", state: "failed" }, // failed before producing a ref
  ]);
  const failing = failingTargets(s);
  assert.deepEqual(failing, [
    { label: "svc/api:test", inv: "inv123", outputRef: "out1a2b" },
    { label: "lib/core:test", inv: "inv123", outputRef: "" },
  ]);
  // The list and the count must never disagree - the count is what the reader trusts, and a list
  // shorter than it reads as "these are all of them".
  assert.equal(failing.length, countFailing(s));
});

test("a target that failed without an output ref is still listed", () => {
  // Dropping it would make the count say two and the list show one, with nothing saying which was
  // withheld. Its run id is the fallback handle.
  const s = statusWith([{ label: "lib/core:test", state: "failed" }]);
  assert.equal(failingTargets(s)[0].inv, "inv123");
});

test("reproduceCommand puts the target first and the project second", () => {
  // magus's own argument order: `magus run <target> [<project>]`. Reversed, the command fails in
  // the terminal the reader pasted it into, which is worse than offering nothing.
  assert.equal(reproduceCommand("svc/api:test"), "magus run test svc/api");
  assert.equal(reproduceCommand("web/app:build"), "magus run build web/app");
});

test("reproduceCommand omits the project for a root target", () => {
  // The root project renders as "." in a label, but `magus run lint .` is noise next to
  // `magus run lint`, and a bare target has no project part at all.
  assert.equal(reproduceCommand(".:lint"), "magus run lint");
  assert.equal(reproduceCommand("lint"), "magus run lint");
});

test("inspectCommand is empty when there is no ref to fetch", () => {
  assert.equal(inspectCommand("out1a2b"), "magus query output out1a2b");
  assert.equal(inspectCommand(""), "", "an unfetchable ref must not become an offered command");
});

// The verdict reads the attention QUEUE and nothing else. This is the regression the tile was
// rewritten for: it used to derive "Attention needed" from the failing count, so the board could
// shout over an empty queue, or read "All clear" while agents sat blocked. Two things called
// attention on one screen, and no way to tell which one was lying.
test("the verdict comes from the queue, never from failing targets", () => {
  const broken = statusWith([{ label: "svc/api:test", state: "failed" }]);
  assert.equal(failingTargets(broken).length, 1, "the run really is failing");
  // A failing target is not a request. Nobody has been asked for anything, so nobody is waiting.
  assert.equal(attentionVerdict({ kind: "ok", requests: [], store: "/s" }).state, "clear");

  // ...and a request waiting is attention even with a perfectly green board.
  const clear = statusWith([{ label: "svc/api:test", state: "passed" }]);
  assert.equal(countFailing(clear), 0);
  const waiting = attentionVerdict({ kind: "ok", requests: [request(0)], store: "/s" });
  assert.equal(waiting.state, "attention");
  assert.equal(waiting.line, "1 request waiting");
});

// An unknown queue must never render as a calm one. "absent" and "unreadable" are the two reads
// where the tile does not KNOW whether anyone is blocked, and showing the good state for either is
// indistinguishable from nobody waiting - which is the one thing this tile must not say by mistake.
test("a queue that could not be read does not read as an empty one", () => {
  assert.equal(attentionVerdict({ kind: "absent" }).state, "warn");
  assert.equal(attentionVerdict({ kind: "unreadable", detail: "boom" }).state, "warn");
  assert.notEqual(
    attentionVerdict({ kind: "absent" }).line,
    attentionVerdict({ kind: "ok", requests: [], store: "/s" }).line,
  );
});

test("the verdict names how long the oldest request has waited", () => {
  const now = 10 * 60 * 1000;
  const v = attentionVerdict(
    { kind: "ok", requests: [request(now - 5 * 60 * 1000)], store: "/s" },
    now,
  );
  assert.match(v.sub, /waiting 5m/);
});

// The headline's mark. A calm queue beside a failing count must not take the success colour: green
// on "Nobody waiting" with a red 1 next to it is the board contradicting itself.
test("a calm queue is only marked successful while nothing is failing", () => {
  assert.equal(verdictStatus("clear", 0), "success");
  assert.equal(verdictStatus("clear", 2), "neutral");
  assert.equal(verdictStatus("warn", 0), "warning");
  assert.equal(verdictStatus("attention", 0), "danger");
});

// frame builds the state the hero repaints from. The demo connection keeps the queue read local, so
// the tile never touches the network.
function frame(targets: { label: string; state: string; ref?: string }[]) {
  return { ...initialState(), status: statusWith(targets), conn: { state: "demo" as const } };
}

test("the hero's verdict is a heading with an icon, and goes neutral beside a failure", () => {
  const tile = attentionTile();
  document.body.append(tile.el);
  tile.update(frame([{ label: "svc/api:test", state: "passed" }]));
  const verdict = tile.el.querySelector(".console-dashboard-hero__verdict");
  assert.equal(verdict?.tagName, "H3");
  const icon = (): string | null =>
    tile.el.querySelector(".console-dashboard-hero__mark .pf-v6-c-icon__content")?.className ??
    null;
  assert.match(icon() ?? "", /pf-m-success/);
  assert.equal(tile.el.dataset.failing, "none");

  tile.update(frame([{ label: "svc/api:test", state: "failed", ref: "out1" }]));
  assert.equal(tile.el.dataset.failing, "some");
  assert.doesNotMatch(icon() ?? "", /pf-m-success/, "no success mark beside a failing count");
  assert.equal(tile.el.dataset.state, "clear", "the verdict itself is still the queue's");
  tile.destroy();
  tile.el.remove();
});

test("why work is queued is text under the counts, not a tooltip", () => {
  const tile = attentionTile();
  const status = statusWith([{ label: "svc/api:test", state: "passed" }]);
  status.pool = { capacity: 2, running: 2, queued: 3 };
  tile.update({ ...initialState(), status, conn: { state: "demo" as const } });
  const reason = tile.el.querySelector<HTMLElement>(".console-dashboard-hero__reason");
  assert.equal(reason?.hidden, false);
  assert.match(reason?.textContent ?? "", /3 waiting: every one of the pool's 2 slots is busy/);
  assert.equal(tile.el.querySelector("[title]"), null, "nothing here lives only in a tooltip");
  tile.destroy();
});

test("a failing chip's menu is not rebuilt by the next identical frame", () => {
  const tile = attentionTile();
  document.body.append(tile.el);
  const failing = [{ label: "svc/api:test", state: "failed", ref: "out1" }];
  tile.update(frame(failing));
  const button = tile.el.querySelector<HTMLButtonElement>(".console-dashboard-hero__failbtn");
  assert.ok(button, "a failing target gets a chip");
  button.click();
  assert.equal(button.getAttribute("aria-expanded"), "true");

  // Frames arrive about once a second. Rebuilding the list on each one closed the menu a reader
  // had just opened.
  tile.update(frame(failing));
  assert.equal(tile.el.querySelector(".console-dashboard-hero__failbtn"), button);
  assert.equal(button.getAttribute("aria-expanded"), "true");

  const first = tile.el.querySelector<HTMLElement>('[role="menuitem"]');
  first?.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert.equal(button.getAttribute("aria-expanded"), "false");
  tile.destroy();
  tile.el.remove();
});

// A queue with one waiting request, served the way the route serves it.
async function withQueue(body: () => Promise<void>): Promise<void> {
  const real = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({
        store: "/s",
        requests: [
          {
            id: "att-0123456789ab",
            opened_ms: Date.now() - 5000,
            outcome: "waiting",
            message: "needs the deploy key\nthen a long second line the row has to shorten",
          },
        ],
      }),
      { status: 200 },
    )) as typeof fetch;
  try {
    await body();
  } finally {
    globalThis.fetch = real;
  }
}

test("closing a request: the reason's instructions are helper text, and Escape gives focus back", async () => {
  await withQueue(async () => {
    const tile = attentionTile();
    document.body.append(tile.el);
    tile.update({
      ...initialState(),
      status: statusWith([]),
      liveHost: "127.0.0.1:7391",
      conn: { state: "connected" as const },
    });
    await new Promise((r) => setTimeout(r, 20));
    const close = tile.el.querySelector<HTMLButtonElement>(
      ".console-dashboard-attention__disposebtn",
    );
    assert.ok(close, "a waiting request carries its close control from the start");
    assert.equal(close.textContent, "Close request", "named for what it does, not 'Dispose'");
    assert.match(close.getAttribute("aria-label") ?? "", /att-0123456789ab/);

    close.click();
    const input = tile.el.querySelector<HTMLInputElement>(".pf-v6-c-form-control__text");
    assert.ok(input);
    assert.doesNotMatch(input.placeholder, /Enter|Esc/, "instructions are not in the placeholder");
    const help = document.getElementById(input.getAttribute("aria-describedby") ?? "");
    assert.match(help?.textContent ?? "", /Enter closes the request, Escape cancels/);

    input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(tile.el.querySelector(".console-dashboard-attention__composer"), null);
    assert.equal(document.activeElement, close, "focus returns to the control that opened it");
    tile.destroy();
    tile.el.remove();
  });
});

test("a shortened message can be opened in full, as a control and not a tooltip", async () => {
  await withQueue(async () => {
    const tile = attentionTile();
    document.body.append(tile.el);
    tile.update({
      ...initialState(),
      status: statusWith([]),
      liveHost: "127.0.0.1:7391",
      conn: { state: "connected" as const },
    });
    await new Promise((r) => setTimeout(r, 20));
    const message = tile.el.querySelector<HTMLElement>(".console-dashboard-attention__message");
    assert.equal(message?.textContent, "needs the deploy key");
    assert.equal(message?.getAttribute("title"), null);
    const more = tile.el.querySelector<HTMLButtonElement>(".console-dashboard-attention__more");
    assert.equal(more?.getAttribute("aria-expanded"), "false");
    more?.click();
    assert.match(message?.textContent ?? "", /long second line/);
    assert.equal(more?.getAttribute("aria-expanded"), "true");
    tile.destroy();
    tile.el.remove();
  });
});

test("the chip is a button with a popup that names the failure in words", () => {
  const tile = attentionTile();
  tile.update(frame([{ label: "svc/api:test", state: "failed" }]));
  const button = tile.el.querySelector<HTMLButtonElement>(".console-dashboard-hero__failbtn");
  assert.equal(button?.getAttribute("aria-haspopup"), "menu");
  assert.match(button?.textContent ?? "", /Failed: svc\/api:test/);
  assert.ok(button?.classList.contains("pf-m-danger"));
  assert.equal(button?.getAttribute("title"), null);
  tile.destroy();
});
