// signin-dom.test.ts - the Connect screen's guards and what each way out of it does to the two things
// it remembers. Untested until now, and the guards are the whole file: this screen exists to stop a
// tab inheriting a scope nobody chose, so a guard that misfires either nags on every status tick or
// never appears at all. Both failures are silent.

import assert from "node:assert/strict";
import { describe, test, beforeEach, afterEach } from "node:test";
import { maybeAskWorkspace } from "./signin";
import { ALL_WORKSPACES, workspaceScope } from "../lib/scope";

const ACME = "/Users/eli/Repos/acme";
const MAGUS = "/Users/eli/Repos/magus";
const BOTH = [ACME, MAGUS];

const ASKED_KEY = "magus:workspace-asked";
const LAST_KEY = "magus:workspace-last";
const SCOPE_KEY = "magus:workspace-scope";
const TOKEN_KEY = "magus-live-token";

function screen(): HTMLElement | null {
  return document.querySelector<HTMLElement>(".console-shell-signin");
}

function choices(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>(".console-shell-signin__choice")];
}

function dismiss(key: string): void {
  document.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true }));
}

// Everything below lives in a suite so its beforeEach is SCOPED. A top-level hook registers on the
// root context, and with --test-isolation=none every -dom test file shares one process - so a global
// hook here would run before every other file's tests too, and wipe the fixtures they just built.
describe("the connect screen", () => {
  // Closing happens in afterEach, not beforeEach. signin.ts keeps an `asking` latch that only its own
  // close() clears, and other -dom files in this shared process empty document.body before each test -
  // so a screen left standing gets orphaned rather than dismissed, and the latch sticks true for the
  // rest of the run, silently guarding out every case that needs the screen to open.
  afterEach(() => {
    if (screen()) dismiss("Escape");
    for (const el of document.querySelectorAll(".console-shell-signin")) el.remove();
  });

  beforeEach(() => {
    // Only the keys this file owns. Clearing the stores wholesale would take state other suites in
    // the same process set up at module load. This runs AFTER the dismiss above, which marks the tab
    // as asked - so clearing here is what lets the next case ask again.
    for (const k of [ASKED_KEY, LAST_KEY, SCOPE_KEY, TOKEN_KEY]) {
      sessionStorage.removeItem(k);
      localStorage.removeItem(k);
    }
  });

  test("two workspaces and no scope yet is the one case that asks", () => {
    maybeAskWorkspace(BOTH);
    assert.ok(screen(), "the screen should be up");
    assert.equal(choices().length, 2);
  });

  // One workspace is one possible answer. Asking would be a question whose only effect is a click.
  test("a single workspace is not a question", () => {
    maybeAskWorkspace([ACME]);
    assert.equal(screen(), null);
  });

  test("a tab that already knows where it is is not asked again", () => {
    sessionStorage.setItem(SCOPE_KEY, ACME);
    maybeAskWorkspace(BOTH);
    assert.equal(screen(), null);
  });

  // The guard that matters most in practice: the shell now calls this from the pulse poll AND from the
  // workspace-list event, so it runs several times a minute for the life of the page.
  test("answering once is answering for the browser tab", () => {
    maybeAskWorkspace(BOTH);
    choices()[0].click();
    assert.equal(screen(), null);
    assert.equal(sessionStorage.getItem(ASKED_KEY), "1");

    // Even back at the daemon-wide scope, which is what dismissing leaves behind.
    sessionStorage.removeItem(SCOPE_KEY);
    maybeAskWorkspace(BOTH);
    assert.equal(screen(), null, "a status tick must not reopen the screen");
  });

  test("picking a workspace scopes the tab and is remembered for the next one", () => {
    maybeAskWorkspace(BOTH);
    const magus = choices().find((b) => b.title === MAGUS);
    assert.ok(magus);
    magus.click();
    assert.equal(workspaceScope(), MAGUS);
    assert.equal(localStorage.getItem(LAST_KEY), MAGUS);
  });

  // Escape has to land somewhere, and the honest destination is the scope that hides nothing.
  test("Escape lands on the daemon-wide scope", () => {
    maybeAskWorkspace(BOTH);
    dismiss("Escape");
    assert.equal(screen(), null);
    assert.equal(workspaceScope(), ALL_WORKSPACES);
  });

  // Dismissing answers THIS tab's question. It is not a statement about the workspace you usually want,
  // and treating it as one charged the next browser tab an extra click for it.
  test("dismissing does not erase the remembered workspace", () => {
    localStorage.setItem(LAST_KEY, ACME);
    maybeAskWorkspace(BOTH);
    dismiss("Escape");
    assert.equal(localStorage.getItem(LAST_KEY), ACME);
  });

  test("watching all workspaces does not erase it either", () => {
    localStorage.setItem(LAST_KEY, ACME);
    maybeAskWorkspace(BOTH);
    document.querySelector<HTMLButtonElement>(".console-shell-signin__all")?.click();
    assert.equal(workspaceScope(), ALL_WORKSPACES);
    assert.equal(localStorage.getItem(LAST_KEY), ACME);
  });

  // The remembered pick is preselected, never auto-applied: the click still happens, because an
  // inherited scope nobody chose is the failure this screen exists to prevent.
  test("the remembered workspace is marked but not applied", () => {
    localStorage.setItem(LAST_KEY, MAGUS);
    maybeAskWorkspace(BOTH);
    const marked = choices().filter((b) => b.dataset.suggested !== undefined);
    assert.equal(marked.length, 1);
    assert.equal(marked[0].title, MAGUS);
    assert.equal(workspaceScope(), ALL_WORKSPACES, "preselecting must not decide");
  });

  // aria-modal claims nothing outside the box exists. Without a trap, Tab walks straight out into the
  // console behind it and the two accounts of where the user is disagree.
  test("Tab is trapped inside the dialog", () => {
    maybeAskWorkspace(BOTH);
    const box = document.querySelector<HTMLElement>(".pf-v6-c-modal-box");
    assert.ok(box);
    const stops = [...box.querySelectorAll<HTMLElement>("button")];
    assert.ok(stops.length >= 3);

    stops[stops.length - 1].focus();
    dismiss("Tab");
    assert.equal(document.activeElement, stops[0], "the last stop should wrap to the first");
  });

  // A credential is shown as evidence, never in full - enough to confirm one arrived and to tell two
  // apart, never enough to read over a shoulder or lift out of a screenshot.
  test("a credential is shown by its tail, never whole", () => {
    const token = "magus_live_supersecret_abcd";
    sessionStorage.setItem(TOKEN_KEY, token);
    maybeAskWorkspace(BOTH);
    const shown = screen()?.textContent ?? "";
    assert.ok(shown.includes("abcd"), "the tail identifies which credential arrived");
    assert.ok(!shown.includes(token), "the whole token must never reach the DOM");
  });
});
