// main-dom.test.ts - the Dashboard's connect prompt. document/window are registered globally by
// test-setup.mjs (node --import), so this runs under node:test like the other *-dom tests.
//
// What is pinned HERE is the connection lifecycle a reader sees when the server is not there:
//
//   - A REOPEN STARTS FROM NOTHING. The module outlives the console tab, so a dashboard that was
//     connected once and is reopened against a server that has since stopped must show the prompt,
//     not a blank board waiting on a connection it believes it already has.
//   - NOTHING RETRIES ON ITS OWN. Once the prompt says the server could not be reached, the status
//     stream stays closed until the reader clicks Retry, and Retry asks exactly once.

import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test, beforeEach, afterEach } from "node:test";
import { rememberHost, setDefaultHost } from "../../lib/settings";
import { activate, deactivate } from "./main";

const HOST = "127.0.0.1:7391";
const scaffold = readFileSync("src/console/dashboard/scaffold.html", "utf8");
const realFetch = globalThis.fetch;

// statusRequests counts opens of the status stream, the one feed whose state drives the prompt.
let statusRequests = 0;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  statusRequests = 0;
});

// A connected dashboard remembers its host in a module cell that localStorage.clear() does not
// reach, and every server surface in this process falls back to it, so it is reset with the default.
afterEach(() => {
  deactivate();
  setDefaultHost("");
  rememberHost("");
  globalThis.fetch = realFetch;
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// mount injects the scaffold inside the test rather than in beforeEach: the suite runs with
// --experimental-test-isolation=none, so a sibling file's beforeEach that empties the body runs
// after this file's and would take the scaffold with it.
function mount(): void {
  document.body.innerHTML = scaffold;
  activate();
}

// serve answers the status stream with an open, silent body when up, and refuses every request the
// way a browser does when nothing listens on the port when down.
function serve(up: boolean): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.endsWith("/api/v1/events")) statusRequests++;
    if (up && url.endsWith("/api/v1/events")) {
      return Promise.resolve(new Response(new ReadableStream({ start() {} }), { status: 200 }));
    }
    return Promise.reject(new TypeError("Failed to fetch"));
  }) as typeof fetch;
}

function title(): string {
  return document.getElementById("dash-connect-title")?.textContent ?? "";
}

function retryButton(): HTMLButtonElement | undefined {
  return [...document.querySelectorAll<HTMLButtonElement>("#dash-connect-actions button")].find(
    (b) => b.textContent === "Retry",
  );
}

test("a dashboard reopened after its server stopped shows the prompt", async () => {
  serve(true);
  mount();
  await settle();
  assert.notEqual(title(), "Could not reach the server");

  deactivate();
  serve(false);
  mount();
  await settle();

  assert.equal(title(), "Could not reach the server");
  assert.ok(retryButton(), "the prompt offers Retry");
});

test("an unreachable server is asked once, and again only on Retry", async () => {
  serve(false);
  mount();
  await settle();
  assert.equal(title(), "Could not reach the server");
  assert.equal(statusRequests, 1);

  retryButton()?.click();
  await settle();
  assert.equal(statusRequests, 2);
  assert.equal(title(), "Could not reach the server");
});

// The console served BY the server carries no #port and, on first use, no Settings address. The
// shell adopts the page's origin as the server, but that flag is per-bundle, so the dashboard has to
// adopt it itself; before it did, a signed-in dashboard on http://localhost:7391 sat on "No server
// connected" while the server streamed status to every other surface.
test("a signed-in dashboard on the server's own origin connects to that origin", async () => {
  const dom = (window as unknown as { happyDOM: { setURL(url: string): void } }).happyDOM;
  const before = location.href;
  dom.setURL("http://localhost:7391/console/dashboard/");
  sessionStorage.setItem("magus-live-token", "test-token");
  const asked: string[] = [];
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    asked.push(url);
    if (url.endsWith("/api/v1/events"))
      return Promise.resolve(new Response(new ReadableStream({ start() {} }), { status: 200 }));
    return Promise.reject(new TypeError("Failed to fetch"));
  }) as typeof fetch;
  try {
    mount();
    await settle();
    assert.ok(
      asked.includes("http://localhost:7391/api/v1/events"),
      "the status stream is opened against the page's own origin",
    );
    assert.notEqual(title(), "No server connected");
  } finally {
    sessionStorage.clear();
    dom.setURL(before);
  }
});
