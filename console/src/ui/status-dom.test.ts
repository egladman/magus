// status-dom.test.ts - a status is a shape and a word as well as a colour. These pin that no mark is
// built from the colour alone, which is the failure the helpers exist to prevent.

import assert from "node:assert/strict";
import { describe, test } from "node:test";
import { statusIcon, statusLabel, statusMark, statusText, type Status } from "./status";

const ALL: Status[] = ["success", "danger", "warning", "info", "running", "neutral"];

describe("status marks", () => {
  test("every icon is a hidden svg, so the word has to come from the text", () => {
    for (const s of ALL) {
      const icon = statusIcon(s);
      assert.equal(icon.getAttribute("aria-hidden"), "true", s);
      assert.ok(icon.querySelector("svg"), s);
      assert.equal(icon.textContent, "", s);
    }
  });

  test("the icon takes the PF colour modifier for its status; neutral takes none", () => {
    const content = (s: Status) => statusIcon(s).querySelector(".pf-v6-c-icon__content");
    assert.ok(content("danger")?.classList.contains("pf-m-danger"));
    assert.ok(content("success")?.classList.contains("pf-m-success"));
    assert.ok(content("warning")?.classList.contains("pf-m-warning"));
    assert.ok(content("running")?.classList.contains("pf-m-info"), "running is the info blue");
    assert.equal(content("neutral")?.className, "pf-v6-c-icon__content");
  });

  test("a running mark is the PF spinner", () => {
    assert.ok(statusIcon("running").querySelector("svg.pf-v6-c-spinner"));
    assert.equal(statusIcon("success").querySelector("svg.pf-v6-c-spinner"), null);
  });

  test("statusText is visually hidden and defaults to a word per status", () => {
    const failed = statusText("danger");
    assert.ok(failed.classList.contains("pf-v6-screen-reader"));
    assert.equal(failed.textContent, "Failed");
    assert.equal(statusText("danger", "Build failed").textContent, "Build failed");
    for (const s of ALL) assert.ok(statusLabel(s).length > 0, s);
    assert.equal(new Set(ALL.map(statusLabel)).size, ALL.length, "no two statuses share a word");
  });

  test("statusMark pairs the shape with the word", () => {
    const mark = statusMark("warning");
    assert.equal(mark.dataset.status, "warning");
    assert.ok(mark.querySelector("svg"));
    assert.equal(mark.textContent, "Warning");
    assert.equal(statusMark("warning", "Slow").textContent, "Slow");
  });
});
