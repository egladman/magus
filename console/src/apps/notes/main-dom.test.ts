// main-dom.test.ts - the Notes app's mount, driven by its demo set so no server is needed. What is
// pinned is what a reader and a screen reader meet: a list with one tab stop, a filter that counts
// and can be cleared, a note that opens with its heading focused, and the ways a body or a copy
// can fail being said out loud.

import assert from "node:assert/strict";
import { test as nodeTest } from "node:test";
import { activate } from "./main";
import type { AppInstance } from "../../desktop/standalone";
import { NOTIFY_EVENT } from "../../lib/notifications";
import { setDefaultHost } from "../../lib/settings";

let mounted: AppInstance | null = null;
let host: HTMLElement;

// Set up and torn down around each test here rather than in root-level hooks: with the runner's
// isolation off, a root-level hook runs for every test in the process, and this one sets #demo.
function test(name: string, fn: () => Promise<void> | void): void {
  nodeTest(name, async () => {
    localStorage.clear();
    sessionStorage.clear();
    document.body.replaceChildren();
    location.hash = "#demo";
    host = document.createElement("div");
    document.body.append(host);
    try {
      await fn();
    } finally {
      mounted?.deactivate();
      mounted = null;
      location.hash = "";
    }
  });
}

async function settle(turns = 6): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

async function mount(): Promise<void> {
  mounted = activate(host);
  await settle();
}

const rows = (): HTMLElement[] => [
  ...host.querySelectorAll<HTMLElement>(".console-notes-app__note"),
];
const search = (): HTMLInputElement => {
  const input = host.querySelector<HTMLInputElement>("input[type=search]");
  assert.ok(input);
  return input;
};
const detail = (): HTMLElement => {
  const el = host.querySelector<HTMLElement>(".console-notes-app__detail");
  assert.ok(el);
  return el;
};
const type = (value: string): void => {
  search().value = value;
  search().dispatchEvent(new Event("input"));
};
const key = (el: Element, k: string): void => {
  el.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true }));
};

test("the list is real lists under store headings, with exactly one tab stop", async () => {
  await mount();
  assert.ok(host.querySelector("section[aria-label=Notes]"));
  assert.equal(host.querySelectorAll("h2 > button[aria-expanded][aria-controls]").length, 2);
  for (const ul of host.querySelectorAll("ul.pf-v6-c-data-list")) {
    for (const child of ul.children) assert.equal(child.tagName, "LI");
    assert.ok(ul.getAttribute("aria-label"));
  }
  const stops = [...host.querySelectorAll<HTMLElement>("[data-roving]")].filter(
    (b) => b.tabIndex === 0,
  );
  assert.equal(stops.length, 1);
  // A row is PF's clickable data-list item: the item is the control, named by its title.
  const first = rows()[0];
  assert.ok(first?.classList.contains("pf-m-clickable"));
  assert.ok(
    first?.querySelector(".pf-v6-c-data-list__item-row > .pf-v6-c-data-list__item-content"),
  );
  assert.ok(first?.querySelector(".pf-v6-c-data-list__cell"));
  assert.equal(
    first?.getAttribute("aria-labelledby"),
    first?.querySelector(".console-notes-app__note-title")?.id,
  );
});

// A failed assert.equal on two DOM nodes diffs their whole object graphs, which takes the test
// process down with it. Focus is compared as a boolean for that reason.
const focused = (el: Element | null | undefined): boolean => !!el && document.activeElement === el;

test("arrow keys, Home and End move through headings and rows", async () => {
  await mount();
  const all = [...host.querySelectorAll<HTMLButtonElement>("[data-roving]")];
  all[0]?.focus();
  assert.ok(focused(all[0]), "the heading takes focus");
  key(all[0] as Element, "ArrowDown");
  assert.ok(focused(all[1]), "ArrowDown moves to the next stop");
  assert.equal(all[1]?.tabIndex, 0);
  assert.equal(all[0]?.tabIndex, -1);
  key(all[1] as Element, "End");
  assert.ok(focused(all[all.length - 1]), "End moves to the last");
  key(all[all.length - 1] as Element, "Home");
  assert.ok(focused(all[0]), "Home moves to the first");
});

