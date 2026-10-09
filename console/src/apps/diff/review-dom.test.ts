// review-dom.test.ts - the Diff app's controls and failures, mounted: the overview's way out, the
// buttons that stand in for keys, the composer's accessible parts, and the toast plus the alert
// every failed send, peek or session read raises. document/window come from test-setup.mjs.
//
// Two mounts are used. The showcase (#demo) needs no server, so it carries the control tests. The
// failure tests mount against a fake server (#port=) that serves the showcase's own fixtures and
// refuses the one call under test, because a failure that only the showcase's in-memory path can
// never reach is not a failure worth testing.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { dispatchCommand, listCommands } from "../../desktop/commands";
import { NOTIFY_EVENT } from "../../lib/notifications";
import { activate } from "./main";
import { demoReview, demoSession } from "./demo";
import { DEMO_FILES } from "./gen/demo";

const realFetch = globalThis.fetch;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
  globalThis.fetch = (() => {
    throw new Error("demo mode must not reach the network");
  }) as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  location.hash = "";
});

async function settle(turns = 14): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// Left in focus mode, the preference would follow every test after this one: it is a module-level
// cell and these tests share a process.
async function setFocusMode(on: boolean): Promise<void> {
  const root = document.querySelector<HTMLElement>(".console-diff-layout");
  if ((root?.dataset.focus === "on") === on) return;
  assert.ok(dispatchCommand("diff.focus.toggle"));
  await settle();
}

async function toastsDuring(body: () => Promise<void> | void): Promise<string[]> {
  const toasts: string[] = [];
  const listen = (e: Event): void => {
    toasts.push(String((e as CustomEvent<{ message: string }>).detail.message));
  };
  document.addEventListener(NOTIFY_EVENT, listen);
  try {
    await body();
  } finally {
    document.removeEventListener(NOTIFY_EVENT, listen);
  }
  return toasts;
}

function key(el: Element, k: string, mods: KeyboardEventInit = {}): void {
  el.dispatchEvent(
    new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...mods }),
  );
}

const json = (body: unknown, status = 200): Response =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });

// fakeServer answers the diff routes from the showcase's fixtures. refuse names the routes that
// fail, by "METHOD path" or "METHOD path#op" for the session route, with the status and reason.
function fakeServer(refuse: Record<string, [number, string]>): void {
  const session = { ...demoSession(), as_of: "d1" };
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = input instanceof Request ? input.url : String(input);
    const url = new URL(raw);
    const method = input instanceof Request ? input.method : (init?.method ?? "GET");
    let op = "";
    if (url.pathname === "/api/v1/diff/session" && method === "POST") {
      op = (JSON.parse(String(init?.body)) as { op: string }).op;
    }
    const hit = refuse[`${method} ${url.pathname}#${op}`] ?? refuse[`${method} ${url.pathname}`];
    if (hit) {
      return json({ error: { code: hit[0], message: hit[1], status: "X" } }, hit[0]);
    }
    switch (url.pathname) {
      case "/api/v1/diff/patch":
        return json({ files: DEMO_FILES, patch: "", digest: "d1", clean: false });
      case "/api/v1/diff":
      case "/api/v1/diff/session":
        return json(session);
      case "/api/v1/diff/review":
        return json(demoReview());
      case "/api/v1/diff/branches":
        return json({ branches: [] });
      default:
        return json({}, 404);
    }
  }) as typeof fetch;
}

const rootEl = (): HTMLElement => {
  const el = document.querySelector<HTMLElement>(".console-diff-layout");
  assert.ok(el, "the app is mounted");
  return el;
};

// The overview replaces the stream, so focus has to move into it: left on the hidden stream, the
// Esc that should close it reached no handler and nothing could.
test("the overview takes focus on the way in, and Esc and its button close it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const scroll = document.querySelector<HTMLElement>(".console-diff-scroll");
  const overview = document.querySelector<HTMLElement>(".console-diff-overview");
  const toggle = document.querySelector<HTMLButtonElement>(".console-diff-toolbar__overview");
  assert.ok(scroll && overview && toggle);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");

  key(scroll, "Escape");
  assert.equal(rootEl().dataset.overview, "on");
  assert.ok(document.activeElement === overview, "focus moves into the overview");
  assert.equal(toggle.getAttribute("aria-expanded"), "true");

  const back = overview.querySelector<HTMLButtonElement>(".console-diff-overview__back");
  assert.ok(back, "a visible way back is on the page");
  assert.equal(back.textContent, "Back to diff");

  // Esc pressed where focus now is closes it.
  key(overview, "Escape");
  assert.equal(rootEl().dataset.overview, "off");
  assert.ok(document.activeElement === scroll, "and focus returns to the stream");

  // The toolbar button opens it, and the overview's own button closes it.
  toggle.click();
  assert.equal(rootEl().dataset.overview, "on");
  overview.querySelector<HTMLButtonElement>(".console-diff-overview__back")?.click();
  assert.equal(rootEl().dataset.overview, "off");
  assert.equal(toggle.getAttribute("aria-expanded"), "false");

  dispose.deactivate();
});

