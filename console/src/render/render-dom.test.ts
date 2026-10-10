// render-dom.test.ts - the shared render layer's parts: the filter field, the clipboard helper, the
// instant formatter's element, the tree keys and the section frame. document/window are registered
// globally by test-setup.mjs (node --import), so this runs under node:test like the other *-dom
// tests. Each of these replaced a copy that every app built for itself and got slightly wrong.

import assert from "node:assert/strict";
import { test as nodeTest, type TestContext } from "node:test";
import { NOTIFY_EVENT, type NotifyInput } from "../lib/notifications";
import { must } from "../lib/guards";
import { copyText } from "./clipboard";
import { createFilterField } from "./filterField";
import { buildSection, createSection, setSectionOpen } from "./sections";
import { timeEl } from "./time";
import { attachTreeKeys } from "./treeKeys";

const notices: NotifyInput[] = [];
const onNotify = (e: Event): void => {
  notices.push((e as CustomEvent<NotifyInput>).detail);
};

// There is no file-level beforeEach on purpose. The DOM suite runs with
// --experimental-test-isolation=none, so a hook declared at file scope fires for every *-dom test in
// the process, siblings included, and this file's would wipe the status bar another file's test had
// just built. Each test starts from a clean body through this wrapper and cleans up after itself.
function test(name: string, fn: (t: TestContext) => void | Promise<void>): void {
  nodeTest(name, async (t) => {
    document.body.replaceChildren();
    notices.length = 0;
    document.addEventListener(NOTIFY_EVENT, onNotify);
    t.after(() => document.removeEventListener(NOTIFY_EVENT, onNotify));
    await fn(t);
  });
}

function text(el: Element | null): string {
  return (el?.textContent ?? "").trim();
}

test("the filter field reports its text after a pause, and clears at once", async () => {
  const seen: string[] = [];
  const field = createFilterField({
    label: "Filter things",
    placeholder: "Filter",
    onChange: (v) => seen.push(v),
    debounceMs: 10,
  });
  document.body.append(field.el);

  assert.equal(field.input.getAttribute("aria-label"), "Filter things");
  const clear = must(field.el.querySelector<HTMLButtonElement>(".console-filter__clear"));
  assert.equal(clear.hidden, true, "nothing to clear in an empty box");

  field.input.value = "abc";
  field.input.dispatchEvent(new Event("input"));
  assert.equal(clear.hidden, false);
  assert.deepEqual(seen, [], "typing is debounced");
  await new Promise((r) => setTimeout(r, 30));
  assert.deepEqual(seen, ["abc"]);

  clear.click();
  assert.equal(field.value(), "");
  assert.deepEqual(seen, ["abc", ""], "Clear applies immediately");
  assert.equal(clear.hidden, true);
  assert.equal(document.activeElement, field.input, "and keeps focus in the box");
  field.dispose();
});

test("Escape clears a filter that has text and stops there", () => {
  const seen: string[] = [];
  const field = createFilterField({ label: "f", placeholder: "f", onChange: (v) => seen.push(v) });
  document.body.append(field.el);
  field.setValue("abc");

  let reachedPane = false;
  document.addEventListener("keydown", () => (reachedPane = true), { once: true });
  field.input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert.equal(field.value(), "");
  assert.deepEqual(seen, [""]);
  assert.equal(reachedPane, false, "a panel that closes on Escape stays open");

  // With nothing to clear, Escape is not the box's.
  field.input.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  assert.equal(reachedPane, true);
});

test("the result count is shown and spoken", () => {
  const field = createFilterField({ label: "f", placeholder: "f", onChange: () => {} });
  document.body.append(field.el);
  const count = must(field.el.querySelector<HTMLElement>(".console-filter__count"));
  const spoken = must(field.el.querySelector<HTMLElement>("[role=status]"));
  assert.equal(count.hidden, true);

  field.setResults("3 of 12", "3 of 12 runs match the filter");
  assert.equal(count.hidden, false);
  assert.equal(text(count), "3 of 12");
  assert.equal(count.getAttribute("aria-hidden"), "true", "the badge is not read twice");
  assert.equal(text(spoken), "3 of 12 runs match the filter");

  field.setResults("", "");
  assert.equal(count.hidden, true);
  assert.equal(text(spoken), "");
});

test("a copy that fails is a toast, not a label that flickers", async () => {
  const realClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: () => Promise.reject(new Error("denied")) },
  });
  try {
    const btn = document.createElement("button");
    btn.textContent = "Copy";
    const ok = await copyText("x", { source: "Log Viewer", what: "the log", button: btn });
    assert.equal(ok, false);
    assert.equal(btn.textContent, "Copy failed");
    const failure = notices.find((n) => n.kind === "error");
    assert.ok(failure, "the failure is raised");
    assert.equal(failure.toast, true);
    assert.match(failure.message, /Could not copy the log: denied/);
  } finally {
    if (realClipboard) Object.defineProperty(navigator, "clipboard", realClipboard);
    else delete (navigator as { clipboard?: unknown }).clipboard;
  }
});

