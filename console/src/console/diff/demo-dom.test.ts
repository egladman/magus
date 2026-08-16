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
  assert.ok(text.some((t) => t?.includes("Audience []string")));

  dispose();
});

test("#demo lists the primary files in the sidebar and folds the generated group", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-sidebar__item").length, 7);
  assert.equal(document.querySelector(".console-diff-sidebar__group")?.textContent, "3 generated");

  const chips = [...document.querySelectorAll(".console-diff-toolbar__stats .pf-v6-c-label")].map(
    (el) => el.textContent,
  );
  // The showcase must never pass itself off as the reader's own tree.
  assert.ok(chips.includes("demo data"));
  assert.ok(chips.includes("7 files"));
  assert.ok(chips.includes("1 public surface"));
  assert.ok(chips.includes("1 untested"));
  // The ranking key is present in the fixture, so the surface must NOT be wearing the
  // "unranked" caveat while claiming a reading order.
  assert.ok(!chips.includes("unranked"));

  dispose();
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

  dispose();
});

test("without #demo and without a daemon the surface offers the demo instead", async () => {
  const dispose = activate(document.body);
  await settle();

  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.equal(root?.dataset.phase, "empty");
  const btn = document.querySelector<HTMLButtonElement>(".pf-v6-c-empty-state__footer button");
  assert.ok(btn && !(btn.parentElement?.parentElement as HTMLElement).hidden);

  btn.click();
  await settle();
  assert.equal(root?.dataset.phase, "ready");
  assert.ok(document.querySelector(".console-diff-row__path"));

  dispose();
});
