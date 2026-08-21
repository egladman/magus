// workspace-picker-dom.test.ts - the title bar's workspace control: when it exists at all, what picking
// does, and whether it lets go on destroy. The teardown case is the reason this file exists: the two
// outside-click listeners live on `document`, not on the control, so dropping the reference alone
// would leave a detached menu answering clicks for the life of the page.

import assert from "node:assert/strict";
import { describe, test, beforeEach } from "node:test";
import { initWorkspacePicker } from "./workspace-picker";
import { ALL_WORKSPACES, setWorkspaceScope, workspaceScope } from "../lib/scope";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";
const BOTH = [ACME, MAGUS];

function mount() {
  const host = document.createElement("div");
  host.dataset.workspacePickerHost = "";
  document.body.append(host);
  const demoCalls: boolean[] = [];
  const picker = initWorkspacePicker(host, { onDemo: (enter) => demoCalls.push(enter) });
  const btn = host.querySelector<HTMLButtonElement>("#console-scope-btn");
  assert.ok(btn);
  return { host, picker, btn, demoCalls };
}

function menuItems(host: HTMLElement): HTMLButtonElement[] {
  return [...host.querySelectorAll<HTMLButtonElement>(".pf-v6-c-menu__item")];
}

// Scoped, not top-level: with --test-isolation=none every -dom file shares one process, so a hook
// registered on the root context runs before every other file's tests and wipes what they set up.
describe("the workspace scope control", () => {
  beforeEach(() => {
    sessionStorage.removeItem("magus:workspace-scope");
    for (const el of document.querySelectorAll("[data-workspace-picker-host]")) el.remove();
  });

  // With one workspace loaded, scoped and unscoped show the same thing - the control would be a
  // permanent reminder of a decision with one possible answer.
  // It used to hide below two workspaces, because scope was not a question with one possible answer.
  // It is the way into the demo now, and a console with no daemon has zero workspaces - so the old
  // rule would have hidden the only control offering anything at all on a first visit.
  test("the control is present even with nothing loaded", () => {
    const { host, picker } = mount();
    const wrap = host.querySelector<HTMLElement>("#console-scope");
    assert.ok(wrap);
    assert.equal(wrap.hidden, false, "the demo has to be reachable with no daemon");

    picker.setWorkspaces(BOTH);
    assert.equal(wrap.hidden, false);
  });

  // The six per-surface "See the demo" buttons are gone; this row is the only way in.
  test("the menu offers the demo, and asks to enter it", () => {
    const { host, picker, btn, demoCalls } = mount();
    picker.setWorkspaces(BOTH);
    btn.click();
    const demo = menuItems(host).find((i) => i.textContent?.includes("Demo data"));
    assert.ok(demo, "no way into the demo");
    demo.click();
    assert.deepEqual(demoCalls, [true]);
  });

  // The button's own text is the VALUE ("acme"), which in a row of icon buttons could be a title, a
  // filter, or a pair of tabs. The caption is what tells a sighted reader what picking one does - and
  // it has to be INSIDE the control, or it is just more text in the title bar.
  test("the caption and the value are one control", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    const wrap = host.querySelector<HTMLElement>("#console-scope");
    const caption = host.querySelector<HTMLElement>(".console-shell-scope__caption");
    assert.ok(wrap);
    assert.ok(caption);
    assert.equal(caption.textContent, "Workspace");
    assert.equal(caption.parentElement, wrap, "the caption must live inside the bordered control");
    assert.equal(btn.parentElement, wrap, "and so must the value");
    // One name, not a visible one and a different spoken one.
    assert.equal(btn.getAttribute("aria-labelledby"), "console-scope-caption " + btn.id);
    assert.equal(btn.getAttribute("aria-label"), null, "aria-label would override the pair");
  });

  // aria-haspopup tells a screen reader this opens a menu; the caret is what tells everyone else.
  test("the value carries a caret", () => {
    const { host, picker } = mount();
    picker.setWorkspaces(BOTH);
    assert.ok(host.querySelector(".console-shell-scope__caret svg"));
  });

  test("the menu offers the daemon-wide view first, then each workspace", () => {
    const { host, picker, btn } = mount();
    picker.setWorkspaces(BOTH);
    btn.click();
    const items = menuItems(host);
    // All workspaces, the two roots, then the demo action.
    assert.equal(items.length, 4);
    assert.equal(items[0].textContent, "All workspaces");
    assert.deepEqual(
      items.slice(1, 3).map((b) => b.title),
      BOTH,
    );
    assert.ok(items[3].textContent?.includes("Demo data"));
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
    assert.equal(host.querySelector("#console-scope"), null, "the control should be gone");
    assert.equal(
      host.querySelector(".console-shell-scope__caption"),
      null,
      "a caption naming a control that no longer exists is worse than none",
    );

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