test("the overview's counts are pluralised and laid out as a description list", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.overview"));
  const overview = document.querySelector<HTMLElement>(".console-diff-overview");
  const text = overview?.textContent ?? "";
  assert.ok(overview?.querySelector("dl.pf-v6-c-description-list"), "terms and their values");
  const values = [
    ...(overview?.querySelectorAll(
      ".pf-v6-c-description-list__description > .pf-v6-c-description-list__text",
    ) ?? []),
  ].map((el) => el.textContent);
  assert.ok(values.includes("1 file"), `a single file is not '1 files', got ${values.join(" | ")}`);
  assert.doesNotMatch(text, /\b1 files\b/);
  assert.doesNotMatch(text, /history lens/);
  dispose.deactivate();
});

// "Read these first" is resolved by path. It used to index the visible files by the position of the
// primary list, which in focus mode (one step on screen) pointed at somebody else's header.
test("a file chosen in the overview takes focus mode to the step that holds it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();
  await setFocusMode(true);

  assert.ok(dispatchCommand("diff.overview"));
  const files = [...document.querySelectorAll<HTMLButtonElement>(".console-diff-overview__file")];
  const target = files.at(-1);
  assert.ok(target && files.length > 1);
  const path = target.textContent ?? "";

  target.click();
  await settle();

  assert.equal(rootEl().dataset.overview, "off");
  const shown = [...document.querySelectorAll(".console-diff-row__path")].map(
    (el) => el.textContent,
  );
  assert.ok(
    shown.some((p) => p?.endsWith(path)),
    `focus mode shows the step holding ${path}, got ${shown.join(", ")}`,
  );

  await setFocusMode(false);
  dispose.deactivate();
});

// The index is wired to the stream by path. It used to index the visible files by the item's
// position in the changeset, so folding generated or settled files moved every item onto its
// neighbour's header.
test("a sidebar item advertises the stream row of its own file, by path", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  // Folding the generated files rebuilds the rows; the mapping has to survive it.
  assert.ok(dispatchCommand("diff.generated.toggle"));
  await settle();

  const first = document.querySelector<HTMLButtonElement>(".console-diff-sidebar__item");
  assert.ok(first);
  const row = Number(first.dataset.fileRow);
  const header = document.querySelector<HTMLElement>(`.console-diff-row[data-row="${row}"]`);
  const path = first.title.split("\n")[0];
  assert.ok(header, "the row the item names is painted");
  assert.ok(
    header.querySelector(".console-diff-row__path")?.textContent?.endsWith(path ?? "?"),
    "and it is this file's header",
  );
  assert.ok(dispatchCommand("diff.generated.toggle"));
  dispose.deactivate();
});

test("the sidebar's list items are not buttons, and the buttons live inside them", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll("button[role=listitem]").length, 0);
  const entries = document.querySelectorAll("[role=listitem] > button.console-diff-sidebar__item");
  assert.equal(entries.length, 11);
  const names = [...document.querySelectorAll(".console-diff-sidebar__counts")].map(
    (el) => el.textContent,
  );
  assert.ok(names.length > 0 && names.every((t) => /referents$/.test(t ?? "")), "the unit is said");

  // The stream's grid owns rows through a window that says it owns nothing.
  assert.equal(
    document.querySelector(".console-diff-spacer")?.getAttribute("role"),
    "presentation",
  );
  assert.equal(document.querySelector(".console-diff-window")?.getAttribute("role"), "rowgroup");
  dispose.deactivate();
});

