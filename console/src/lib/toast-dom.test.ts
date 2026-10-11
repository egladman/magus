// toast-dom.test.ts - the toast Alert group: one persistent live region, PF alerts stacked newest first,
// a close button on every toast, and a timer that waits while the reader is looking at one.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { showCountdownToast, showRefreshToast, showToast } from "./refresh-toast";
import { mountToastGroup, pushToast, renderTransientToast, TOAST_MS } from "./toast";

const ACTION = ".pf-v6-c-alert__action-group button";
const MESSAGE = ".pf-v6-c-alert__description";

const sleep = (ms: number): Promise<void> => new Promise((r) => setTimeout(r, ms));

function group(): HTMLElement {
  return mountToastGroup();
}

function items(): HTMLElement[] {
  return [...group().querySelectorAll<HTMLElement>(".pf-v6-c-alert-group__item")].filter(
    (i) => !i.classList.contains("pf-m-outgoing"),
  );
}

function titles(): string[] {
  return items().map((i) => i.querySelector(".pf-v6-c-alert__title")?.textContent ?? "");
}

// Clears by removing, not by dismissing: a dismissal's exit timer would outlive the test.
function reset(): void {
  group().replaceChildren();
}

describe("the toast group", () => {
  afterEach(reset);

  test("is one persistent polite live region", () => {
    const g = group();
    assert.equal(g.id, "console-toasts");
    assert.ok(g.classList.contains("pf-v6-c-alert-group"));
    assert.ok(g.classList.contains("pf-m-toast"));
    assert.equal(g.getAttribute("aria-live"), "polite");
    assert.equal(g.getAttribute("aria-atomic"), "false", "an added toast is read alone");
    assert.equal(mountToastGroup(), g, "asking again returns the same group");
    assert.equal(document.querySelectorAll("#console-toasts").length, 1);
  });

  test("is PF's list: a ul of li items", () => {
    const g = group();
    assert.equal(g.tagName, "UL");
    assert.equal(g.getAttribute("role"), "list");
    pushToast({ source: "Runs", message: "Done.", kind: "ok", ms: 0 });
    const [item] = items();
    assert.equal(item.tagName, "LI");
    assert.ok(item.parentElement === g);
  });

  test("a toast is a PF alert: hidden icon, severity prefix, close button", () => {
    pushToast({ source: "Runs", message: "Could not load.", kind: "error", ms: 0 });
    const [item] = items();
    const alert = item.querySelector(".pf-v6-c-alert");
    assert.ok(alert?.classList.contains("pf-m-danger"));
    assert.equal(alert?.getAttribute("role"), "alert", "an error is announced assertively");
    const icon = alert?.querySelector(".pf-v6-c-alert__icon svg");
    assert.equal(icon?.getAttribute("aria-hidden"), "true");
    assert.equal(item.querySelector(".pf-v6-c-alert__title")?.textContent, "Danger alert: Runs");
    assert.equal(item.querySelector(".pf-v6-c-alert__description")?.textContent, "Could not load.");
    const close = item.querySelector(".pf-v6-c-alert__action button");
    assert.equal(close?.getAttribute("aria-label"), "Close danger alert: Runs");
  });

  test("kinds map to PF variants, and only an error takes role=alert", () => {
    const variant = (kind: "ok" | "warn" | "error" | "info"): [string, string | null] => {
      reset();
      pushToast({ source: "S", message: "m " + kind, kind, ms: 0 });
      const alert = items()[0].querySelector(".pf-v6-c-alert");
      const mod = [...(alert?.classList ?? [])].find((c) => c.startsWith("pf-m-")) ?? "";
      return [mod, alert?.getAttribute("role") ?? null];
    };
    assert.deepEqual(variant("ok"), ["pf-m-success", null]);
    assert.deepEqual(variant("warn"), ["pf-m-warning", null]);
    assert.deepEqual(variant("error"), ["pf-m-danger", "alert"]);
    assert.deepEqual(variant("info"), ["pf-m-info", null]);
  });

  test("the newest toast is on top and toasts stack rather than replace", () => {
    pushToast({ source: "A", message: "first", ms: 0 });
    pushToast({ source: "B", message: "second", ms: 0 });
    assert.deepEqual(titles(), ["Success alert: B", "Success alert: A"]);
  });

  test("the stack is capped, dropping the oldest", () => {
    for (let i = 0; i < 8; i++) pushToast({ source: "S" + i, message: "m", ms: 0 });
    assert.equal(items().length, 5);
    assert.equal(titles()[0], "Success alert: S7");
    assert.equal(titles()[4], "Success alert: S3");
  });

  test("an identical live toast is not shown twice", () => {
    pushToast({ source: "A", message: "same", kind: "warn", ms: 0 });
    pushToast({ source: "A", message: "same", kind: "warn", ms: 0 });
    assert.equal(items().length, 1);
  });

  test("the close button dismisses it and runs onDismiss", () => {
    let dismissed = 0;
    pushToast({ source: "A", message: "x", ms: 0, onDismiss: () => dismissed++ });
    items()[0].querySelector<HTMLButtonElement>(".pf-v6-c-alert__action button")?.click();
    assert.equal(items().length, 0, "it leaves the live stack at once");
    assert.equal(dismissed, 1);
  });

  test("an action button runs its handler", () => {
    let ran = 0;
    pushToast({
      source: "A",
      message: "x",
      ms: 0,
      actions: [{ label: "Retry", run: () => void ran++ }],
    });
    const button = items()[0].querySelector<HTMLButtonElement>(ACTION);
    assert.equal(button?.textContent, "Retry");
    button?.click();
    assert.equal(ran, 1);
  });

  test("dismisses itself after its time", async () => {
    pushToast({ source: "A", message: "brief", ms: 30 });
    assert.equal(items().length, 1);
    await sleep(80);
    assert.equal(items().length, 0);
  });

  test("waits while the pointer is on it and resumes when it leaves", async () => {
    pushToast({ source: "A", message: "read me", ms: 40 });
    const [item] = items();
    item.dispatchEvent(new MouseEvent("mouseenter"));
    await sleep(120);
    assert.equal(items().length, 1, "still up while hovered");
    item.dispatchEvent(new MouseEvent("mouseleave"));
    await sleep(1300);
    assert.equal(items().length, 0, "goes after leaving");
  });

  test("waits while focus is inside it", async () => {
    pushToast({ source: "A", message: "focus me", ms: 40 });
    const [item] = items();
    item.dispatchEvent(new FocusEvent("focusin", { bubbles: true }));
    await sleep(120);
    assert.equal(items().length, 1);
    item.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
    await sleep(1300);
    assert.equal(items().length, 0);
  });

  test("ms 0 keeps it until closed", async () => {
    pushToast({ source: "A", message: "stays", ms: 0 });
    await sleep(60);
    assert.equal(items().length, 1);
  });

  test("the default is eight seconds", () => {
    assert.equal(TOAST_MS, 8000);
  });
});

