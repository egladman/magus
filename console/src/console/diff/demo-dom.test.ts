// demo-dom.test.ts - the Diff surface's daemon-free showcase, mounted. document/window come
// from test-setup.mjs (node --import), the same as the other *-dom tests.
//
// What is pinned here is the promise the #demo fragment makes: the surface renders FULLY with
// no daemon, no workspace and no network. So fetch is replaced with one that fails the test if
// it is called at all - a showcase that quietly falls back to a daemon works on the machine
// that has one running and shows an empty state to everybody else, which is the failure this
// mode exists to prevent.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { activate } from "./main";
import { dispatchCommand } from "../commands";

const realFetch = globalThis.fetch;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
  globalThis.fetch = (() => {
    throw new Error("demo mode must not reach the network");
  }) as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  location.hash = "";
});

// settle lets load()'s awaits and the per-hunk digests resolve. Turns rather than a delay:
// everything under test resolves on the microtask queue.
async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

test("#demo renders the changeset with no daemon", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "ready");

  const paths = [...document.querySelectorAll(".console-diff-row__path")].map(
    (el) => el.textContent,
  );
  assert.equal(paths[0], "libs/authkit/claims.go");

  // Rows the daemon's annotations produce, not the patch's: a story row is a touch, and the
  // text row proves the patch body reached the virtualizer.
  assert.ok(document.querySelector(".console-diff-row--story"));
  const text = [...document.querySelectorAll(".console-diff-row__text")].map(
    (el) => el.textContent,
  );
  assert.ok(text.some((t) => t?.includes("Audience Audience")));

  dispose.deactivate();
});

test("#demo lists the primary files in the sidebar and folds the generated group", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-sidebar__item").length, 11);
  assert.equal(document.querySelector(".console-diff-sidebar__group")?.textContent, "3 generated");

  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  // The showcase must never pass itself off as the reader's own tree - but it says so through
  // the shell's connection pill, the one place every surface says it. A second badge here made
  // the diff the only surface announcing demo twice, in a style nothing else uses.
  assert.ok(!chips.includes("demo data"), "demo state belongs to the connection pill, not a chip");
  assert.ok(chips.includes("11 files"));
  assert.ok(chips.includes("1 public surface"));
  assert.ok(chips.includes("1 untested"));
  // The ranking key is present in the fixture, so the surface must NOT be wearing the
  // "unranked" caveat while claiming a reading order.
  assert.ok(!chips.includes("unranked"));

  dispose.deactivate();
});

test("#demo shows the agent's pending suggestions in the rail", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const items = [...document.querySelectorAll(".console-diff-rail__item")];
  assert.equal(items.length, 2);
  assert.equal(items[0]?.querySelector(".console-diff-rail__who")?.textContent, "claude-code");

  // Skipping answers the suggestion in memory, so the rail has to shrink with no daemon in it.
  (items[0]?.querySelector(".console-diff-rail__skip") as HTMLButtonElement).click();
  await settle();
  assert.equal(document.querySelectorAll(".console-diff-rail__item").length, 1);

  dispose.deactivate();
});

test("#demo does not advertise workspace context it cannot provide", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-row__peek").length, 0);

  dispose.deactivate();
});

// The row button above is withheld on purpose, but the p key and the command palette reach the
// same action through no button at all - so a demo reader who finds either must still be told
// why nothing came up, not met with silence.
test("#demo answers the peek command instead of doing nothing", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.context.peek"));
  const context = document.querySelector<HTMLElement>(".console-diff-context");
  assert.equal(context?.hidden, false);
  assert.match(context?.textContent ?? "", /unavailable in this showcase/);

  dispose.deactivate();
});

// The surface used to carry its own "See the demo" button. It does not any more - one entry point for
// the whole console, the title bar's Workspace menu - so what it owes someone with no daemon is a
// SENTENCE naming where a populated version lives, not a dead end. Every /console/<surface>/ path is
// the shell with a <base> injected (scripts/surface-stubs.mjs), so that menu is always on screen.
test("without #demo and without a daemon the surface says where a populated one lives", async () => {
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "empty");
  assert.equal(
    document.querySelectorAll(".pf-v6-c-empty-state__footer button").length,
    0,
    "the per-surface demo button is gone",
  );
  const body = document.querySelector<HTMLElement>(".pf-v6-c-empty-state__body")?.textContent ?? "";
  assert.match(body, /Workspace menu/, "an empty surface has to name where a populated one lives");

  dispose.deactivate();
});

// #demo still reaches the demo with no button anywhere - the fragment is the mechanism, and the
// button was only ever one way to set it.
test("#demo still loads the fabricated changeset", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "ready");
  assert.ok(document.querySelector(".console-diff-row__path"));

  dispose.deactivate();
});

// The pane-width defaults. These key off the surface's OWN box, not the viewport, because the
// surface tiles: two panes on a wide desktop give it far less room than the window suggests, and a
// viewport query reads "wide" for both. happy-dom does no layout, so the ResizeObserver is stubbed
// and driven by hand - which is also the only way to exercise a retile without a real browser.
//
// The stub is restored in the same test. Isolation is `none`, so a leaked global would follow every
// other *-dom test in the process.
test("the diff sizes itself from its own pane, not the window", async () => {
  const realRO = globalThis.ResizeObserver;
  // activate() installs two observers (the sidebar virtualizer's and the pane's); keep the one
  // watching the layout root.
  let paneCb: ResizeObserverCallback | null = null;
  class CapturingRO {
    constructor(private cb: ResizeObserverCallback) {}
    observe(el: Element): void {
      if (el.classList.contains("console-diff-layout")) paneCb = this.cb;
    }
    unobserve(): void {}
    disconnect(): void {}
  }
  globalThis.ResizeObserver = CapturingRO as unknown as typeof ResizeObserver;

  try {
    location.hash = "#demo";
    const dispose = activate(document.body);
    await settle();

    const root = document.querySelector<HTMLElement>(".console-diff-layout");
    assert.ok(paneCb, "the surface must observe its own root");
    // happy-dom reports innerWidth 1024, so the surface mounts in its wide defaults.
    assert.equal(root?.dataset.sidebar, "open");

    const resizeTo = async (width: number): Promise<void> => {
      (paneCb as ResizeObserverCallback)(
        [{ contentRect: { width } } as ResizeObserverEntry],
        {} as ResizeObserver,
      );
      await settle();
    };

    // A 700px PANE on an unchanged 1024px window: the case a viewport query cannot see.
    await resizeTo(700);
    assert.equal(root?.dataset.sidebar, "collapsed", "a narrow pane folds the index");

    // Retiled back to full width, the index returns.
    await resizeTo(1200);
    assert.equal(root?.dataset.sidebar, "open", "a wide pane restores the index");

    // A zero box is a detached or hidden surface, not a measurement, and must not collapse it.
    await resizeTo(0);
    assert.equal(root?.dataset.sidebar, "open", "an unlaid-out box is not a narrow one");

    dispose.deactivate();
  } finally {
    globalThis.ResizeObserver = realRO;
  }
});
