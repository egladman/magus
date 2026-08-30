// main-dom.test.ts - the Activity surface's payload expansion. document/window are registered
// globally by test-setup.mjs (node --import), so this runs under node:test like the other *-dom
// tests. The event -> section mapping itself is covered in adapter.test.ts.
//
// What is pinned HERE is the one thing the trail could not do: READ A BODY IT NAMES. An event
// carries a content ref, a size, and at most the first 240 characters of the response, and
// ActivityService.GetPayload is the documented way to resolve the rest - which no console code
// called, so a reader who wanted the payload the trail was pointing at had nowhere to go. These
// tests fetch through the same stubbed transport the surface really uses, so a control that is
// drawn but never wired fails them.

import assert from "node:assert/strict";
import { test, afterEach } from "node:test";
import { activate } from "./main";
import type { SurfaceInstance } from "../standalone";

const realFetch = globalThis.fetch;
let mounted: SurfaceInstance | null = null;
let hostEl: HTMLElement | null = null;
// The refs GetPayload was asked for, in order: the surface must resolve the ref the reader clicked
// on, not whichever one it happened to build the control from last.
let asked: string[] = [];

// There is no beforeEach on purpose. The DOM suite runs with --experimental-test-isolation=none, so
// a hook declared at file scope fires for every *-dom test in the process, siblings included - and
// the setup these tests need is a #port attach, which would put a sibling's surface into live mode
// against a daemon that is not there. mount() does it per test instead. The teardown below is only
// the undo (a null check away from a no-op elsewhere), which is what the sibling files do too.
afterEach(() => {
  mounted?.deactivate();
  mounted = null;
  hostEl?.remove();
  hostEl = null;
  globalThis.fetch = realFetch;
  location.hash = "";
  asked = [];
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

// The wire shape, in protobuf JSON: a Timestamp is RFC3339, an int64 a string, and an enum its
// declared name. Written as the daemon actually serializes it so a fixture cannot pass while the
// real feed would not parse.
function mcpEvent(): unknown {
  return {
    time: new Date(Date.now() - 30_000).toISOString(),
    kind: "KIND_MCP_TOOL_CALL",
    actor: "agent:claude",
    action: "magus_query",
    outcome: "OUTCOME_OK",
    responseRef: "mcpbbbb",
    responseBytes: "2048",
    // The response as the trail keeps it: the opening characters, and a ref for the rest.
    preview: "nodes: console, docs, engine, ...",
  };
}

// serve answers the two ActivityService procedures. payload is what GetPayload gives back: either
// the stored bytes, or the failure to answer with.
function serve(
  events: unknown[],
  payload: { body: string } | { status: number; code: string; message: string },
): void {
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const headers = new Headers({ "content-type": "application/json" });
    if (url.includes("ActivityService/ListActivityEvents")) {
      return Promise.resolve({
        ok: true,
        status: 200,
        headers,
        json: () => Promise.resolve({ events, nextPageToken: "" }),
      } as unknown as Response);
    }
    if (url.includes("ActivityService/GetPayload")) {
      // The transport serializes even a JSON request to bytes, so the body arrives as a Uint8Array.
      const raw = init?.body;
      const json = raw instanceof Uint8Array ? new TextDecoder().decode(raw) : String(raw ?? "{}");
      asked.push((JSON.parse(json) as { ref?: string }).ref ?? "");
      if ("body" in payload) {
        const bytes = Buffer.from(payload.body, "utf8");
        return Promise.resolve({
          ok: true,
          status: 200,
          headers,
          json: () =>
            Promise.resolve({
              body: bytes.toString("base64"),
              sizeBytes: String(bytes.length),
            }),
        } as unknown as Response);
      }
      return Promise.resolve({
        ok: false,
        status: payload.status,
        headers,
        json: () => Promise.resolve({ code: payload.code, message: payload.message }),
      } as unknown as Response);
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

// mount attaches the surface to a fresh host. The hash is the surface's source selector: "#port="
// is what puts it in live mode against a loopback daemon (the stub), "#demo" the synthesized trail.
async function mount(hash = "#port=7391"): Promise<HTMLElement> {
  location.hash = hash;
  const host = document.createElement("div");
  document.body.append(host);
  hostEl = host;
  mounted = activate(host);
  await settle();
  return host;
}

// The expand controls are the only buttons inside a section's body; the fold toggle is the head.
function controls(host: HTMLElement): HTMLButtonElement[] {
  return [...host.querySelectorAll<HTMLButtonElement>(".console-render-section__lines button")];
}

function sectionText(host: HTMLElement): string {
  return host.querySelector(".console-render-section")?.textContent ?? "";
}

test("an event with a stored body offers to show it, sized", async () => {
  serve([mcpEvent()], { body: "{}" });
  const host = await mount();

  const btns = controls(host);
  assert.equal(btns.length, 1, "one control for the one ref the event carries");
  assert.equal(btns[0].textContent, "show response (2.0 KB)");
});

test("clicking it resolves that ref and paints the full body", async () => {
  serve([mcpEvent()], { body: "line one\nline two\n" });
  const host = await mount();

  controls(host)[0].click();
  await settle();

  assert.deepEqual(asked, ["mcpbbbb"]);
  const text = sectionText(host);
  assert.match(text, /response mcpbbbb/, "the expanded body is labelled with the ref it came from");
  assert.match(text, /line one/);
  assert.match(text, /line two/);
  assert.equal(controls(host).length, 0, "the control is spent once the body is on screen");
});

// A ref outlives its blob: the trail rotates payloads out from under events that still name them.
// That is a normal end state, so the reader is told plainly rather than shown a thrown error or a
// retry for something that is never coming back.
test("a rotated-away payload says so instead of failing", async () => {
  serve([mcpEvent()], {
    status: 404,
    code: "not_found",
    message: "trail: no workspace holds this payload",
  });
  const host = await mount();

  controls(host)[0].click();
  await settle();

  assert.match(sectionText(host), /response mcpbbbb is no longer stored/);
  assert.equal(controls(host).length, 0);
});

// A daemon blip is the opposite case: the body still exists, so the control has to survive for the
// reader to press again.
test("a transient failure keeps the control and names the reason", async () => {
  serve([mcpEvent()], { status: 503, code: "unavailable", message: "daemon went away" });
  const host = await mount();

  controls(host)[0].click();
  await settle();

  const btns = controls(host);
  assert.equal(btns.length, 1);
  assert.equal(btns[0].disabled, false, "still pressable");
  assert.equal(btns[0].textContent, "show response (2.0 KB)", "the label is restored");
  assert.match(sectionText(host), /could not read the response: .*daemon went away/);
});

// The demo trail's refs are synthesized: no store holds them, so a control there could only ever
// fail. The offer is gated on a live client rather than on the ref alone.
test("the demo trail offers no expansion", async () => {
  serve([], { body: "unused" });
  const host = await mount("#demo");

  assert.ok(host.querySelector(".console-render-section"), "the demo still paints its sections");
  assert.equal(controls(host).length, 0);
});
