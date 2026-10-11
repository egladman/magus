// alert-dom.test.ts - the inline Alert: PF markup, an aria-hidden icon, a screen-reader severity prefix,
// and a live role only where one is wanted.

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { inlineAlert, type AlertVariant } from "./alert";

const VARIANTS: AlertVariant[] = ["info", "success", "warning", "danger", "custom"];

describe("inlineAlert", () => {
  test("renders PF inline alert markup for every variant", () => {
    for (const variant of VARIANTS) {
      const el = inlineAlert({ variant, title: "T" });
      assert.ok(el.classList.contains("pf-v6-c-alert"), variant);
      assert.ok(el.classList.contains("pf-m-inline"), variant);
      assert.ok(el.classList.contains("pf-m-" + variant), variant);
      const icon = el.querySelector(".pf-v6-c-alert__icon svg");
      assert.ok(icon, variant);
      assert.equal(icon.getAttribute("aria-hidden"), "true", variant);
    }
  });

  test("the title carries the severity as hidden text, ahead of the words", () => {
    const el = inlineAlert({ variant: "danger", title: "Could not load runs" });
    const title = el.querySelector(".pf-v6-c-alert__title");
    assert.ok(title);
    assert.equal(title.querySelector(".pf-v6-screen-reader")?.textContent, "Danger alert:");
    assert.equal(title.textContent, "Danger alert: Could not load runs");
    assert.equal(el.getAttribute("aria-label"), "Danger alert");
  });

  test("danger is role=alert always; others are role=status only when live", () => {
    assert.equal(inlineAlert({ variant: "danger", title: "x" }).getAttribute("role"), "alert");
    assert.equal(
      inlineAlert({ variant: "danger", title: "x", live: false }).getAttribute("role"),
      "alert",
    );
    assert.equal(inlineAlert({ variant: "info", title: "x" }).getAttribute("role"), null);
    assert.equal(
      inlineAlert({ variant: "info", title: "x", live: true }).getAttribute("role"),
      "status",
    );
    assert.equal(
      inlineAlert({ variant: "success", title: "x", live: true }).getAttribute("role"),
      "status",
    );
  });

  test("a string body becomes a paragraph and a Node is appended as given", () => {
    const text = inlineAlert({ variant: "info", title: "x", body: "Plain" });
    assert.equal(text.querySelector(".pf-v6-c-alert__description p")?.textContent, "Plain");

    const code = document.createElement("code");
    code.textContent = "magus run";
    const node = inlineAlert({ variant: "info", title: "x", body: code });
    assert.equal(node.querySelector(".pf-v6-c-alert__description > code"), code);

    const bare = inlineAlert({ variant: "info", title: "x" });
    assert.equal(bare.querySelector(".pf-v6-c-alert__description"), null);
  });

  test("actions go in the action group in order", () => {
    const a = document.createElement("button");
    const b = document.createElement("button");
    const el = inlineAlert({ variant: "warning", title: "x", actions: [a, b] });
    const group = el.querySelector(".pf-v6-c-alert__action-group");
    assert.ok(group);
    assert.deepEqual([...group.children], [a, b]);
    const none = inlineAlert({ variant: "warning", title: "x", actions: [] });
    assert.equal(none.querySelector(".pf-v6-c-alert__action-group"), null);
  });
});
