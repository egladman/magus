// querybuilder-dom.test.ts - the question builder's markup, as a keyboard and screen-reader reader
// meets it: the WAI-ARIA tablist, panels that name their tab, real icons on every close control, the
// views as PF menu items that explain themselves when they cannot run, and a copy failure that is
// said in the panel and as a notification.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { createQueryBuilder, type GraphCapabilities, type QueryBuilder } from "./querybuilder";

const NOTIFY = "magus:notify";

let ran: string[] = [];
let applied: string[] = [];

function caps(over: Partial<GraphCapabilities> = {}): GraphCapabilities {
  return {
    flavor: "knowledge",
    hasDurations: false,
    hasAffectedSet: false,
    live: false,
    affectedFallback: "",
    ...over,
  };
}

function mount(c: GraphCapabilities = caps()): {
  qb: QueryBuilder;
  q: <T extends Element>(s: string) => T;
} {
  const qb = createQueryBuilder({
    kinds: () => ["spell", "target"],
    relations: () => ["uses"],
    projects: () => ["console"],
    currentQuery: () => "kind:spell",
    applyQuery: (query) => applied.push(query),
    matchCount: () => ({ matched: 2, total: 9 }),
    runView: (v) => ran.push(v),
    radialPick: () => ran.push("radial-pick"),
    capabilities: () => c,
  });
  document.body.append(qb.el);
  qb.open();
  const q = <T extends Element>(sel: string): T => {
    const el = qb.el.querySelector<T>(sel);
    assert.ok(el, sel);
    return el;
  };
  return { qb, q };
}

function press(el: Element, key: string): void {
  el.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
}

