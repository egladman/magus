// help-popover-dom.test.ts - the "?" popover and, above all, what it costs. The Dashboard rebuilds its
// cards on every frame and attached a popover to each, so a popover that adds a node and four global
// listeners per call grew without bound (the audit counted 1,143 after minutes). These pin that
// attaching is free, that only an open popover holds a node or listeners, and that dispose releases
// everything.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { attachHelpPopover, createHelpButton } from "./help-popover";

interface Added {
  type: string;
  signal: AbortSignal | undefined;
}

// spyOnListeners records every listener added to document and window while it is installed. A
// listener added with an AbortSignal is live until that signal aborts.
function spyOnListeners(): { added: Added[]; live: () => number; restore: () => void } {
  const added: Added[] = [];
  const docAdd = document.addEventListener;
  const winAdd = window.addEventListener;
  const wrap =
    (target: EventTarget, orig: EventTarget["addEventListener"]) =>
    (type: string, fn: unknown, opts?: boolean | AddEventListenerOptions): void => {
      added.push({ type, signal: typeof opts === "object" ? opts.signal : undefined });
      orig.call(target, type, fn as EventListener, opts);
    };
  document.addEventListener = wrap(document, docAdd) as typeof document.addEventListener;
  window.addEventListener = wrap(window, winAdd) as typeof window.addEventListener;
  return {
    added,
    live: () => added.filter((a) => !a.signal?.aborted).length,
    restore: () => {
      document.addEventListener = docAdd;
      window.addEventListener = winAdd;
    },
  };
}

function popovers(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".pf-v6-c-popover")];
}

function trigger(title = "Explains the filter."): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.setAttribute("aria-label", "Filter syntax");
  b.title = title;
  document.body.append(b);
  return b;
}