test("a hunk can be commented on, and marked read, with a click", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const comment = document.querySelector<HTMLButtonElement>(".console-diff-row__comment-action");
  assert.ok(comment, "every hunk heading has a Comment button");
  assert.ok(comment.classList.contains("pf-m-link"), "a PF link button, not a bare one");
  comment.click();
  const box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.match(box?.getAttribute("aria-label") ?? "", /Comment on this hunk/);
  box?.querySelector<HTMLButtonElement>(".console-diff-composer__cancel")?.click();
  assert.equal(document.querySelector(".console-diff-composer"), null, "Cancel closes it");

  const mark = document.querySelector<HTMLButtonElement>(".console-diff-row__mark");
  assert.ok(mark);
  assert.equal(mark.textContent, "Mark read");
  mark.click();
  await settle();
  const after = document.querySelector<HTMLButtonElement>(".console-diff-row__mark");
  assert.equal(after?.textContent, "Mark unread", "the row repaints with the new mark");
  assert.ok(
    document.querySelector(".console-diff-row--hunk[data-viewed] .pf-v6-c-label"),
    "and carries a read label",
  );
  dispose.deactivate();
});

test("a reading pass can be stepped and marked with buttons, and views switched with a toggle", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();
  await setFocusMode(true);

  const nav = document.querySelector<HTMLElement>(".console-diff-progress__nav");
  const buttons = [...(nav?.querySelectorAll<HTMLButtonElement>("button") ?? [])];
  assert.deepEqual(
    buttons.map((b) => b.textContent),
    ["Previous", "Next", "Mark read and next"],
  );
  assert.equal(buttons[0]?.disabled, true, "there is nothing before the first step");

  buttons[2]?.click();
  await settle();
  assert.equal(document.querySelector(".console-diff-progress__text")?.textContent, "Step 2 of 12");
  assert.equal(buttons[0]?.disabled, false);
  buttons[0]?.click();
  await settle();
  assert.equal(document.querySelector(".console-diff-progress__text")?.textContent, "Step 1 of 12");

  const split = [
    ...document.querySelectorAll<HTMLElement>(".console-diff-toolbar__mode button"),
  ].at(-1);
  assert.equal(split?.textContent, "Split");
  split?.click();
  await settle();
  assert.equal(split?.getAttribute("aria-pressed"), "true");
  assert.ok(
    document.querySelector(".console-diff-row--pair"),
    "the stream is laid out in two columns",
  );
  document.querySelector<HTMLButtonElement>(".console-diff-toolbar__mode button")?.click();
  await settle();

  await setFocusMode(false);
  dispose.deactivate();
});

test("the send box and the reply box label their field and can be cancelled", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.publish"));
  let box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.equal(box?.querySelector("textarea")?.getAttribute("aria-label"), "Review summary");
  assert.equal(
    Number(
      box?.querySelector("textarea")?.getAttribute("rows") ?? box?.querySelector("textarea")?.rows,
    ),
    2,
    "two rows, so it does not cover the code",
  );
  assert.ok(box?.querySelector(".pf-v6-c-tabs [role=tablist]"), "Write and Preview are PF tabs");
  const tabs = [...(box?.querySelectorAll<HTMLElement>("[role=tab]") ?? [])];
  assert.deepEqual(
    tabs.map((t) => [t.textContent, t.getAttribute("aria-selected")]),
    [
      ["Write", "true"],
      ["Preview", "false"],
    ],
  );
  tabs[1]?.click();
  assert.equal(tabs[1]?.getAttribute("aria-selected"), "true");
  assert.equal(tabs[0]?.getAttribute("aria-selected"), "false");
  assert.equal(
    tabs[1]?.getAttribute("aria-controls"),
    box?.querySelector<HTMLElement>(".console-diff-composer__preview")?.id,
    "each tab names its panel",
  );
  // The network sentence is one helper line, and the drafts sit in a section the reader opens.
  assert.equal(box?.querySelectorAll(".pf-v6-c-helper-text").length, 1);
  const toggle = box?.querySelector<HTMLButtonElement>(".console-diff-disclosure__toggle");
  assert.equal(toggle?.getAttribute("aria-expanded"), "false");
  toggle?.click();
  assert.equal(toggle?.getAttribute("aria-expanded"), "true");
  box?.querySelector<HTMLButtonElement>(".console-diff-composer__cancel")?.click();
  assert.equal(document.querySelector(".console-diff-composer"), null);

  assert.ok(dispatchCommand("diff.thread.reply"));
  box = document.querySelector<HTMLElement>(".console-diff-composer");
  assert.equal(box?.querySelector("textarea")?.getAttribute("aria-label"), "Reply to priya");
  key(box?.querySelector("textarea") as Element, "Escape");
  assert.equal(document.querySelector(".console-diff-composer"), null, "Esc still cancels");
  dispose.deactivate();
});

