// card-dom.test.ts - the tile shell every board card is built from. What is pinned is what a reader
// that cannot see the page depends on: the card has a name, its toggle is named by the card it
// folds, the "?" popovers are closed and unwired when the board is torn down, and a card with
// nothing to show says so in the one shared empty state.

import assert from "node:assert/strict";
import { beforeEach, test } from "node:test";
import {
  Card,
  countBadge,
  disposeHelpGlyphs,
  emptyState,
  helpGlyph,
  prose,
  tableScroller,
} from "./card";

beforeEach(() => {
  localStorage.clear();
  document.body.replaceChildren();
});

test("a card is a compact PF card named by its title, with a heading and a toggle", () => {
  const card = new Card("t-name", "Workspaces");
  document.body.append(card.el);
  assert.ok(card.el.classList.contains("pf-m-compact"));
  const title = card.el.querySelector(".pf-v6-c-card__title-text");
  assert.equal(title?.tagName, "H3", "card titles are h3 under the board's h2 sections");
  assert.equal(card.el.getAttribute("aria-labelledby"), title?.id);
  const toggle = card.el.querySelector<HTMLButtonElement>("[data-collapse]");
  assert.equal(toggle?.getAttribute("aria-labelledby"), title?.id, "named by the card it folds");
  assert.equal(toggle?.getAttribute("aria-label"), null, "not a generic 'Collapse card'");
  assert.ok(toggle?.classList.contains("pf-m-plain"), "PF's plain button, so PF's hit area");
});

test("the toggle folds the card and says so in aria-expanded", () => {
  const card = new Card("t-fold", "Pool");
  document.body.append(card.el);
  const toggle = card.el.querySelector<HTMLButtonElement>("[data-collapse]");
  assert.equal(toggle?.getAttribute("aria-expanded"), "true");
  toggle?.click();
  assert.equal(toggle?.getAttribute("aria-expanded"), "false");
  assert.equal(card.el.hasAttribute("data-collapsed"), true);
  assert.equal(card.el.classList.contains("pf-m-expanded"), false);
  toggle?.click();
  assert.equal(card.el.hasAttribute("data-collapsed"), false);
});

test("two cards never share a title id", () => {
  const a = new Card("t-a", "A");
  const b = new Card("t-b", "B");
  const idA = a.el.querySelector(".pf-v6-c-card__title-text")?.id;
  const idB = b.el.querySelector(".pf-v6-c-card__title-text")?.id;
  assert.ok(idA && idB && idA !== idB);
});

test("the help popover opens from the glyph and is closed by disposing the board", () => {
  const host = document.createElement("div");
  host.append(helpGlyph("It matters because of this.", "the pool"));
  document.body.append(host);
  const trigger = host.querySelector<HTMLButtonElement>("[data-help-trigger]");
  assert.ok(trigger);
  trigger.click();
  assert.equal(document.querySelectorAll(".console-help-popover").length, 1);

  disposeHelpGlyphs(host);
  assert.equal(
    document.querySelectorAll(".console-help-popover").length,
    0,
    "an open popover does not outlive the board that owned its trigger",
  );
  trigger.click();
  assert.equal(
    document.querySelectorAll(".console-help-popover").length,
    0,
    "an unwired trigger opens nothing",
  );
});

test("setEmpty swaps in the shared empty state and marks the card for the rotator", () => {
  const card = new Card("t-empty", "Locks");
  card.body.append(document.createElement("ul"));
  document.body.append(card.el);

  card.setEmpty("No `locks` are held.");
  assert.equal(card.el.hasAttribute("data-empty"), true);
  const state = card.el.querySelector("[data-empty-state]");
  assert.ok(state?.classList.contains("pf-m-xs"), "PF's extra-small empty state");
  assert.equal(state?.textContent, "No locks are held.");
  assert.equal(state?.querySelectorAll("code").length, 1, "identifiers are code");

  // The same text again is not a rebuild: the card is repainted on every status frame.
  card.setEmpty("No `locks` are held.");
  assert.equal(card.el.querySelector("[data-empty-state]"), state);

  card.setEmpty(null);
  assert.equal(card.el.hasAttribute("data-empty"), false);
  assert.equal(card.el.querySelector("[data-empty-state]"), null);
});

test("prose turns a backticked command into code and leaves the rest as text", () => {
  const p = document.createElement("p");
  prose(p, "Run `magus run test` first.");
  assert.equal(p.textContent, "Run magus run test first.");
  assert.equal(p.querySelector("code")?.textContent, "magus run test");
  assert.equal(emptyState("Nothing.").querySelector("code"), null);
});

test("a count badge is a PF read badge that names its unit to a screen reader", () => {
  const badge = countBadge("locks held");
  badge.set(3);
  assert.ok(badge.el.classList.contains("pf-v6-c-badge"));
  assert.equal(badge.el.textContent, "3 locks held");
  assert.equal(badge.el.querySelector(".pf-v6-screen-reader")?.textContent, " locks held");
});

test("a table's scroll container becomes a named, focusable region", () => {
  const holder = document.createElement("div");
  const wrap = document.createElement("div");
  wrap.className = "console-table__wrap";
  holder.append(wrap);
  tableScroller(holder, "Targets, scrolls sideways");
  assert.equal(wrap.tabIndex, 0);
  assert.equal(wrap.getAttribute("role"), "region");
  assert.equal(wrap.getAttribute("aria-label"), "Targets, scrolls sideways");
});
