// alerts-dom.test.ts - the Big Picture alert rail. document/window are registered globally by
// test-setup.mjs (node --import), so these run under node:test like view-dom.test.ts.
//
// What is worth pinning here is the ADMISSION and COALESCING policy, not the markup. The rail is
// the only channel telling a room that something broke while the console's own chrome is hidden, so
// the failures that matter are "it stayed quiet when it should have spoken" and "it buried the one
// line that mattered under fourteen others" - both of which are decisions this module makes and
// neither of which a screenshot would catch.

import assert from "node:assert/strict";
import { test, beforeEach } from "node:test";
import { NOTIFY_EVENT } from "../../../lib/notifications";
import { viewMode } from "./bigPicture";
import { mountAlertRail } from "./alerts";

function fire(detail: Record<string, unknown>): void {
  document.dispatchEvent(new CustomEvent(NOTIFY_EVENT, { detail }));
}

// The rail is shown by a data attribute, not by `hidden`: display is not animatable, so toggling it
// made the rail snap in and out. Asserting on the attribute keeps these tests pinned to the
// behavior (is it announcing?) rather than to the mechanism that produces it.
function shown(el: HTMLElement): boolean {
  return el.hasAttribute("data-shown");
}

function lines(el: HTMLElement): string[] {
  return [...el.querySelectorAll(".console-dashboard-alertrail__card")].map(
    (l) => (l as HTMLElement).innerText || (l.textContent ?? ""),
  );
}

function actions(el: HTMLElement): HTMLAnchorElement[] {
  return [...el.querySelectorAll<HTMLAnchorElement>(".console-dashboard-alertrail__action")];
}

beforeEach(() => {
  viewMode.set("board");
});

test("stays silent on the board, where the notification bell is visible", () => {
  const rail = mountAlertRail();
  fire({ source: "Dashboard", kind: "error", key: "a", message: "svc/api:test failed." });
  assert.equal(shown(rail.el), false, "the board has its own channel; a second one is noise");
  rail.destroy();
});

test("admits the bell tier and refuses history-tier notifications", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");

  fire({ source: "Settings", kind: "ok", key: "ok1", message: "Theme saved." });
  assert.equal(shown(rail.el), false, "a wall board that announces routine success gets ignored");

  fire({ source: "Dashboard", kind: "error", key: "e1", message: "svc/api:test failed." });
  assert.equal(shown(rail.el), true);
  assert.equal(lines(rail.el).length, 1);

  // important decouples the bell tier from severity: a non-failure can still need a human.
  fire({
    source: "Share",
    kind: "warn",
    key: "w1",
    important: true,
    message: "A device connected.",
  });
  assert.equal(lines(rail.el).length, 2);
  rail.destroy();
});

test("coalesces a burst from one source into a single counted line", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");

  // What a single failing CI run actually looks like: distinct keys, one source.
  for (const target of ["svc/api:test", "lib/core:test", "web/app:build", "cli:lint"]) {
    fire({ source: "Dashboard", kind: "error", key: target, message: target + " failed." });
  }
  const rendered = lines(rail.el);
  assert.equal(
    rendered.length,
    1,
    "four lines saying the same thing is a wall of noise at distance",
  );
  assert.match(rendered[0], /x4/, "the count must survive the collapse, or it is silent data loss");
  rail.destroy();
});

test("dedupes a repeated key so a re-detected failure does not re-alert every frame", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");

  // The dashboard re-derives failing targets from EVERY status frame (~1s), so the same failure is
  // raised over and over. The store drops these before the bell; the rail sits alongside the store,
  // not downstream of it, so it has to make the same call itself.
  for (let i = 0; i < 5; i++) {
    fire({ source: "Dashboard", kind: "error", key: "same", message: "svc/api:test failed." });
  }
  assert.equal(lines(rail.el).length, 1);
  assert.doesNotMatch(lines(rail.el)[0], /x[2-9]/, "one event, re-observed, is still one event");
  rail.destroy();
});

test("carries the notification's deep link onto the card as an action", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");

  // The dashboard sets exactly this on every failing target, pointed at its captured output. The
  // rail used to drop it, which left the one alert channel a wall display has announcing that
  // something broke and offering no way to go and look at it.
  fire({
    source: "Dashboard",
    kind: "error",
    key: "linked",
    message: "svc/api:test failed.",
    link: { label: "Open in log viewer", href: "../logs/#ref=out1a2b3c" },
  });
  const [action] = actions(rail.el);
  assert.ok(action, "an alert with a link must render an action");
  assert.equal(action.getAttribute("href"), "../logs/#ref=out1a2b3c");
  assert.equal(action.textContent, "Open in log viewer");
  // A new tab: following it must never navigate the board itself away, which on a cast screen
  // nobody is standing at is not recoverable.
  assert.equal(action.getAttribute("target"), "_blank");
  rail.destroy();
});

test("an alert with no link renders no action rather than a dead one", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");
  fire({ source: "Daemon", kind: "error", key: "bare", message: "Daemon health is down." });
  assert.equal(actions(rail.el).length, 0);
  assert.equal(lines(rail.el).length, 1, "the alert itself still shows");
  rail.destroy();
});

test("leaving Big Picture clears the rail", () => {
  const rail = mountAlertRail();
  viewMode.set("bigPicture");
  fire({ source: "Dashboard", kind: "error", key: "z", message: "svc/api:test failed." });
  assert.equal(shown(rail.el), true);

  viewMode.set("board");
  assert.equal(
    shown(rail.el),
    false,
    "the bell is visible again; a stale rail would contradict it",
  );
  rail.destroy();
});
