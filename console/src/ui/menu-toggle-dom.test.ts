// menu-toggle-dom.test.ts - the PF MenuToggle every menu trigger is built from: a text toggle carries its
// label and caret, an icon-only plain toggle is named by aria-label, and wireMenu drives aria-expanded.

import assert from "node:assert/strict";
import { test } from "node:test";
import { wireMenu } from "./menu";
import { kebabIcon, menuToggle } from "./menu-toggle";

test("a text toggle has its label and PF's caret, and starts collapsed", () => {
  const t = menuToggle({ text: "Dismiss older", small: true });
  assert.ok(t.classList.contains("pf-v6-c-menu-toggle"));
  assert.ok(t.classList.contains("pf-m-small"));
  assert.equal(t.type, "button");
  assert.equal(t.getAttribute("aria-expanded"), "false");
  assert.equal(t.querySelector(".pf-v6-c-menu-toggle__text")?.textContent, "Dismiss older");
  assert.ok(
    t.querySelector(".pf-v6-c-menu-toggle__controls .pf-v6-c-menu-toggle__toggle-icon svg"),
  );
});

test("an icon-only plain toggle is its icon, named by aria-label", () => {
  const t = menuToggle({ variant: "plain", icon: kebabIcon(), ariaLabel: "More actions" });
  assert.ok(t.classList.contains("pf-m-plain"));
  assert.equal(t.getAttribute("aria-label"), "More actions");
  assert.ok(t.querySelector(".pf-v6-c-menu-toggle__icon svg"));
  assert.equal(t.querySelector(".pf-v6-c-menu-toggle__controls"), null, "no caret without a label");
});

test("status and variant modifiers ride the toggle", () => {
  const t = menuToggle({ text: "x", variant: "secondary", danger: true, classes: "mine" });
  assert.ok(t.classList.contains("pf-m-secondary"));
  assert.ok(t.classList.contains("pf-m-danger"));
  assert.ok(t.classList.contains("mine"));
});

test("wireMenu keeps its aria-expanded honest", () => {
  const t = menuToggle({ text: "Open" });
  const menu = document.createElement("div");
  menu.hidden = true;
  document.body.append(t, menu);
  const dispose = wireMenu(menu, t);
  t.click();
  assert.equal(t.getAttribute("aria-expanded"), "true");
  t.click();
  assert.equal(t.getAttribute("aria-expanded"), "false");
  dispose();
  t.remove();
  menu.remove();
});