test("a comment you wrote is 'You (draft)', not the word the server uses for the route", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const who = [...document.querySelectorAll(".console-diff-row__who")].map((el) => el.textContent);
  assert.ok(who.includes("You (draft)"), `got ${who.join(", ")}`);
  assert.ok(!who.some((t) => /unattributed/i.test(t ?? "")));
  dispose.deactivate();
});

test("the key legend is the bound commands, and focus mode rewords the keys that change", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const bound = listCommands()
    .filter((c) => c.group === "Diff" && c.key)
    .map((c) => (c.key === "Escape" ? "Esc" : c.key));
  const legend = (): string[] =>
    [...document.querySelectorAll(".console-diff-toolbar__keys dt kbd")].map(
      (el) => el.textContent ?? "",
    );
  assert.deepEqual(legend(), bound, "every bound key is taught, and nothing else");
  assert.ok(legend().length >= 18);
  assert.ok(
    [...document.querySelectorAll(".console-diff-toolbar__keys kbd")].every((el) =>
      el.classList.contains("console-cheatsheet-kbd"),
    ),
  );
  const names = (): string[] =>
    [...document.querySelectorAll(".console-diff-toolbar__keys dd")].map(
      (el) => el.textContent ?? "",
    );
  assert.ok(names().includes("next hunk"));

  await setFocusMode(true);
  assert.ok(names().includes("next step"), "] steps in focus mode");
  assert.ok(names().includes("mark the step read, then next"), "v marks the step");
  assert.ok(!names().includes("next hunk"));
  await setFocusMode(false);
  dispose.deactivate();
});

test("the key legend opens from its button and closes on Esc and on a click outside", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const toggle = document.querySelector<HTMLButtonElement>(".console-diff-toolbar__keystoggle");
  const pop = document.querySelector<HTMLElement>(
    ".console-diff-toolbar__keyswrap .console-diff-popover",
  );
  assert.ok(toggle && pop);
  assert.equal(pop.hidden, true);
  toggle.click();
  assert.equal(pop.hidden, false);
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  key(document.body, "Escape");
  assert.equal(pop.hidden, true);
  assert.equal(toggle.getAttribute("aria-expanded"), "false");

  toggle.click();
  await settle(2);
  document.body.click();
  assert.equal(pop.hidden, true, "a click outside closes it");
  dispose.deactivate();
});

// The reading mark's pressed state is PF's own, and the command sits in a popover off the head
// with a Copy that says it copied, announces it, and goes back.
test("the reading command copies, is announced, and its button goes back", async () => {
  location.hash = "#demo";
  const written: string[] = [];
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: (t: string) => (written.push(t), Promise.resolve()) },
  });
  const dispose = activate(document.body);
  await settle();

  document.querySelector<HTMLButtonElement>(".console-diff-toolbar__reading")?.click();
  await settle();
  const copy = document.querySelector<HTMLButtonElement>(
    ".console-diff-reading button[aria-label='Copy command']",
  );
  assert.ok(copy);
  const before = copy.innerHTML;
  copy.click();
  await settle(4);
  assert.match(written[0] ?? "", /^gh pr comment 482/);
  assert.notEqual(copy.innerHTML, before, "the icon says it copied");
  await new Promise((r) => setTimeout(r, 120));
  assert.match(
    document.querySelector("[role=status].pf-v6-screen-reader")?.textContent ?? "",
    /copied/i,
    "and it is announced",
  );
  await new Promise((r) => setTimeout(r, 1300));
  assert.equal(copy.innerHTML, before, "and goes back");

  // Turning the mark off closes the popover with it.
  document.querySelector<HTMLButtonElement>(".console-diff-toolbar__reading")?.click();
  await settle();
  assert.equal(document.querySelector<HTMLElement>(".console-diff-reading")?.hidden, true);
  dispose.deactivate();
});

test("the loading state shows a spinner, and an answered state does not", async () => {
  location.hash = "";
  const dispose = activate(document.body);
  await settle();
  // No server and no showcase: the connect prompt, which is an answer.
  const spinner = document.querySelector<HTMLElement>(".console-diff-empty__spinner");
  assert.ok(spinner, "the spinner is there for the first read");
  assert.equal(spinner.hidden, true, "and gone once the read has an answer");
  dispose.deactivate();
});