describe("the question builder", () => {
  beforeEach(() => {
    document.body.replaceChildren();
    ran = [];
    applied = [];
  });
  afterEach(() => {
    Reflect.deleteProperty(navigator, "clipboard");
  });

  test("its tabs are a tablist whose tabs control labelled panels", () => {
    const { q } = mount();
    const list = q<HTMLElement>('[role="tablist"]');
    assert.equal(list.tagName, "UL");
    assert.ok(list.getAttribute("aria-label"));
    for (const li of list.querySelectorAll("li"))
      assert.equal(li.getAttribute("role"), "presentation", "items must not break the tablist");
    const tabs = [...list.querySelectorAll<HTMLElement>('[role="tab"]')];
    assert.equal(tabs.length, 2);
    for (const tab of tabs) {
      const panel = document.getElementById(tab.getAttribute("aria-controls") ?? "");
      assert.ok(panel, "aria-controls names a panel");
      assert.equal(panel.getAttribute("role"), "tabpanel");
      assert.equal(panel.getAttribute("aria-labelledby"), tab.id);
      assert.equal(panel.tabIndex, 0, "a panel with no focusable first child must be reachable");
    }
  });

  test("one tab is in the tab order and the arrow keys move between them", () => {
    const { q } = mount();
    const [filter, view] = [
      ...q<HTMLElement>('[role="tablist"]').querySelectorAll<HTMLElement>('[role="tab"]'),
    ];
    assert.equal(filter.getAttribute("aria-selected"), "true");
    assert.equal(filter.tabIndex, 0);
    assert.equal(view.tabIndex, -1);
    assert.ok(
      filter.closest("li")?.classList.contains("pf-m-current"),
      "PF marks the item current",
    );

    press(filter, "ArrowRight");
    assert.equal(view.getAttribute("aria-selected"), "true");
    assert.equal(view.tabIndex, 0);
    assert.equal(filter.tabIndex, -1);
    assert.equal(document.activeElement, view);
    assert.equal(document.getElementById(view.getAttribute("aria-controls") ?? "")?.hidden, false);

    press(view, "ArrowRight");
    assert.equal(filter.getAttribute("aria-selected"), "true", "wraps around");
    press(filter, "End");
    assert.equal(view.getAttribute("aria-selected"), "true");
    press(view, "Home");
    assert.equal(filter.getAttribute("aria-selected"), "true");
  });

  test("no control is a text character standing in for an icon", () => {
    const { q, qb } = mount();
    const add = qb.el.querySelector<HTMLElement>(".console-graph-qb__add");
    assert.ok(add);
    add.click();
    for (const sel of [".console-graph-qb__close", ".console-graph-qb__del"]) {
      const b = q<HTMLElement>(sel);
      assert.ok(b.querySelector("svg"), sel + " draws an svg");
      assert.ok(!(b.textContent ?? "").includes("×"), sel + " has no multiplication sign");
      assert.ok(b.getAttribute("aria-label"), sel + " is named");
    }
  });

  test("terms are named by their position, so six rows are not six 'Value' fields", () => {
    const { qb } = mount();
    qb.el.querySelector<HTMLElement>(".console-graph-qb__add")?.click();
    const names = [...qb.el.querySelectorAll(".console-graph-qb__row [aria-label]")].map((e) =>
      e.getAttribute("aria-label"),
    );
    assert.ok(names.some((n) => /term 1/.test(n ?? "")));
    assert.ok(names.some((n) => /term 2/.test(n ?? "")));
    const neg = qb.el.querySelector(".console-graph-qb__neg");
    assert.match(neg?.getAttribute("aria-label") ?? "", /^not/, "the visible word is in the name");
    assert.equal(neg?.textContent, "not", "the word does not flip with the state");
  });

  test("the views are PF menu items, and one the graph cannot answer says why and runs nothing", () => {
    const { q, qb } = mount(caps({ flavor: "knowledge" }));
    q<HTMLElement>('[role="tab"][id$="view"]').click();
    const items = [
      ...qb.el.querySelectorAll<HTMLElement>("#console-graph-qb-panel-view .pf-v6-c-menu__item"),
    ];
    assert.equal(items.length, 8, "one row per view");
    const cycles = items.find((i) => /circular/.test(i.textContent ?? ""));
    assert.ok(cycles);
    assert.equal(cycles.getAttribute("aria-disabled"), "true");
    assert.match(cycles.textContent ?? "", /Switch to the target graph/);
    cycles.click();
    assert.deepEqual(ran, []);
    const hubs = items.find((i) => /everything depend on/.test(i.textContent ?? ""));
    assert.ok(hubs);
    hubs.click();
    assert.deepEqual(ran, ["hubs"]);
  });

  test("the examples are PF menu items that load a filter into the rows", () => {
    const { qb } = mount();
    const first = qb.el.querySelector<HTMLElement>(
      ".console-graph-qb__examples .pf-v6-c-menu__item",
    );
    assert.ok(first);
    first.click();
    assert.equal(applied.at(-1), "kind:target");
  });

  test("a failed copy is said in the panel and sent as a notification", async () => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: () => Promise.reject(new Error("denied")) },
    });
    const heard: Array<{ message: string; kind?: string; toast?: boolean }> = [];
    const listen = (e: Event) => heard.push((e as CustomEvent).detail);
    document.addEventListener(NOTIFY, listen);
    try {
      const { qb } = mount();
      const copy = [...qb.el.querySelectorAll<HTMLElement>(".console-graph-qb__copyrow button")][0];
      copy.click();
      await new Promise((r) => setTimeout(r, 0));
      const alert = qb.el.querySelector<HTMLElement>(".console-graph-qb__notice [role='alert']");
      assert.ok(alert, "an inline alert in the panel");
      assert.match(alert.textContent ?? "", /Danger alert/);
      assert.match(alert.textContent ?? "", /denied/);
      assert.equal(heard.length, 1);
      assert.equal(heard[0].kind, "error");
      assert.equal(heard[0].toast, true);
      assert.match(heard[0].message, /Could not copy/);
    } finally {
      document.removeEventListener(NOTIFY, listen);
    }
  });
});
