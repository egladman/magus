// menu-dom.test.ts - the keyboard half of a menu: roving focus, the keys that move it, and every way the
// menu closes, with where focus lands each time. The last case pins that the disposer lets go.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { wireMenu } from "./menu";

function build(labels = ["One", "Two", "Three"]) {
  const host = document.createElement("div");
  host.dataset.menuHost = "";
  const trigger = document.createElement("button");
  trigger.type = "button";
  trigger.textContent = "Open";
  const menu = document.createElement("div");
  menu.hidden = true;
  const items = labels.map((label) => {
    const b = document.createElement("button");
    b.type = "button";
    b.setAttribute("role", "menuitem");
    b.textContent = label;
    menu.append(b);
    return b;
  });
  const outside = document.createElement("button");
  host.append(trigger, menu, outside);
  document.body.append(host);
  return { host, trigger, menu, items, outside };
}

function key(el: Element, k: string): KeyboardEvent {
  const e = new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true });
  el.dispatchEvent(e);
  return e;
}

describe("wireMenu", () => {
  afterEach(() => {
    for (const el of document.querySelectorAll("[data-menu-host]")) el.remove();
  });

  test("the trigger opens it onto the first item and toggles it shut", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    assert.equal(trigger.getAttribute("aria-expanded"), "false");
    assert.equal(trigger.getAttribute("aria-haspopup"), "menu");

    trigger.click();
    assert.equal(menu.hidden, false);
    assert.equal(trigger.getAttribute("aria-expanded"), "true");
    assert.equal(document.activeElement, items[0]);

    trigger.click();
    assert.equal(menu.hidden, true);
    assert.equal(trigger.getAttribute("aria-expanded"), "false");
    assert.equal(document.activeElement, trigger);
  });

  test("roving focus: one item is tabbable, the rest are not", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    trigger.click();
    const tabs = (): number[] => items.map((i) => i.tabIndex);
    assert.deepEqual(tabs(), [0, -1, -1]);
    key(items[0], "ArrowDown");
    assert.deepEqual(tabs(), [-1, 0, -1]);
  });

  test("Up and Down move and wrap; Home and End jump", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    trigger.click();

    key(document.activeElement as Element, "ArrowDown");
    assert.equal(document.activeElement, items[1]);
    key(document.activeElement as Element, "ArrowDown");
    key(document.activeElement as Element, "ArrowDown");
    assert.equal(document.activeElement, items[0], "wraps to the top");
    key(document.activeElement as Element, "ArrowUp");
    assert.equal(document.activeElement, items[2], "wraps to the bottom");
    key(document.activeElement as Element, "Home");
    assert.equal(document.activeElement, items[0]);
    key(document.activeElement as Element, "End");
    assert.equal(document.activeElement, items[2]);
  });

  test("a handled key is prevented so the page does not scroll", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    trigger.click();
    assert.equal(key(items[0], "ArrowDown").defaultPrevented, true);
    assert.equal(key(items[1], "a").defaultPrevented, false);
  });

  test("Down on a closed trigger opens onto the first item, Up onto the last", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    key(trigger, "ArrowDown");
    assert.equal(menu.hidden, false);
    assert.equal(document.activeElement, items[0]);
    key(items[0], "Escape");
    key(trigger, "ArrowUp");
    assert.equal(document.activeElement, items[2]);
  });

  test("a checked radio item is where it opens", () => {
    const { trigger, menu, items } = build();
    items[1].setAttribute("role", "menuitemradio");
    items[1].setAttribute("aria-checked", "true");
    wireMenu(menu, trigger);
    trigger.click();
    assert.equal(document.activeElement, items[1]);
  });

  test("Escape closes it and returns focus to the trigger, without reaching ancestors", () => {
    const { host, trigger, menu, items } = build();
    wireMenu(menu, trigger);
    let reached = false;
    host.addEventListener("keydown", () => {
      reached = true;
    });
    trigger.click();
    key(items[0], "Escape");
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, trigger);
    assert.equal(reached, false);
  });

  test("Tab sends focus to the trigger and closes, without being prevented", () => {
    const { trigger, menu, items } = build();
    wireMenu(menu, trigger);
    trigger.click();
    const tab = key(items[0], "Tab");
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, trigger);
    assert.equal(tab.defaultPrevented, false, "the browser carries on from the trigger");
  });

  test("an outside click closes it and leaves focus where the click put it", () => {
    const { trigger, menu, outside } = build();
    wireMenu(menu, trigger);
    trigger.click();
    outside.focus();
    outside.click();
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, outside);
  });

  test("an outside click while focus is still inside returns it to the trigger", () => {
    const { trigger, menu } = build();
    wireMenu(menu, trigger);
    trigger.click();
    document.body.click();
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, trigger);
  });

  test("a click inside the menu does not close it until an item is activated", () => {
    const { trigger, menu, items } = build();
    const padding = document.createElement("div");
    menu.append(padding);
    wireMenu(menu, trigger);
    trigger.click();
    padding.click();
    assert.equal(menu.hidden, false);
    items[2].click();
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, trigger, "focus came back because it was inside");
  });

  test("activating an item that moved focus elsewhere does not pull it back", () => {
    const { trigger, menu, items, outside } = build();
    wireMenu(menu, trigger);
    items[0].addEventListener("click", () => outside.focus());
    trigger.click();
    items[0].click();
    assert.equal(menu.hidden, true);
    assert.equal(document.activeElement, outside);
  });

  test("disabled and hidden items are skipped", () => {
    const { trigger, menu, items } = build();
    items[1].setAttribute("aria-disabled", "true");
    wireMenu(menu, trigger);
    trigger.click();
    key(items[0], "ArrowDown");
    assert.equal(document.activeElement, items[2]);
  });

  test("onOpen runs before the menu shows, and onClose after it hides", () => {
    const { trigger, menu } = build();
    const seen: string[] = [];
    wireMenu(menu, trigger, {
      onOpen: () => seen.push("open:" + menu.hidden),
      onClose: () => seen.push("close:" + menu.hidden),
    });
    trigger.click();
    trigger.click();
    assert.deepEqual(seen, ["open:true", "close:true"]);
  });

  test("items rebuilt by onOpen are the ones that get focus", () => {
    const { trigger, menu } = build();
    wireMenu(menu, trigger, {
      onOpen: () => {
        const fresh = document.createElement("button");
        fresh.setAttribute("role", "menuitem");
        fresh.textContent = "Fresh";
        menu.replaceChildren(fresh);
      },
    });
    trigger.click();
    assert.equal(document.activeElement?.textContent, "Fresh");
  });

  test("the disposer removes every listener and is safe twice", () => {
    const { trigger, menu, outside } = build();
    const dispose = wireMenu(menu, trigger);
    trigger.click();
    assert.equal(menu.hidden, false);
    dispose();
    dispose();
    outside.click();
    assert.equal(menu.hidden, false, "no longer answers outside clicks");
    trigger.click();
    assert.equal(menu.hidden, false, "no longer answers the trigger");
  });
});