test("folding a store keeps focus on its heading and hides its rows from the tab stops", async () => {
  await mount();
  const head = host.querySelector<HTMLButtonElement>('[data-roving="store:private"]');
  assert.ok(head);
  head.focus();
  head.click();
  const again = host.querySelector<HTMLButtonElement>('[data-roving="store:private"]');
  assert.equal(again?.getAttribute("aria-expanded"), "false");
  assert.ok(focused(again), "focus stays on the heading");
  const controls = again?.getAttribute("aria-controls") ?? "";
  assert.equal(document.querySelector<HTMLElement>("#" + CSS.escape(controls))?.hidden, true);
});

test("the filter counts, announces and clears; an empty result is one small empty state", async () => {
  await mount();
  const count = host.querySelector(".console-notes-app__count");
  assert.equal(count?.getAttribute("aria-live"), "polite");
  assert.equal(count?.textContent, "6 notes");

  type("auth");
  assert.match(count?.textContent ?? "", /^\d+ of 6 notes$/);
  const clear = host.querySelector<HTMLButtonElement>("button[aria-label='Clear filter']");
  assert.equal(clear?.hidden, false);

  type("zzz-nothing");
  assert.equal(count?.textContent, "0 of 6 notes");
  assert.equal(
    host.querySelectorAll(".console-notes-app__list .pf-v6-c-empty-state.pf-m-sm").length,
    1,
  );
  assert.equal(host.querySelectorAll("[data-roving]").length, 0);

  const action = [...host.querySelectorAll("button")].find((b) => b.textContent === "Clear filter");
  assert.ok(action);
  action.click();
  assert.equal(search().value, "");
  assert.equal(count?.textContent, "6 notes");
});

test("Escape in the filter clears it", async () => {
  await mount();
  type("auth");
  key(search(), "Escape");
  assert.equal(search().value, "");
});

test("opening a note shows its words, not a hue: scope label, status words, tags as a list", async () => {
  await mount();
  const row = rows().find((r) => r.dataset.name === "rejected-a-second-service-token");
  assert.ok(row);
  row.click();
  const text = detail().textContent ?? "";
  assert.match(text, /Shared/);
  assert.match(text, /211 days behind its subject/);
  assert.match(text, /dangling/);
  assert.match(text, /Edit from a terminal/);
  assert.doesNotMatch(text, /Edit it/);
  assert.equal(detail().querySelectorAll("ul[aria-label=Tags] > li").length, 2);
  assert.equal(row.getAttribute("aria-current"), "true");
  assert.equal(detail().querySelector("code")?.textContent?.includes("`"), false);
});

test("list and detail say the same thing about how far behind a note is", async () => {
  await mount();
  const row = rows().find((r) => r.dataset.name === "rejected-a-second-service-token");
  assert.ok(row);
  assert.match(row.textContent ?? "", /211 days behind its subject/);
  row.click();
  assert.match(detail().textContent ?? "", /211 days behind its subject/);
});

test("over a covering overlay the list and filter are inert; Escape and Back restore the row", async () => {
  await mount();
  detail().style.position = "absolute";
  const row = rows()[0];
  assert.ok(row);
  row.click();
  const pane = host.querySelector(".console-notes-app__pane");
  const bar = host.querySelector(".console-notes-app__bar");
  assert.equal(pane?.hasAttribute("inert"), true);
  assert.equal(bar?.hasAttribute("inert"), true);
  assert.equal(document.activeElement?.tagName, "H2");

  key(detail(), "Escape");
  assert.equal(detail().hasAttribute("data-open"), false);
  assert.equal(pane?.hasAttribute("inert"), false);
  assert.ok(focused(row), "Escape returns focus to the row");

  row.click();
  host.querySelector<HTMLButtonElement>(".console-notes-app__detail-bar button")?.click();
  assert.ok(focused(row), "Back returns focus to the row");
});