test("a successful copy flashes the control and can confirm in a toast", async () => {
  const written: string[] = [];
  const realClipboard = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: (t: string) => (written.push(t), Promise.resolve()) },
  });
  try {
    const btn = document.createElement("button");
    btn.innerHTML =
      '<span class="pf-v6-c-button__icon">i</span><span class="console-render-btn__label">Copy</span>';
    const ok = await copyText("hello", {
      source: "Log Viewer",
      what: "the log",
      button: btn,
      confirm: "Copied.",
    });
    assert.equal(ok, true);
    assert.deepEqual(written, ["hello"]);
    assert.equal(text(btn.querySelector(".console-render-btn__label")), "Copied");
    assert.ok(btn.querySelector(".pf-v6-c-button__icon"), "the icon is not disturbed");
  } finally {
    if (realClipboard) Object.defineProperty(navigator, "clipboard", realClipboard);
    else delete (navigator as { clipboard?: unknown }).clipboard;
  }
});

test("an instant is a <time> that carries the moment it was computed from", () => {
  const now = Date.now();
  const el = timeEl(now - 5 * 60_000, now);
  assert.equal(el.tagName, "TIME");
  assert.equal(el.dataset.time, String(now - 5 * 60_000));
  assert.equal(el.dateTime, new Date(now - 5 * 60_000).toISOString());
  assert.equal(el.textContent, "5m ago");
  assert.ok(el.title, "the full instant is a title, so a relative reading can be checked");
});

test("the section frame wires a toggle to its lines and keeps actions out of it", () => {
  const action = document.createElement("button");
  action.textContent = "Copy";
  const { secEl, toggle, lines } = createSection({
    status: "fail",
    collapsed: true,
    countText: "2 lines",
    fillTitle: (t) => (t.textContent = "head"),
    actions: [action],
  });
  document.body.append(secEl);

  assert.equal(secEl.getAttribute("data-status"), "fail");
  assert.equal(secEl.hasAttribute("data-collapsed"), true);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
  assert.equal(toggle.getAttribute("aria-controls"), lines.id);
  assert.equal(toggle.contains(action), false);

  toggle.click();
  assert.equal(secEl.hasAttribute("data-collapsed"), false);
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  setSectionOpen(secEl, false);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");
});

test("buildSection names its count in the caller's noun and offers a Copy", () => {
  const sec = buildSection(
    { title: "title", lines: ["title", "a", "b"] },
    { status: "", copyText: "title\na\nb", countNoun: ["detail", "details"] },
  );
  assert.equal(text(sec.querySelector(".console-render-section__count")), "2 details");
  assert.equal(sec.hasAttribute("data-status"), false);
  assert.equal(text(sec.querySelector(".console-render-section__action")), "Copy");
});

function treeOf(): HTMLElement {
  const root = document.createElement("div");
  root.innerHTML =
    '<ul role="tree">' +
    '<li role="treeitem" id="a" aria-expanded="true" aria-selected="false"><div class="pf-v6-c-tree-view__content"><button class="pf-v6-c-tree-view__node">A</button></div>' +
    '<ul role="group">' +
    '<li role="treeitem" id="a1" aria-selected="false"><div class="pf-v6-c-tree-view__content"><button class="pf-v6-c-tree-view__node">A1</button></div></li>' +
    "</ul></li>" +
    '<li role="treeitem" id="b" aria-expanded="false" aria-selected="false"><div class="pf-v6-c-tree-view__content"><button class="pf-v6-c-tree-view__node">B</button></div>' +
    '<ul role="group">' +
    '<li role="treeitem" id="b1" aria-selected="false"><div class="pf-v6-c-tree-view__content"><button class="pf-v6-c-tree-view__node">B1</button></div></li>' +
    "</ul></li></ul>";
  document.body.append(root);
  return must(root.querySelector<HTMLElement>("ul"));
}

function press(el: Element, key: string): void {
  el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
}

test("the tree is one Tab stop on its items, and the arrow keys walk the visible rows", () => {
  const tree = treeOf();
  const reseat = attachTreeKeys(tree);
  reseat();
  const stops = (): string[] =>
    [...tree.querySelectorAll<HTMLElement>('li[role="treeitem"]')]
      .filter((n) => n.tabIndex === 0)
      .map((n) => n.id);
  assert.deepEqual(stops(), ["a"]);
  assert.ok(
    [...tree.querySelectorAll<HTMLElement>(".pf-v6-c-tree-view__node")].every(
      (n) => n.tabIndex === -1,
    ),
    "the row buttons stay out of the tab order",
  );

  const a = must(document.getElementById("a"));
  a.focus();
  press(a, "ArrowDown");
  assert.equal(document.activeElement?.id, "a1");
  assert.deepEqual(stops(), ["a1"], "the tab stop follows focus");
  press(must(document.activeElement), "ArrowDown");
  assert.equal(document.activeElement?.id, "b", "a collapsed branch's children are skipped");
  press(must(document.activeElement), "End");
  assert.equal(document.activeElement?.id, "b");
  press(must(document.activeElement), "Home");
  assert.equal(document.activeElement?.id, "a");

  // Left on an open branch closes it (the row's own click does the toggling), and on a leaf climbs.
  let toggled = 0;
  a.addEventListener("click", () => toggled++);
  press(a, "ArrowLeft");
  assert.equal(toggled, 1);
  const leaf = must(document.getElementById("a1"));
  leaf.focus();
  press(leaf, "ArrowLeft");
  assert.equal(document.activeElement?.id, "a");

  // Enter on a focused item presses that item's own row button.
  let pressed = 0;
  must(leaf.querySelector("button")).addEventListener("click", () => pressed++);
  press(leaf, "Enter");
  assert.equal(pressed, 1);
});
