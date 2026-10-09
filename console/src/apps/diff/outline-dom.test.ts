// outline-dom.test.ts - the agent's outline as the person meets it: shown, not copyable, and
// refused when pasted back into a reply. The Copy brief button and its toasts live here too,
// since a toast is a DOM event. document/window come from test-setup.mjs.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { NOTIFY_EVENT } from "../../lib/notifications";
import { dispatchCommand } from "../../desktop/commands";
import { activate } from "./main";
import { guardOutline, guardReply, OUTLINE_PASTE_REFUSAL } from "./outline";
import { copyThreadBrief } from "./thread-brief";

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

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// toastsDuring collects the messages raised through the event the shell listens on.
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

// paste fires a paste event carrying text and reports whether the field kept it (not cancelled).
function paste(field: HTMLElement, text: string): boolean {
  const event = new Event("paste", { bubbles: true, cancelable: true });
  Object.defineProperty(event, "clipboardData", { value: { getData: () => text } });
  return field.dispatchEvent(event);
}

test("an outline row refuses copy, cut, drag and the context menu", () => {
  const row = document.createElement("div");
  guardOutline(row);
  for (const type of ["copy", "cut", "dragstart", "contextmenu"]) {
    const event = new Event(type, { bubbles: true, cancelable: true });
    assert.equal(row.dispatchEvent(event), false, `${type} is cancelled`);
  }
  assert.equal(row.draggable, false);
});

test("the reply box refuses a paste of an outline topic and toasts that the person types", async () => {
  const field = document.createElement("textarea");
  guardReply(field, () => [{ thread: "t1", topics: ["who calls Put"] }]);

  const toasts = await toastsDuring(() => {
    assert.equal(paste(field, "see who calls put in the cache"), false, "the paste is cancelled");
  });
  assert.deepEqual(toasts, [OUTLINE_PASTE_REFUSAL]);
  assert.match(OUTLINE_PASTE_REFUSAL, /You type the reply yourself/);

  const quiet = await toastsDuring(() => {
    assert.equal(paste(field, "my own words"), true, "other text pastes as usual");
  });
  assert.deepEqual(quiet, []);
});

test("#demo shows the agent's outline beside the conversation, with nothing to copy", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  const rows = [...document.querySelectorAll<HTMLElement>(".console-diff-row--outline")];
  assert.deepEqual(
    rows.map((r) => r.querySelector(".console-diff-row__outline")?.textContent),
    [
      "claude-code suggests covering:",
      "who verifies against two audiences",
      "what the docstring should promise",
    ],
  );
  assert.ok(rows.every((r) => r.dataset.author === "agent"));
  assert.equal(rows[0]?.hasAttribute("data-head"), true);
  for (const row of rows) {
    assert.equal(row.querySelector("button"), null, "no copy affordance on an outline");
    assert.equal(row.dispatchEvent(new Event("copy", { cancelable: true })), false);
  }

  // Directly after the conversation it belongs to.
  const stream = [...document.querySelectorAll<HTMLElement>(".console-diff-row--comment")];
  const at = stream.findIndex((r) => r.dataset.commentId === "th1-b");
  assert.equal(stream[at + 1], rows[0]);
  dispose.deactivate();
});

test("#demo's reply box refuses an outline topic pasted into it", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.ok(dispatchCommand("diff.thread.reply"));
  const field = document.querySelector<HTMLTextAreaElement>(".console-diff-composer textarea");
  assert.ok(field);

  const toasts = await toastsDuring(() => {
    assert.equal(paste(field, "what the docstring should promise"), false);
  });
  assert.deepEqual(toasts, [OUTLINE_PASTE_REFUSAL]);
  dispose.deactivate();
});

// The showcase has no server to build a brief from, so it withholds the button the way it
// withholds Peek, rather than offering one that can only fail.
test("#demo withholds Copy brief, and keeps Reply on the thread's root", async () => {
  location.hash = "#demo";
  const dispose = activate(document.body);
  await settle();

  assert.equal(document.querySelectorAll(".console-diff-row__brief").length, 0);
  assert.equal(
    document.querySelectorAll('.console-diff-row__reply[data-thread-id="th1"]').length,
    1,
    "the thread is still answerable",
  );
  dispose.deactivate();
});

test("Copy brief asks for the conversation by its root and copies the text it is given", async () => {
  let url = "";
  globalThis.fetch = (async (input: RequestInfo | URL) => {
    url = String(input);
    return new Response(JSON.stringify({ id: "t1", brief: "the brief\nbody" }));
  }) as typeof fetch;

  let copied = "";
  const toasts = await toastsDuring(async () => {
    const ok = await copyThreadBrief(
      "127.0.0.1:7391",
      "t 1",
      new AbortController().signal,
      async (t) => {
        copied = t;
      },
    );
    assert.equal(ok, true);
  });
  assert.equal(url, "http://127.0.0.1:7391/api/v1/diff/thread?id=t+1");
  assert.equal(copied, "the brief\nbody");
  assert.deepEqual(toasts, []);
});

test("Copy brief toasts the server's refusal and copies nothing", async () => {
  globalThis.fetch = (async () =>
    new Response(
      JSON.stringify({
        error: { code: 404, message: "no review is open for this branch", status: "NOT_FOUND" },
      }),
      { status: 404 },
    )) as typeof fetch;

  let wrote = false;
  const toasts = await toastsDuring(async () => {
    const ok = await copyThreadBrief(
      "127.0.0.1:7391",
      "t1",
      new AbortController().signal,
      async () => {
        wrote = true;
      },
    );
    assert.equal(ok, false);
  });
  assert.deepEqual(toasts, ["no review is open for this branch"]);
  assert.equal(wrote, false);
});

test("Copy brief toasts an unreachable server and a clipboard that refuses", async () => {
  globalThis.fetch = (async () => {
    throw new TypeError("Failed to fetch");
  }) as typeof fetch;
  const unreachable = await toastsDuring(async () => {
    assert.equal(
      await copyThreadBrief("127.0.0.1:7391", "t1", new AbortController().signal, async () => {}),
      false,
    );
  });
  assert.equal(unreachable.length, 1, "an unreachable server is reported once");

  globalThis.fetch = (async () =>
    new Response(JSON.stringify({ id: "t1", brief: "b" }))) as typeof fetch;
  const refused = await toastsDuring(async () => {
    assert.equal(
      await copyThreadBrief("127.0.0.1:7391", "t1", new AbortController().signal, async () => {
        throw new Error("denied");
      }),
      false,
    );
  });
  assert.deepEqual(refused, ["Could not copy the brief: denied"]);
});