test("beside the list the detail leaves focus on the row and nothing is inert", async () => {
  await mount();
  const row = rows()[0];
  assert.ok(row);
  row.focus();
  row.click();
  assert.equal(host.querySelector(".console-notes-app__pane")?.hasAttribute("inert"), false);
  assert.ok(focused(row), "focus stays on the row");
});

test("a body shows a skeleton while it loads; a failure is a danger alert whose Retry loads it", async () => {
  location.hash = "";
  setDefaultHost("127.0.0.1:7391");
  const realFetch = globalThis.fetch;
  let getNote = 0;
  const json = (body: unknown, status = 200): Promise<Response> =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status,
        headers: { "content-type": "application/json" },
      }),
    );
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("NotesService/ListNotes")) {
      return json({
        notes: [{ name: "a", title: "A note", scope: "SCOPE_SHARED", path: "notes/a.md" }],
        stores: [{ scope: "SCOPE_SHARED", declared: true }],
      });
    }
    if (url.includes("NotesService/GetNote")) {
      getNote++;
      if (getNote === 1) return json({ code: "unavailable", message: "store busy" }, 503);
      return json({ name: "a", title: "A note", body: "Hello there." });
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
  try {
    await mount();
    rows()[0]?.click();
    assert.equal(detail().querySelector("[aria-busy=true]") !== null, true, "busy while loading");
    assert.ok(detail().querySelector(".pf-v6-c-skeleton"));
    await settle(20);

    const alert = detail().querySelector(".pf-v6-c-alert.pf-m-danger");
    assert.ok(alert, "a failed body is an inline danger alert");
    assert.equal(detail().querySelector("[aria-busy=true]"), null);
    const retry = [...detail().querySelectorAll("button")].find((b) => b.textContent === "Retry");
    assert.ok(retry);
    retry.click();
    await settle(20);
    assert.equal(detail().querySelector(".pf-v6-c-alert.pf-m-danger"), null);
    assert.match(
      detail().querySelector(".console-notes-app__prose")?.textContent ?? "",
      /Hello there/,
    );
  } finally {
    globalThis.fetch = realFetch;
    setDefaultHost("");
  }
});

test("a copy that the browser refuses is reported, not silent", async () => {
  await mount();
  const seen: string[] = [];
  const onNotify = (e: Event): void => {
    seen.push((e as CustomEvent).detail?.message ?? "");
  };
  document.addEventListener(NOTIFY_EVENT, onNotify);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: () => Promise.reject(new Error("denied")) },
  });
  rows()[0]?.click();
  detail().querySelector<HTMLButtonElement>("button[aria-label='Copy path']")?.click();
  await settle();
  document.removeEventListener(NOTIFY_EVENT, onNotify);
  assert.equal(seen.length, 1);
  assert.match(seen[0] ?? "", /Could not copy the path \(denied\)/);
});

test("a copy that works is announced to a screen reader", async () => {
  await mount();
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: () => Promise.resolve() },
  });
  rows()[0]?.click();
  detail().querySelector<HTMLButtonElement>("button[aria-label='Copy edit command']")?.click();
  await settle();
  assert.equal(
    host.querySelector(".pf-v6-screen-reader[role=status]")?.textContent,
    "Copied the edit command.",
  );
});

test("a captured review thread reads as quoted, with its resolved entry marked in words", async () => {
  await mount();
  const row = rows().find((r) => r.dataset.name === "review-thread-claims-audience");
  assert.ok(row);
  assert.match(row.textContent ?? "", /Quoted/);
  row.click();
  await settle();
  assert.ok(detail().querySelector(".console-notes-app__thread"));
  const resolved = detail().querySelector("[data-resolved] .pf-v6-c-label.pf-m-green");
  assert.match(resolved?.textContent ?? "", /Resolved/);
  assert.equal(detail().querySelectorAll("h3.console-notes-app__thread-file").length, 2);
});

test("with nothing selected the pane is a small empty state, not bare text", async () => {
  await mount();
  const empty = detail().querySelector(".pf-v6-c-empty-state.pf-m-sm");
  assert.match(empty?.textContent ?? "", /No note selected/);
});
