// notice-dom.test.ts - the explorer's one notice and its toggles' announced state.
//
// Pinned here: a failure is a toast AND text in the pane (the console's standard), a refusal is a
// warning toast, a hint is neither; the notice announces itself once, through the alert and not its
// host; and a toggle never looks pressed while announcing otherwise.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { setActive, setSelected, showStatus } from "./notice";

interface Heard {
  source: string;
  message: string;
  kind?: string;
  key?: string;
  toast?: boolean;
}

let heard: Heard[] = [];
const listen = (e: Event) => heard.push((e as CustomEvent<Heard>).detail);

describe("showStatus", () => {
  let host: HTMLElement;

  beforeEach(() => {
    heard = [];
    document.body.replaceChildren();
    host = document.createElement("div");
    host.hidden = true;
    document.body.append(host);
    document.addEventListener("magus:notify", listen);
  });
  afterEach(() => document.removeEventListener("magus:notify", listen));

  test("a failure is a danger alert in the pane and an error toast", () => {
    showStatus(host, "Could not read graph.json: bad token", "danger");
    assert.equal(host.hidden, false);
    const alert = host.querySelector<HTMLElement>(".pf-v6-c-alert");
    assert.ok(alert);
    assert.equal(alert.getAttribute("role"), "alert");
    assert.match(alert.textContent ?? "", /Danger alert/);
    assert.equal(heard.length, 1);
    assert.equal(heard[0].kind, "error");
    assert.equal(heard[0].toast, true);
    assert.equal(heard[0].source, "Graph");
    assert.match(heard[0].message, /bad token/);
  });

  test("a refusal is a warning alert and a warning toast", () => {
    showStatus(host, "layered layout is capped at 500 nodes", "warning");
    assert.ok(host.querySelector(".pf-m-warning"));
    assert.equal(heard.length, 1);
    assert.equal(heard[0].kind, "warn");
    assert.equal(heard[0].toast, true);
  });

  test("a hint is read in place and interrupts nobody", () => {
    showStatus(host, "Showing 12 projects.");
    const alert = host.querySelector<HTMLElement>(".pf-v6-c-alert");
    assert.ok(alert);
    assert.ok(alert.classList.contains("pf-m-info"));
    assert.equal(alert.getAttribute("role"), "status");
    assert.deepEqual(heard, []);
  });

  test("the host has no live role of its own, so nothing is announced twice", () => {
    showStatus(host, "Could not copy", "danger");
    assert.equal(host.hasAttribute("role"), false);
    assert.equal(host.hasAttribute("aria-live"), false);
  });

  test("the icon is a shape and the severity a word, never a letter", () => {
    showStatus(host, "Nothing depends on this", "info");
    const icon = host.querySelector(".pf-v6-c-alert__icon");
    assert.ok(icon);
    assert.ok(icon.querySelector("svg"));
    assert.doesNotMatch(icon.textContent ?? "", /\S/);
    assert.match(host.textContent ?? "", /Info alert/);
  });

  test("an empty message hides the host and removes the alert and its action", () => {
    showStatus(host, "Showing this workspace as of 10:02", "warning", {
      label: "Reconnect",
      run: () => {},
    });
    assert.ok(host.querySelector("button"));
    showStatus(host, "");
    assert.equal(host.hidden, true);
    assert.equal(host.children.length, 0);
    assert.equal(host.dataset.message, "");
  });

  test("an action runs once per click and does not outlive its message", () => {
    let ran = 0;
    showStatus(host, "Stopped answering", "warning", { label: "Reconnect", run: () => ran++ });
    host.querySelector<HTMLButtonElement>("button")?.click();
    assert.equal(ran, 1);
    showStatus(host, "Showing 12 projects.");
    assert.equal(host.querySelector("button"), null);
  });

  test("the message is recorded as given, for a writer checking its own text is still up", () => {
    showStatus(host, "first", "info");
    assert.equal(host.dataset.message, "first");
    showStatus(host, "second", "danger");
    assert.equal(host.dataset.message, "second");
  });

  test("a repeated failure answers in the pane each time under one notification key", () => {
    showStatus(host, "Could not copy: denied", "danger");
    showStatus(host, "Could not copy: denied", "danger");
    assert.equal(host.querySelectorAll(".pf-v6-c-alert").length, 1);
    // Both go to the notifier under one key; the store dedupes on it (lib/notifications).
    assert.equal(new Set(heard.map((h) => h.key)).size, 1);
  });
});

describe("toggles", () => {
  test("setActive sets the drawn mark and the announced state together", () => {
    const b = document.createElement("button");
    setActive(b, true);
    assert.equal(b.hasAttribute("data-active"), true);
    assert.equal(b.getAttribute("aria-pressed"), "true");
    setActive(b, false);
    assert.equal(b.hasAttribute("data-active"), false);
    assert.equal(b.getAttribute("aria-pressed"), "false");
  });

  test("setSelected does the same for a PF toggle-group button", () => {
    const b = document.createElement("button");
    setSelected(b, true);
    assert.equal(b.classList.contains("pf-m-selected"), true);
    assert.equal(b.getAttribute("aria-pressed"), "true");
    setSelected(b, false);
    assert.equal(b.classList.contains("pf-m-selected"), false);
    assert.equal(b.getAttribute("aria-pressed"), "false");
  });
});
