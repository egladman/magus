// notifications-dom.test.ts - the bell's history panel as the PF Notification drawer it is: a titled
// dialog, one list item per entry named by its message, times that keep up with the clock, and a Clear
// all the reader can take back.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { mountNotificationCenter, type NotificationCenter } from "./notifications";
import { mountToastGroup } from "./toast";
import { must } from "./guards";

const panel = (): HTMLElement => must(document.getElementById("console-notifypanel"));
const entries = (): HTMLElement[] => [
  ...panel().querySelectorAll<HTMLElement>(".pf-v6-c-notification-drawer__list-item"),
];

describe("the notification drawer", () => {
  let center: NotificationCenter;

  // Inside the suite on purpose: the console's tests share one process, and a hook at the top of the
  // file would run around every test in every file.
  beforeEach(() => {
    document.body.replaceChildren();
    mountToastGroup().replaceChildren();
    center = mountNotificationCenter();
  });

  afterEach(() => {
    // An open panel holds a live interval, which would keep the test process from ever exiting.
    center.close();
    document.getElementById("console-notifypanel")?.remove();
    document.getElementById("console-notifybtn")?.remove();
    mountToastGroup().replaceChildren();
  });

  test("is a PF drawer: a dialog named by an h2, an svg close, a list of items", () => {
    center.store.notify({ source: "Runs", message: "build failed", kind: "error" });
    center.open();
    const p = panel();
    assert.ok(p.classList.contains("pf-v6-c-notification-drawer"));
    assert.equal(p.getAttribute("role"), "dialog");
    const title = must(p.querySelector<HTMLElement>(".pf-v6-c-notification-drawer__header-title"));
    assert.equal(title.tagName, "H2");
    assert.equal(p.getAttribute("aria-labelledby"), title.id);
    const close = must(
      p.querySelector<HTMLElement>(".pf-v6-c-notification-drawer__header-action-close button"),
    );
    assert.equal(close.getAttribute("aria-label"), "Close notifications");
    assert.ok(close.querySelector("svg"), "the close mark is an svg, not a text glyph");
    assert.equal(p.querySelector(".pf-v6-c-notification-drawer__list")?.tagName, "UL");
  });

  test("an entry carries its status in a shape and a word, and its dismiss is named by it", () => {
    center.store.notify({ source: "Runs", message: "build failed", kind: "error" });
    center.store.notify({ source: "Settings", message: "applied", kind: "ok" });
    center.open();
    const [ok, failed] = entries();
    assert.ok(ok.classList.contains("pf-m-success"));
    assert.ok(failed.classList.contains("pf-m-danger"));
    assert.ok(failed.querySelector(".pf-v6-c-notification-drawer__list-item-header-icon svg"));
    const title = must(
      failed.querySelector<HTMLElement>(".pf-v6-c-notification-drawer__list-item-header-title"),
    );
    assert.equal(title.tagName, "H3");
    assert.match(title.textContent ?? "", /Danger notification:\s+build failed/);
    const dismiss = must(
      failed.querySelector<HTMLElement>(".pf-v6-c-notification-drawer__list-item-action button"),
    );
    assert.equal(dismiss.getAttribute("aria-label"), "Dismiss notification: build failed");
    dismiss.click();
    assert.equal(entries().length, 1);
  });

  test("an empty history is a PF empty state", () => {
    center.open();
    assert.ok(panel().querySelector(".pf-v6-c-empty-state"));
    assert.equal(entries().length, 0);
  });

  test("the times are rewritten by a ticker that runs only while the panel is open", () => {
    // The runner's own clock is left alone: the interval is captured, not scheduled.
    const real = { set: globalThis.setInterval, clear: globalThis.clearInterval };
    let tick: (() => void) | undefined;
    const cleared: unknown[] = [];
    globalThis.setInterval = ((fn: () => void) => {
      tick = fn;
      return 7;
    }) as unknown as typeof setInterval;
    globalThis.clearInterval = ((id: unknown) => {
      cleared.push(id);
    }) as typeof clearInterval;
    try {
      center.store.notify({ source: "Runs", message: "done", at: Date.now() - 125_000 });
      center.open();
      assert.ok(tick, "opening starts the ticker");
      const time = must(panel().querySelector<HTMLElement>("time[data-time]"));
      time.textContent = "stale";
      tick?.();
      assert.equal(time.textContent, "2m ago");
      center.close();
      assert.deepEqual(cleared.includes(7), true, "closing stops it");
    } finally {
      globalThis.setInterval = real.set;
      globalThis.clearInterval = real.clear;
    }
  });

  test("Clear all raises a toast whose Undo puts the history back", () => {
    center.store.notify({ source: "Runs", message: "one", at: 1 });
    center.store.notify({ source: "Runs", message: "two", at: 2 });
    center.open();
    const clear = must(
      [...panel().querySelectorAll<HTMLButtonElement>("button")].find(
        (b) => b.textContent === "Clear all",
      ),
    );
    clear.click();
    assert.equal(center.store.list().length, 0);

    const undo = must(
      [...document.querySelectorAll<HTMLButtonElement>("#console-toasts button")].find(
        (b) => b.textContent === "Undo",
      ),
    );
    undo.click();
    assert.deepEqual(
      center.store.list().map((n) => n.message),
      ["two", "one"],
      "restored in time order",
    );
  });
});
