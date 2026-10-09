// app-menu-dom.test.ts - the title-bar Applications menu: its group is named and read, picking an app
// dispatches its open command, and the menu is the keyboard menu from menu.ts.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { registerCommand, unregisterCommand } from "../desktop/commands";
import { initAppMenu } from "./app-menu";

const APPS = [
  { id: "runs", label: "Runs" },
  { id: "logs", label: "Logs" },
];

// The slice of index.html the menu binds to.
function mount(): { btn: HTMLElement; panel: HTMLElement; list: HTMLElement } {
  const host = document.createElement("div");
  host.dataset.appMenuHost = "";
  host.innerHTML =
    '<button id="console-appmenu-btn" type="button" aria-label="Menu" title="Menu"></button>' +
    '<div id="console-appmenu" hidden aria-label="Menu"><div class="pf-v6-c-menu__content">' +
    '<section class="pf-v6-c-menu__group">' +
    '<h3 class="pf-v6-c-menu__group-title" aria-hidden="true">Menu</h3>' +
    '<ul role="menu" data-app-list></ul></section>' +
    '<ul role="menu"><li role="none"><a role="menuitem" href="#docs">Documentation</a></li></ul>' +
    "</div></div>";
  document.body.append(host);
  const q = (s: string): HTMLElement => {
    const el = host.querySelector<HTMLElement>(s);
    assert.ok(el, s);
    return el;
  };
  return {
    btn: q("#console-appmenu-btn"),
    panel: q("#console-appmenu"),
    list: q("[data-app-list]"),
  };
}

describe("the Applications menu", () => {
  afterEach(() => {
    for (const el of document.querySelectorAll("[data-app-menu-host]")) el.remove();
    unregisterCommand("console.open.runs");
  });

  test("the group is titled Applications, is not hidden from readers, and names its list", () => {
    const { panel, list } = mount();
    initAppMenu(APPS);
    const title = panel.querySelector(".pf-v6-c-menu__group-title");
    assert.equal(title?.textContent, "Applications");
    assert.equal(title?.hasAttribute("aria-hidden"), false);
    assert.ok(title?.id);
    assert.equal(list.getAttribute("aria-labelledby"), title?.id);
    assert.equal(panel.getAttribute("aria-label"), "Applications");
  });

  test("one menu item per app, then the documentation link", () => {
    const { panel } = mount();
    initAppMenu(APPS);
    const labels = [...panel.querySelectorAll('[role="menuitem"]')].map((i) => i.textContent);
    assert.deepEqual(labels, ["Runs", "Logs", "Documentation"]);
  });

  test("the button opens it onto the first app and Escape returns focus to the button", () => {
    const { btn, panel } = mount();
    initAppMenu(APPS);
    btn.click();
    assert.equal(panel.hidden, false);
    assert.equal(document.activeElement?.textContent, "Runs");
    document.activeElement?.dispatchEvent(
      new KeyboardEvent("keydown", { key: "Escape", bubbles: true }),
    );
    assert.equal(panel.hidden, true);
    assert.equal(document.activeElement, btn);
  });

  test("Down reaches the documentation link past the app list", () => {
    const { btn } = mount();
    initAppMenu(APPS);
    btn.click();
    for (let i = 0; i < 2; i++) {
      document.activeElement?.dispatchEvent(
        new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }),
      );
    }
    assert.equal(document.activeElement?.textContent, "Documentation");
  });

  test("picking an app closes the menu and runs its open command", () => {
    const { btn, panel } = mount();
    const opened: string[] = [];
    registerCommand({
      id: "console.open.runs",
      label: "Open Runs",
      run: () => {
        opened.push("runs");
      },
    });
    initAppMenu(APPS);
    btn.click();
    panel.querySelector<HTMLElement>('[data-app-open="runs"]')?.click();
    assert.deepEqual(opened, ["runs"]);
    assert.equal(panel.hidden, true);
  });

  test("without the markup it does nothing and still returns a disposer", () => {
    const dispose = initAppMenu(APPS);
    assert.equal(typeof dispose, "function");
    dispose();
  });
});
