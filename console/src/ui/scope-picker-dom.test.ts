// scope-picker-dom.test.ts - the title bar's workspace control: when it exists at all, what picking
// does, and whether it lets go on destroy. The teardown case is the reason this file exists: the two
// outside-click listeners live on `document`, not on the control, so dropping the reference alone
// would leave a detached menu answering clicks for the life of the page.

import assert from "node:assert/strict";
import { describe, test, beforeEach } from "node:test";
import { initScopePicker } from "./scope-picker";
import { ALL_WORKSPACES, setWorkspaceScope, workspaceScope } from "../lib/scope";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";
const BOTH = [ACME, MAGUS];

function mount() {
  const host = document.createElement("div");
  host.dataset.scopePickerHost = "";
  document.body.append(host);
  const picker = initScopePicker(host);
  const btn = host.querySelector<HTMLButtonElement>("#console-scope-btn");
  assert.ok(btn);
  return { host, picker, btn };
}

function menuItems(host: HTMLElement): HTMLButtonElement[] {
  return [...host.querySelectorAll<HTMLButtonElement>(".pf-v6-c-menu__item")];
}

// Scoped, not top-level: with --test-isolation=none every -dom file shares one process, so a hook
// registered on the root context runs before every other file's tests and wipes what they set up.
describe("the workspace scope control", () => {
  beforeEach(() => {
    sessionStorage.removeItem("magus:workspace-scope");
    for (const el of document.querySelectorAll("[data-scope-picker-host]")) el.remove();
  });

  // With one workspace loaded, scoped and unscoped show the same thing - the control would be a
  // permanent reminder of a decision with one possible answer.
  test("the control stays hidden until scope is a real question", () => {
    const { picker, btn } = mount();
    assert.equal(btn.hidden, true, "nothing published yet");

    picker.setWorkspaces([ACME]);
    assert.equal(btn.hidden, true, "one workspace is one answer");

    picker.setWorkspaces(BOTH);
    assert.equal(btn.hidden, false);
  });

  test("the menu offers the daemon-wide view first, then each workspace", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    btn.click();
    const items = menuItems(host);
    assert.equal(items.length, 3);
    assert.equal(items[0].textContent, "All workspaces");
    assert.deepEqual(
      items.slice(1).map((b) => b.title),
      BOTH,
    );
  });

  test("picking scopes the browser tab", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    btn.click();
    const magus = menuItems(host).find((b) => b.title === MAGUS);
    assert.ok(magus);
    magus.click();
    assert.equal(workspaceScope(), MAGUS);
  });

  // The scope can change from anywhere in the tab - the Connect screen, another surface - and a control
  // that only wrote would keep announcing the workspace you left.
  test("the control follows a scope set somewhere else", () => {
    const { picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    setWorkspaceScope(ACME);
    assert.match(btn.textContent ?? "", /acme/);

    setWorkspaceScope(ALL_WORKSPACES);
    assert.match(btn.textContent ?? "", /All workspaces/);
  });

  // Focus would otherwise sit on an element that just became hidden, which drops it to the body and
  // restarts the next Tab at the top of the page.
  test("choosing returns focus to the control", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    btn.click();
    const magus = menuItems(host).find((b) => b.title === MAGUS);
    assert.ok(magus);
    magus.focus();
    magus.click();
    assert.equal(document.activeElement, btn);
  });

  test("destroy removes the control and stops it tracking the scope", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    setWorkspaceScope(ACME);
    const before = btn.textContent;

    picker.destroy();
    assert.equal(host.querySelector("#console-scope-btn"), null, "the control should be gone");

    // The subscription is a window listener, so it outlives the element unless destroy drops it.
    setWorkspaceScope(MAGUS);
    assert.equal(btn.textContent, before, "a destroyed picker must not still be painting");
  });

  // Two mounts must not leave the first one's document listeners behind, which is what a picker with no
  // teardown does every time a test or a re-init runs.
  test("a second picker does not inherit the first one's listeners", () => {
    const first = mount();
    first.picker.setWorkspaces(BOTH);
    first.picker.destroy();

    const second = mount();
    second.picker.setWorkspaces(BOTH);
    second.btn.click();
    assert.equal(second.btn.getAttribute("aria-expanded"), "true");

    // An outside click closes exactly one menu - the live one.
    document.body.click();
    assert.equal(second.btn.getAttribute("aria-expanded"), "false");
  });
});
