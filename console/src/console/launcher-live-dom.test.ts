// launcher-live-dom.test.ts - the welcome screen's live reading. It is the first thing anyone sees,
// and it is the one place a wrong number is read as "what magus is doing" rather than as one tile
// among many, so the two failure modes worth pinning are: reporting a count nobody measured, and
// implying the pool is the reader's rather than the machine's.

import assert from "node:assert/strict";
import { describe, test, beforeEach } from "node:test";
import { buildLauncher, syncLauncherPulse, type Launchable } from "./home";
import type { PulseView } from "./pulse";

const SURFACES: Launchable[] = [
  { pageId: "dashboard", label: "Dashboard", hint: "What magus is doing right now" },
  { pageId: "logs", label: "Log Viewer", hint: "Read a run's captured output" },
];

// Scoped, not top-level: with --test-isolation=none a root hook runs before every other file's tests.
describe("the launcher's live reading", () => {
  let root: HTMLElement;
  const opened: string[] = [];

  beforeEach(() => {
    opened.length = 0;
    root = buildLauncher(SURFACES, (id) => opened.push(id));
  });

  const row = (): HTMLElement => {
    const el = root.querySelector<HTMLElement>("[data-launcher-live]");
    assert.ok(el);
    return el;
  };
  const text = (): string => row().textContent ?? "";

  test("a cold visit shows no reading at all", () => {
    assert.equal(row().hidden, true, "nothing has answered yet");
  });

  // The failure this guards: an old daemon that does not serve the route, or one dropped request,
  // rendering as a quiet idle machine. "No answer" and "nothing running" are different facts.
  test("no answer hides the row rather than reporting zero", () => {
    syncLauncherPulse(root, { running: 3, queued: 0, workspaces: [] });
    assert.equal(row().hidden, false);
    syncLauncherPulse(root, null);
    assert.equal(row().hidden, true, "a lost answer must not read as an idle daemon");
  });

  test("a busy daemon says what it is doing", () => {
    syncLauncherPulse(root, { running: 5, queued: 2, workspaces: ["/a", "/b"] });
    assert.equal(row().hidden, false);
    assert.match(text(), /5 targets running/);
    assert.match(text(), /2 queued/);
    assert.match(text(), /2 workspaces loaded/);
  });

  test("one of a thing is not 1 things", () => {
    syncLauncherPulse(root, { running: 1, queued: 1, workspaces: ["/a"] });
    assert.match(text(), /1 target running/);
    assert.match(text(), /1 queued\b/);
    assert.match(text(), /1 workspace loaded/);
  });

  // Idle is a real measurement and a connected daemon saying "nothing running" is informative - it is
  // the ABSENT reading above that has to stay silent, not this one.
  test("an idle daemon still reports", () => {
    syncLauncherPulse(root, { running: 0, queued: 0, workspaces: ["/a"] });
    assert.equal(row().hidden, false);
    assert.match(text(), /Nothing running/);
  });

  // There is ONE pool behind every loaded workspace, so these counts are the machine's occupancy and
  // never the reader's. The dashboard hero carries the same qualifier; this is the screen where
  // dropping it would most look like a personal number.
  test("the counts name whose they are", () => {
    syncLauncherPulse(root, { running: 4, queued: 0, workspaces: ["/a", "/b"] });
    assert.match(text(), /daemon-wide/);
  });

  test("the reading offers a way in", () => {
    syncLauncherPulse(root, { running: 4, queued: 0, workspaces: [] });
    const btn = row().querySelector<HTMLButtonElement>("button");
    assert.ok(btn);
    btn.click();
    assert.deepEqual(opened, ["dashboard"]);
  });
});