describe("the help popover", () => {
  beforeEach(() => {
    for (const el of popovers()) el.remove();
  });
  afterEach(() => {
    for (const el of popovers()) el.remove();
  });

  test("attaching many popovers adds no global listeners and no nodes", () => {
    const spy = spyOnListeners();
    const nodes = document.body.querySelectorAll("*").length;
    const triggers: HTMLElement[] = [];
    const disposers: Array<() => void> = [];
    for (let i = 0; i < 200; i++) {
      const t = trigger();
      triggers.push(t);
      disposers.push(attachHelpPopover(t));
    }
    const grew = document.body.querySelectorAll("*").length - nodes;
    spy.restore();
    assert.equal(spy.added.length, 0, "no document or window listener per popover");
    assert.equal(popovers().length, 0, "no popover element until one opens");
    assert.equal(grew, triggers.length, "only the triggers themselves are in the document");
    for (const d of disposers) d();
    for (const t of triggers) t.remove();
  });

  test("a popover exists only while open, and the shared listeners only then", () => {
    const spy = spyOnListeners();
    const t = trigger();
    const dispose = attachHelpPopover(t);
    t.click();
    assert.equal(popovers().length, 1);
    assert.equal(spy.live(), 4, "click, keydown, resize, scroll: one set");
    t.click();
    assert.equal(popovers().length, 0, "removed on close, not hidden");
    assert.equal(spy.live(), 0, "every shared listener released with it");
    spy.restore();
    dispose();
    t.remove();
  });

  test("opening many in turn never holds more than one set", () => {
    const spy = spyOnListeners();
    const ts = Array.from({ length: 25 }, () => trigger());
    const disposers = ts.map((t) => attachHelpPopover(t));
    for (const t of ts) t.click();
    assert.equal(popovers().length, 1, "opening one closes the last");
    assert.equal(spy.live(), 4);
    spy.restore();
    for (const d of disposers) d();
    assert.equal(popovers().length, 0);
    assert.equal(spy.live(), 0);
    for (const t of ts) t.remove();
  });

  test("dispose closes an open popover, releases the trigger, and is safe twice", () => {
    const spy = spyOnListeners();
    const t = trigger("Original tooltip.");
    const dispose = attachHelpPopover(t);
    assert.equal(t.getAttribute("title"), null, "the native tooltip is stripped while attached");
    t.click();
    dispose();
    dispose();
    assert.equal(popovers().length, 0);
    assert.equal(spy.live(), 0);
    assert.equal(t.getAttribute("aria-haspopup"), null);
    assert.equal(t.getAttribute("aria-expanded"), null);
    assert.equal(t.getAttribute("title"), "Original tooltip.");
    spy.restore();
    t.click();
    assert.equal(popovers().length, 0, "a disposed trigger opens nothing");
    t.remove();
  });

  test("renders PF Popover markup named for the trigger and described by its text", () => {
    const t = trigger("Use key:value to filter.");
    const dispose = attachHelpPopover(t);
    t.click();
    const [pop] = popovers();
    assert.equal(pop.getAttribute("role"), "dialog");
    assert.equal(pop.getAttribute("aria-modal"), "true");
    assert.equal(pop.getAttribute("aria-label"), "Filter syntax");
    const body = pop.querySelector(".pf-v6-c-popover__body");
    assert.equal(body?.textContent, "Use key:value to filter.");
    assert.equal(pop.getAttribute("aria-describedby"), body?.id);
    assert.ok(pop.querySelector(".pf-v6-c-popover__arrow"));
    assert.equal(t.getAttribute("aria-expanded"), "true");
    assert.equal(t.getAttribute("aria-controls"), pop.id);
    assert.equal(document.activeElement, pop.querySelector(".pf-v6-c-popover__close button"));
    dispose();
    t.remove();
  });

  test("Escape closes it and returns focus to the trigger", () => {
    const t = trigger();
    const dispose = attachHelpPopover(t);
    t.click();
    document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    assert.equal(popovers().length, 0);
    assert.equal(t.getAttribute("aria-expanded"), "false");
    assert.equal(t.getAttribute("aria-controls"), null);
    assert.equal(document.activeElement, t);
    dispose();
    t.remove();
  });

  test("the close button, and a click outside, close it", () => {
    const t = trigger();
    const dispose = attachHelpPopover(t);
    t.click();
    popovers()[0].querySelector<HTMLButtonElement>(".pf-v6-c-popover__close button")?.click();
    assert.equal(popovers().length, 0);
    assert.equal(document.activeElement, t);

    t.click();
    popovers()[0].querySelector<HTMLElement>(".pf-v6-c-popover__body")?.click();
    assert.equal(popovers().length, 1, "a click inside leaves it open");
    document.body.click();
    assert.equal(popovers().length, 0);
    dispose();
    t.remove();
  });

  test("Tab stays on the close button, the popover's only control", () => {
    const t = trigger();
    const dispose = attachHelpPopover(t);
    t.click();
    const tab = new KeyboardEvent("keydown", { key: "Tab", bubbles: true, cancelable: true });
    document.activeElement?.dispatchEvent(tab);
    assert.equal(tab.defaultPrevented, true);
    const close = popovers()[0].querySelector(".pf-v6-c-popover__close button");
    assert.equal(document.activeElement, close);
    dispose();
    t.remove();
  });

  test("a trigger with no text attaches nothing", () => {
    const t = trigger("   ");
    const dispose = attachHelpPopover(t);
    t.click();
    assert.equal(popovers().length, 0);
    assert.equal(t.getAttribute("aria-haspopup"), null);
    dispose();
    t.remove();
  });

  test("explicit text and label win over the trigger's own", () => {
    const t = trigger("From the title.");
    const dispose = attachHelpPopover(t, { text: "Given text.", label: "Given name" });
    t.click();
    const [pop] = popovers();
    assert.equal(pop.querySelector(".pf-v6-c-popover__body")?.textContent, "Given text.");
    assert.equal(pop.getAttribute("aria-label"), "Given name");
    dispose();
    t.remove();
  });

  test("createHelpButton is a PF plain button with a name and a 24px hit-area hook", () => {
    const b = createHelpButton("What is a scope?");
    assert.equal(b.type, "button");
    assert.ok(b.classList.contains("pf-v6-c-button"));
    assert.ok(b.classList.contains("pf-m-plain"));
    assert.equal(b.getAttribute("aria-label"), "What is a scope?");
    assert.equal(b.querySelector("svg")?.getAttribute("aria-hidden"), "true");
    assert.ok("helpTrigger" in b.dataset, "the stylesheet sizes [data-help-trigger] to 24px");
  });
});