// --- failures ---------------------------------------------------------------------------

test("a send that fails toasts, says so in an alert, and keeps the box open with the words", async () => {
  location.hash = "#port=7391";
  fakeServer({ "POST /api/v1/diff/session#publish": [502, "no pull request for this branch"] });
  const dispose = activate(document.body);
  await settle(30);

  const toasts = await toastsDuring(async () => {
    assert.ok(dispatchCommand("diff.publish"));
    const box = document.querySelector<HTMLElement>(".console-diff-composer--batch");
    const field = box?.querySelector<HTMLTextAreaElement>("textarea");
    assert.ok(field, "the send box opened against the fake server");
    field.value = "a summary";
    key(field, "Enter", { metaKey: true });
    await settle(30);
  });

  assert.deepEqual(toasts, ["Could not send your drafts: no pull request for this branch"]);
  const box = document.querySelector<HTMLElement>(".console-diff-composer--batch");
  assert.ok(box, "the box stays open");
  assert.ok("failed" in box.dataset);
  const alert = box.querySelector<HTMLElement>("[role=alert]");
  assert.match(alert?.textContent ?? "", /no pull request for this branch/);
  assert.equal(box.querySelector("textarea")?.value, "a summary", "the words are still there");
  assert.equal(box.querySelector("textarea")?.disabled, false, "and it can be tried again");
  dispose.deactivate();
});

test("a reply that fails toasts and shows an alert in its own box", async () => {
  location.hash = "#port=7391";
  fakeServer({ "POST /api/v1/diff/session#reply": [500, "the host refused the reply"] });
  const dispose = activate(document.body);
  await settle(30);

  const toasts = await toastsDuring(async () => {
    assert.ok(dispatchCommand("diff.thread.reply"));
    const field = document.querySelector<HTMLTextAreaElement>(".console-diff-composer textarea");
    assert.ok(field);
    field.value = "agreed";
    key(field, "Enter", { ctrlKey: true });
    await settle(30);
  });

  assert.deepEqual(toasts, ["Could not send your reply: the host refused the reply"]);
  assert.match(
    document.querySelector(".console-diff-composer [role=alert]")?.textContent ?? "",
    /the host refused the reply/,
  );
  dispose.deactivate();
});

test("a failed peek at surrounding code toasts and says so in the panel", async () => {
  location.hash = "#port=7391";
  fakeServer({ "GET /api/v1/diff/context": [500, "boom"] });
  const dispose = activate(document.body);
  await settle(30);

  const toasts = await toastsDuring(async () => {
    const peek = document.querySelector<HTMLButtonElement>(".console-diff-row__peek");
    assert.ok(peek, "a live app offers the peek");
    peek.click();
    await settle(30);
  });

  assert.equal(toasts.length, 1);
  assert.match(toasts[0] ?? "", /Could not load surrounding code/);
  const panel = document.querySelector<HTMLElement>(".console-diff-context");
  assert.equal(panel?.hidden, false);
  assert.match(panel?.querySelector("[role=alert]")?.textContent ?? "", /Could not load/);
  dispose.deactivate();
});

test("an agent session that cannot be read toasts and says so in the panel", async () => {
  location.hash = "#port=7391";
  const server = (() => {
    fakeServer({});
    return globalThis.fetch;
  })();
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const raw = input instanceof Request ? input.url : String(input);
    if (raw.includes("ViewerService/GetSessionActivity")) {
      return json({ code: "internal", message: "no session store" }, 500);
    }
    return server(input, init);
  }) as typeof fetch;
  const dispose = activate(document.body);
  await settle(30);

  const toasts = await toastsDuring(async () => {
    const open = document.querySelector<HTMLButtonElement>(".console-diff-row__session");
    assert.ok(open, "a touch row offers the session");
    open.click();
    await settle(30);
  });

  // One toast, and the transport raises it: the app adds the alert, not a second toast.
  assert.equal(toasts.length, 1, toasts.join(" | "));
  assert.match(toasts[0] ?? "", /GetSessionActivity failed: no session store/);
  const panel = document.querySelector<HTMLElement>(".console-diff-context");
  assert.match(panel?.querySelector("[role=alert]")?.textContent ?? "", /no session store/);
  assert.equal(panel?.querySelector("h2")?.className, "console-diff-context__title");
  assert.ok(
    panel?.querySelector("h3.console-diff-agent__heading"),
    "headings nest under the title",
  );
  dispose.deactivate();
});
