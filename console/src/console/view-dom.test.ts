// view-dom.test.ts - the render layer that needs a document: h() element creation and
// bind() wiring a signal onto a real DOM node. document/window are registered globally by
// test-setup.mjs (node --import), so these run under node:test beside the pure-logic
// view.test.ts.
import assert from "node:assert/strict";
import { test } from "node:test";
import { bind, h, signal } from "./view";

test("h creates an element with the given tag, class, and text", () => {
  const el = h("div", "console-x", "hello");
  assert.equal(el.tagName, "DIV");
  assert.equal(el.className, "console-x");
  assert.equal(el.textContent, "hello");
});

test("h omits class and text when not given", () => {
  const el = h("span");
  assert.equal(el.className, "");
  assert.equal(el.textContent, "");
});

test("h elements compose into a real DOM tree via append", () => {
  const row = h("li", "row");
  row.append(h("span", "label", "name"), h("code", "token", "open.logs"));
  assert.equal(row.children.length, 2);
  assert.equal(row.querySelector(".token")?.textContent, "open.logs");
});

test("bind wires signal changes onto a DOM node", () => {
  const label = signal("a");
  const el = h("span");
  bind(label, (v) => {
    el.textContent = v;
  });
  label.set("b");
  assert.equal(el.textContent, "b");
});
