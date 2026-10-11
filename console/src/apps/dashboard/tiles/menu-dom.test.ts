// menu-dom.test.ts - the board's button-and-menu pair (failing-target chips, the log viewer picker).
// Both were hand-rolled role="menu" popups with no keyboard; they are PF Menus behind wireMenu now,
// and what a keyboard reader needs is pinned here.

import assert from "node:assert/strict";
import { afterEach, test } from "node:test";
import { menuButton } from "./menu";

afterEach(() => document.body.replaceChildren());

function mount(runs: string[]) {
  const button = document.createElement("button");
  button.type = "button";
  button.textContent = "Open";
  const menu = menuButton(button, [
    { label: "First", run: () => runs.push("first") },
    { label: "Second", run: () => runs.push("second") },
  ]);
  document.body.append(menu.el);
  return menu;
}

function key(el: Element, k: string): void {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
}

const items = (): HTMLElement[] => [...document.querySelectorAll<HTMLElement>('[role="menuitem"]')];

test("Down on the closed button opens the menu onto its first item", () => {
  const menu = mount([]);
  const popup = menu.el.querySelector<HTMLElement>(".pf-v6-c-menu");
  assert.equal(popup?.hidden, true);
  assert.equal(menu.button.getAttribute("aria-haspopup"), "menu");
  key(menu.button, "ArrowDown");
  assert.equal(popup?.hidden, false);
  assert.equal(menu.button.getAttribute("aria-expanded"), "true");
  assert.equal(document.activeElement, items()[0]);
  menu.dispose();
});

test("arrows move through the items and Escape returns to the button", () => {
  const menu = mount([]);
  key(menu.button, "ArrowDown");
  key(items()[0], "ArrowDown");
  assert.equal(document.activeElement, items()[1]);
  key(items()[1], "Escape");
  assert.equal(menu.el.querySelector<HTMLElement>(".pf-v6-c-menu")?.hidden, true);
  assert.equal(document.activeElement, menu.button);
  menu.dispose();
});

test("choosing an item runs it and closes the menu", () => {
  const runs: string[] = [];
  const menu = mount(runs);
  menu.button.click();
  items()[1].click();
  assert.deepEqual(runs, ["second"]);
  assert.equal(menu.el.querySelector<HTMLElement>(".pf-v6-c-menu")?.hidden, true);
  menu.dispose();
});

test("setActions replaces the rows without re-wiring", () => {
  const runs: string[] = [];
  const menu = mount(runs);
  menu.setActions([{ label: "Only", run: () => runs.push("only") }]);
  menu.button.click();
  assert.equal(items().length, 1);
  items()[0].click();
  assert.deepEqual(runs, ["only"]);
  menu.dispose();
});

test("disposing takes the document listener with it", () => {
  const menu = mount([]);
  menu.button.click();
  menu.dispose();
  // A click anywhere used to be heard by every menu ever mounted; a disposed one stays open.
  document.body.click();
  assert.equal(menu.el.querySelector<HTMLElement>(".pf-v6-c-menu")?.hidden, false);
});
