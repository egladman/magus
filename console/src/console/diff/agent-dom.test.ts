// agent-dom.test.ts - the session panel: Ran, transcript turns, and the sentence that stands in
// for every part of a transcript the daemon could not show.

import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { create } from "@bufbuild/protobuf";
import {
  SessionActivitySchema,
  TurnRole,
  type SessionActivity,
} from "@wire/viewer/v1alpha1/viewer_pb";
import { fetchSessionActivity, renderAgentSession } from "./agent";
import { activate } from "./main";
import type { DiffTouch } from "./session";

const realFetch = globalThis.fetch;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
});

afterEach(() => {
  globalThis.fetch = realFetch;
  location.hash = "";
});

const touch: DiffTouch = {
  host: "claude-code",
  session: "sess-1",
  transcript: "/home/u/.claude/projects/x/sess-1.jsonl",
  read: ["a.go"],
  ran: ["magus", "go"],
};

// What the daemon answers today: tool turns only, and the three parts session load never carries.
function toolOnly(): SessionActivity {
  return create(SessionActivitySchema, {
    session: "sess-1",
    host: "claude-code",
    wrote: true,
    turns: [
      { role: TurnRole.TOOL, kind: "file.read", text: "a.go" },
      { role: TurnRole.TOOL, kind: "shell.command", program: "go", verdict: "deny", exit: 2 },
      { role: TurnRole.TOOL, kind: "file.write", text: "a.go" },
    ],
    unrecorded: [
      {
        role: TurnRole.USER,
        reason: "session load records tool calls only; user prompts are not loaded",
      },
      {
        role: TurnRole.ASSISTANT,
        reason: "session load records tool calls only; assistant text is not loaded",
      },
      {
        role: TurnRole.REASONING,
        reason: "session load records tool calls only; reasoning is not loaded",
      },
    ],
  });
}

function texts(root: ParentNode, selector: string): string[] {
  return [...root.querySelectorAll(selector)].map((el) => el.textContent ?? "");
}

test("the panel renders Ran oldest first and every transcript turn", () => {
  const body = document.createElement("div");
  renderAgentSession(body, touch, { activity: toolOnly() });

  assert.deepEqual(texts(body, ".console-diff-agent__program"), ["go", "magus"]);
  assert.deepEqual(
    [...body.querySelectorAll(".console-diff-agent__turn")].map((li) => [
      li.querySelector(".console-diff-agent__role")?.textContent,
      li.querySelector(".console-diff-agent__text")?.textContent,
      li.querySelector(".console-diff-agent__note")?.textContent ?? "",
    ]),
    [
      ["file.read", "a.go", ""],
      ["shell.command", "go", "guard: deny, exit 2"],
      ["file.write", "a.go", ""],
    ],
  );
});

test("missing prompts, assistant text and reasoning are each stated", () => {
  const body = document.createElement("div");
  renderAgentSession(body, touch, { activity: toolOnly() });

  assert.deepEqual(texts(body, ".console-diff-agent__notice"), [
    "User prompts unavailable for this session: session load records tool calls only; user prompts are not loaded.",
    "Assistant text unavailable for this session: session load records tool calls only; assistant text is not loaded.",
    "Reasoning unavailable for this session: session load records tool calls only; reasoning is not loaded.",
  ]);
});

test("a recorded reasoning turn renders and suppresses its unavailable sentence", () => {
  const activity = create(SessionActivitySchema, {
    session: "sess-1",
    wrote: true,
    turns: [
      { role: TurnRole.USER, text: "rename the claim" },
      { role: TurnRole.REASONING, text: "the audience check reads the old name" },
      { role: TurnRole.ASSISTANT, text: "Renaming it." },
      { role: TurnRole.TOOL, kind: "file.write", text: "a.go" },
    ],
  });
  const body = document.createElement("div");
  renderAgentSession(body, touch, { activity });

  const rows = [...body.querySelectorAll<HTMLElement>(".console-diff-agent__turn")];
  assert.deepEqual(
    rows
      .slice(0, 3)
      .map((li) => [li.dataset.role, li.querySelector(".console-diff-agent__text")?.textContent]),
    [
      ["user", "rename the claim"],
      ["reasoning", "the audience check reads the old name"],
      ["assistant", "Renaming it."],
    ],
  );
  assert.deepEqual(texts(body, ".console-diff-agent__notice"), []);
});

test("a daemon that did not answer still states reasoning is unavailable", () => {
  const body = document.createElement("div");
  renderAgentSession(body, touch, {
    failed: "viewer: this daemon does not serve session activity",
  });

  assert.equal(body.querySelectorAll(".console-diff-agent__program").length, 2);
  assert.ok(
    texts(body, ".console-diff-agent__notice").includes(
      "Reasoning unavailable for this session: the daemon did not answer (viewer: this daemon does not serve session activity).",
    ),
  );
});

test("a session with no loaded write says how to load it", () => {
  const body = document.createElement("div");
  const activity = create(SessionActivitySchema, { session: "sess-1", wrote: false });
  renderAgentSession(body, { ...touch, ran: [] }, { activity });

  assert.deepEqual(texts(body, ".console-diff-agent__empty"), [
    "No commands recorded before this write.",
    "No loaded transcript holds this write. Run the host's session load adapter to load it.",
  ]);
  assert.ok(
    texts(body, ".console-diff-agent__notice").some((t) => t.startsWith("Reasoning unavailable")),
  );
});

test("fetchSessionActivity asks the viewer service for one session and path", async () => {
  let url = "";
  let sent: unknown = null;
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    url = input instanceof Request ? input.url : String(input);
    const body = input instanceof Request ? await input.text() : init?.body;
    sent = JSON.parse(
      typeof body === "string" ? body : new TextDecoder().decode(body as Uint8Array),
    );
    return new Response(JSON.stringify({ session: "sess-1", wrote: true }), {
      headers: { "Content-Type": "application/json" },
    });
  }) as typeof fetch;

  const result = await fetchSessionActivity(
    "127.0.0.1:7391",
    "sess-1",
    "a.go",
    new AbortController().signal,
  );
  assert.ok(url.endsWith("/magus.viewer.v1alpha1.ViewerService/GetSessionActivity"));
  assert.deepEqual(sent, { session: "sess-1", path: "a.go" });
  assert.ok("activity" in result && result.activity.wrote);
});

test("the showcase story row renders Ran and its Session panel states reasoning is unavailable", async () => {
  globalThis.fetch = (() => {
    throw new Error("demo mode must not reach the network");
  }) as typeof fetch;
  location.hash = "#demo";
  const dispose = activate(document.body);
  for (let i = 0; i < 12; i++) await new Promise((r) => setTimeout(r, 0));

  const story = [...document.querySelectorAll<HTMLElement>(".console-diff-row--story")].find((el) =>
    el.querySelector(".console-diff-row__ran"),
  );
  assert.ok(story, "a story row carrying Ran");
  assert.match(story.querySelector(".console-diff-row__ran")?.textContent ?? "", /^ran /);

  const open = [...story.querySelectorAll<HTMLButtonElement>("button")].find(
    (b) => b.textContent === "Session",
  );
  assert.ok(open);
  open.click();
  await new Promise((r) => setTimeout(r, 0));

  const panel = document.querySelector<HTMLElement>(".console-diff-context");
  assert.ok(panel);
  assert.equal(panel.hidden, false);
  assert.ok(texts(panel, ".console-diff-agent__program").length > 0);
  assert.ok(
    texts(panel, ".console-diff-agent__notice").includes(
      "Reasoning unavailable for this session: session activity is not part of this showcase.",
    ),
  );
  dispose.deactivate();
});