describe("the toast entry points", () => {
  afterEach(reset);

  test("renderTransientToast keeps its signature and stacks", () => {
    renderTransientToast("Settings", "Saved.", "ok", undefined, 0);
    const link = { label: "View", run: () => {} };
    renderTransientToast("Settings", "Dropped 2 keys.", "warn", link, 0);
    assert.equal(items().length, 2);
    assert.equal(items()[0].querySelector(ACTION)?.textContent, "View");
  });

  test("showToast raises a toast and records it in the history", () => {
    const seen: string[] = [];
    const on = (e: Event): void => {
      seen.push((e as CustomEvent<{ message: string }>).detail.message);
    };
    document.addEventListener("magus:notify", on);
    showToast("Settings", "Applied.", "ok", { ms: 0 });
    document.removeEventListener("magus:notify", on);
    assert.deepEqual(titles(), ["Success alert: Settings"]);
    assert.deepEqual(seen, ["Applied."]);
  });

  test("the refresh prompt is dismissible, idempotent, and reloads on its button", () => {
    showRefreshToast("Dashboard", "A new version is available.");
    showRefreshToast("Dashboard", "Again.");
    assert.equal(items().length, 1);
    const button = items()[0].querySelector(ACTION);
    assert.equal(button?.textContent, "Refresh");
    assert.ok(items()[0].querySelector(".pf-v6-c-alert__action button"), "has a close button");
    assert.ok(items()[0].querySelector(".pf-v6-c-alert.pf-m-info"));
    items()[0].querySelector<HTMLButtonElement>(".pf-v6-c-alert__action button")?.click();
    assert.equal(items().length, 0);
  });

  test("the countdown ticks in place and cancelling stops it", async () => {
    let elapsed = 0;
    const cancel = showCountdownToast(
      "Dashboard",
      (s) => `Reloading in ${s}s`,
      2,
      () => elapsed++,
    );
    assert.equal(items().length, 1);
    assert.equal(items()[0].querySelector(MESSAGE)?.textContent, "Reloading in 2s");
    await sleep(1100);
    assert.equal(items()[0].querySelector(MESSAGE)?.textContent, "Reloading in 1s");
    assert.equal(items().length, 1, "one toast, rewritten");
    cancel();
    assert.equal(items().length, 0);
    await sleep(1200);
    assert.equal(elapsed, 0, "a cancelled countdown never fires");
  });

  test("closing the countdown toast cancels it too", async () => {
    let elapsed = 0;
    showCountdownToast(
      "Dashboard",
      (s) => `in ${s}`,
      1,
      () => elapsed++,
    );
    items()[0].querySelector<HTMLButtonElement>(".pf-v6-c-alert__action button")?.click();
    await sleep(1200);
    assert.equal(elapsed, 0);
  });
});
