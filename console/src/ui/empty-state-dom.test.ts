// empty-state-dom.test.ts - the PF EmptyState structure: header holding the icon over the title, a
// body, and the actions inside a footer.

import assert from "node:assert/strict";
import { test } from "node:test";
import { statusGlyph } from "./status";
import { emptyStateShell } from "./empty-state";
import { expandableSection } from "./expandable";

test("the actions live in footer > actions and the icon in a hidden __icon", () => {
  const s = emptyStateShell({
    heading: "h2",
    title: "Nothing yet",
    icon: statusGlyph("danger"),
    classes: "pf-m-sm mine",
    ways: true,
  });
  assert.ok(s.root.classList.contains("pf-v6-c-empty-state"));
  assert.ok(s.root.classList.contains("mine"));
  assert.equal(s.icon?.getAttribute("aria-hidden"), "true");
  // Nodes are compared as booleans: a failed assert.equal on two DOM nodes diffs their object graphs
  // and takes the test process down with it.
  assert.ok(s.icon?.parentElement === s.header);
  assert.equal(s.title.tagName, "H2");
  assert.equal(s.title.parentElement?.className, "pf-v6-c-empty-state__title");
  assert.equal(s.title.textContent, "Nothing yet");
  assert.ok(s.actions.parentElement === s.footer);
  assert.ok(s.footer.parentElement === s.body.parentElement);
  assert.equal(s.actions.dataset.emptyWays, "");
});

test("without an icon there is no __icon", () => {
  const s = emptyStateShell({ heading: "h3" });
  assert.equal(s.icon, null);
  assert.equal(s.root.querySelector(".pf-v6-c-empty-state__icon"), null);
});

test("an expandable section keeps its toggle, region and expanded class in step", () => {
  const e = expandableSection("3 generated");
  document.body.append(e.el);
  assert.equal(e.toggle.getAttribute("aria-expanded"), "false");
  assert.equal(e.body.hidden, true);
  assert.equal(e.toggle.getAttribute("aria-controls"), e.body.id);
  assert.equal(e.body.getAttribute("aria-labelledby"), e.toggle.id);
  e.toggle.click();
  assert.equal(e.toggle.getAttribute("aria-expanded"), "true");
  assert.equal(e.body.hidden, false);
  assert.ok(e.el.classList.contains("pf-m-expanded"));
  e.set(false);
  assert.equal(e.el.classList.contains("pf-m-expanded"), false);
  e.el.remove();
});
