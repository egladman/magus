// main-dom.test.ts - the Tools app's mount. document/window come from test-setup.mjs (node
// --import). The rows are served through the real ListTools transport so the wire mapping is under
// test too: a fixture written as the server serializes it (protobuf JSON, enums by name) cannot
// pass while the real feed would not parse.
//
// Pinned here is what a reader ends up looking at: the table sorts, the three filters narrow it and
// say so, and the two ways there is nothing to show (no server, a filter matching nothing) stay
// apart, because only one of them is the reader's to fix.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { setDefaultHost } from "../../lib/settings";
import { activate } from "./main";
import type { AppInstance } from "../../desktop/standalone";

const HOST = "127.0.0.1:7391";
const realFetch = globalThis.fetch;

let mounted: AppInstance | null = null;
let host: HTMLElement;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
  host = document.createElement("div");
  document.body.append(host);
});

// The mount holds a poll interval and the default-host cell is shared by every DOM test in this
// process, so both are released rather than left for a sibling's document.
afterEach(() => {
  mounted?.deactivate();
  mounted = null;
  setDefaultHost("");
  globalThis.fetch = realFetch;
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

function tool(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    bin: "go",
    spell: "go",
    installedVersion: "v1.26.5",
    workspaceWindow: ">= 1.26",
    effectiveWindow: ">= 1.26",
    verdict: "VERDICT_INSIDE",
    cycle: "1.26",
    eol: "2027-02-11",
    support: "SUPPORT_SUPPORTED",
    ...over,
  };
}

const PROJECTS = [
  {
    path: ".",
    tools: [
      tool(),
      tool({
        bin: "node",
        spell: "typescript",
        installedVersion: "v26.5.0",
        workspaceWindow: ">= 22, < 25",
        effectiveWindow: ">= 22, < 25",
        verdict: "VERDICT_TOO_NEW",
        violation: true,
        diagnosticCode: "MGS3006",
        cycle: "26",
        eol: "",
        support: "SUPPORT_UNANNOUNCED",
      }),
    ],
  },
  {
    path: "services/identity",
    tools: [
      tool({
        installedVersion: "v1.25.3",
        workspaceWindow: undefined,
        effectiveWindow: ">= 1.25",
        cycle: "1.25",
        eol: "2026-08-19",
        support: "SUPPORT_EOL",
      }),
    ],
  },
];

function serve(projects: unknown[], lifecycleState = "LIFECYCLE_STATE_LIVE"): void {
  setDefaultHost(HOST);
  globalThis.fetch = ((input: RequestInfo | URL) => {
    const url = String(input instanceof Request ? input.url : input);
    if (url.includes("ToolService/ListTools")) {
      const body = { projects, lifecycle: { provider: "endoflife-date", state: lifecycleState } };
      return Promise.resolve(
        new Response(JSON.stringify(body), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

async function mount(): Promise<void> {
  mounted = activate(host);
  await settle();
}

// table reads each body row as its cells.
function table(): string[][] {
  return [...host.querySelectorAll("tbody tr")].map((tr) =>
    [...tr.querySelectorAll("td")].map((td) => td.textContent ?? ""),
  );
}

function header(label: string): HTMLButtonElement {
  const btn = [...host.querySelectorAll<HTMLButtonElement>("thead button")].find(
    (b) => b.textContent === label,
  );
  assert.ok(btn, "no column " + label);
  return btn;
}

function filter(label: string): HTMLButtonElement {
  const btn = [...host.querySelectorAll<HTMLButtonElement>(".console-tools__filter")].find((b) =>
    b.textContent?.startsWith(label),
  );
  assert.ok(btn, "no filter " + label);
  return btn;
}

const tools = (): string[] => table().map((r) => r[0] + "@" + r[1]);

const count = (): string | null | undefined =>
  host.querySelector(".console-tools__count")?.textContent;

test("every toolchain row is a table row, with the wire mapped into its cells", async () => {
  serve(PROJECTS);
  await mount();
  assert.deepEqual(
    [...host.querySelectorAll("thead th")].map((th) => th.textContent),
    [
      "Tool",
      "Project",
      "Version",
      "Pin",
      "Window",
      "Declared by",
      "Verdict",
      "Probed",
      "Cycle",
      "End of life",
      "Support",
    ],
  );
  assert.deepEqual(table()[0].slice(0, 2), ["go", "."]);
  const node = table().find((r) => r[0] === "node");
  assert.deepEqual(node?.slice(2, 7), [
    "v26.5.0",
    ">= 22, < 25",
    ">= 22, < 25",
    "workspace",
    "too new (MGS3006)",
  ]);
  assert.equal(count(), "3 tools, 1 outside window");
});

test("the table shows the server's window text and violation flag, not its own reading of them", async () => {
  // The bounds are absent and the diagnostic code is blank, so a cell can only read as it does
  // because the server rendered it, and the count can only include the row because the server
  // flagged it.
  serve([
    {
      path: ".",
      tools: [
        tool({
          bin: "odd",
          effectiveWindow: "whatever the server printed",
          verdict: "VERDICT_TOO_OLD",
          violation: true,
        }),
      ],
    },
  ]);
  await mount();
  assert.equal(table()[0][4], "whatever the server printed");
  assert.equal(table()[0][6], "too old");
  assert.equal(count(), "1 tool, 1 outside window");
});

test("a column header sorts the table and a second press reverses it", async () => {
  serve(PROJECTS);
  await mount();
  assert.deepEqual(tools(), ["go@.", "go@services/identity", "node@."]);
  header("Project").click();
  assert.deepEqual(tools(), ["go@.", "node@.", "go@services/identity"]);
  header("Project").click();
  assert.deepEqual(tools(), ["go@services/identity", "go@.", "node@."]);
  header("End of life").click();
  assert.deepEqual(tools(), ["node@.", "go@services/identity", "go@."]);
});

test("each filter narrows the table and names how many rows it would leave", async () => {
  serve(PROJECTS);
  await mount();
  assert.equal(filter("Past end of life").textContent, "Past end of life (1)");
  assert.equal(filter("Unannounced").textContent, "Unannounced (1)");
  assert.equal(filter("Unpinned").textContent, "Unpinned (1)");

  filter("Past end of life").click();
  assert.deepEqual(tools(), ["go@services/identity"]);
  assert.equal(filter("Past end of life").getAttribute("aria-pressed"), "true");
  assert.equal(count(), "1 of 3 tools, 1 outside window");

  filter("Past end of life").click();
  filter("Unannounced").click();
  assert.deepEqual(tools(), ["node@."]);

  filter("Unannounced").click();
  filter("Unpinned").click();
  assert.deepEqual(tools(), ["go@services/identity"]);
});

test("filters combine, and a combination matching nothing says so", async () => {
  serve(PROJECTS);
  await mount();
  filter("Unannounced").click();
  filter("Unpinned").click();
  assert.deepEqual(tools(), []);
  assert.equal(
    host.querySelector(".console-table__empty")?.textContent,
    "No tool matches every active filter.",
  );
  assert.equal(host.querySelector<HTMLElement>(".console-table__empty")?.hidden, false);
});

test("a provider that did not answer is named, not left to blank columns", async () => {
  serve(PROJECTS, "LIFECYCLE_STATE_UNREACHED");
  await mount();
  assert.match(
    host.querySelector(".console-tools__note")?.textContent ?? "",
    /^end of life unknown: endoflife-date did not answer/,
  );
});

test("a live provider is credited without a warning", async () => {
  serve(PROJECTS);
  await mount();
  assert.equal(
    host.querySelector(".console-tools__note")?.textContent,
    "end of life from endoflife-date (live)",
  );
});

test("a workspace declaring no probed tool says what to declare", async () => {
  serve([]);
  await mount();
  assert.deepEqual(table(), []);
  assert.match(host.querySelector(".console-table__empty")?.textContent ?? "", /supported/);
});

test("with no server address the connect prompt stands in for the table", async () => {
  await mount();
  assert.equal(host.querySelector<HTMLElement>(".console-tools__empty")?.hidden, false);
  assert.equal(host.querySelector<HTMLElement>(".console-tools__table")?.hidden, true);
  assert.match(host.querySelector(".console-tools__empty-title")?.textContent ?? "", /No server/);
});

test("a refused connection is the connect prompt with a retry, not an empty table", async () => {
  setDefaultHost(HOST);
  globalThis.fetch = (() => Promise.reject(new Error("stub: refused"))) as typeof fetch;
  await mount();
  assert.equal(host.querySelector<HTMLElement>(".console-tools__table")?.hidden, true);
  assert.match(
    host.querySelector(".console-tools__empty-title")?.textContent ?? "",
    /Could not reach the server/,
  );
});
