// status-dom.test.ts - who owns the connection control. The app in front and the shell's readiness
// poller both write to it, and the rule they share is the invisible data-owner stamp. A regression
// is silent in the worst way: the control keeps rendering, it just answers about the wrong thing.
// This file also pins what the control IS: a button whose state is a shape and a word as well as a
// colour, with its change announced once.
//
// document/window are registered globally by test-setup.mjs (node --import), same as the other
// *-dom tests.

import assert from "node:assert/strict";
import { describe, test, beforeEach } from "node:test";
import { buildConnection, connectionLabel, writeConnection } from "./connection";
import { publishStatus } from "./status";

const conn = () => document.getElementById("console-conn") as HTMLElement;
const count = () => document.getElementById("console-count") as HTMLElement;
const live = () => document.querySelector("[data-conn-status]") as HTMLElement;
const markClass = () =>
  conn().querySelector("[data-conn-mark] .pf-v6-c-icon__content")?.className ?? "";

// Scoped in a suite so the hook does not register on the ROOT context: with
// --test-isolation=none every -dom file shares one process, and a global hook here would wipe
// the fixtures the other files just built (see signin-dom.test.ts, which learned this first).
describe("the console status bar", () => {
  beforeEach(() => {
    location.hash = "";
    document.body.innerHTML =
      '<span id="console-count"></span><span id="console-observing"></span>';
    document.body.prepend(buildConnection());
  });

  test("an app reporting its own link claims the control", () => {
    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(connectionLabel(conn()), "connected");
    assert.equal(conn().dataset.state, "connected");
    assert.equal(conn().dataset.owner, "app");
  });

  // The whole point of the split: an app with nothing to say about the server must leave the
  // control alone rather than assert "not connected" about a link it never probed.
  test("an app with no link of its own leaves the control unclaimed", () => {
    publishStatus({ count: "2373 nodes" });
    assert.equal(conn().dataset.owner, undefined);
    assert.equal(connectionLabel(conn()), "not connected");
    assert.equal(count().textContent, "2373 nodes");
    assert.equal(count().hidden, false);
  });

  // count and observing describe the app's DATA, so they are written either way - the connection
  // half being absent must not take them down with it.
  test("data slots are written whether or not the control is claimed", () => {
    publishStatus({ count: "14 events", observing: { text: "run 3", title: "watching run 3" } });
    assert.equal(count().textContent, "14 events");
    assert.equal(document.getElementById("console-observing")?.textContent, "run 3");

    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(count().textContent, "");
    assert.equal(count().hidden, true);
  });

  // Demo is decided by the fragment, not by the app, and that override predates the ownership
  // stamp. A claimed control in demo mode still reads "demo".
  test("demo mode overrides a claimed label", () => {
    location.hash = "#demo";
    publishStatus({ connection: "connected", label: "connected" });
    assert.equal(connectionLabel(conn()), "demo");
    assert.equal(conn().dataset.state, "demo");
  });

  test("it is a real button, described by the probe sentence", () => {
    assert.equal(conn().tagName, "BUTTON");
    assert.equal(conn().getAttribute("type"), "button");
    assert.equal(conn().getAttribute("role"), null);
    assert.equal(conn().getAttribute("aria-live"), null, "a poll must not make the button chatter");
    writeConnection(conn(), {
      hint: "Not connected to 127.0.0.1:7391. Click to change the server address.",
    });
    const description = document.getElementById(conn().getAttribute("aria-describedby") ?? "");
    assert.match(description?.textContent ?? "", /Not connected to 127\.0\.0\.1:7391/);
  });

  // A dot that changes only hue says nothing to a reader who cannot tell the hues apart: a degraded
  // server and a healthy one are both "connected", so the difference has to be a shape and a word.
  test("health is a shape and a word, not only a colour", () => {
    publishStatus({ connection: "connected", label: "server ready" });
    assert.match(markClass(), /pf-m-success/);
    writeConnection(conn(), { health: "warn" });
    assert.match(markClass(), /pf-m-warning/);
    assert.match(live().textContent ?? "", /degraded/);
    writeConnection(conn(), { health: "fail" });
    assert.match(markClass(), /pf-m-danger/);
    assert.match(live().textContent ?? "", /failure/);
    // The icon is decoration; the state's word lives in the live region, not inside the button.
    assert.equal(conn().querySelector("[data-conn-mark]")?.textContent, "");
  });

  // The status region is written when the STATE changes. A poll that repeats it must not re-announce.
  test("an unchanged state does not rewrite the live status", () => {
    publishStatus({ connection: "connected", label: "server ready" });
    const node = live().firstChild;
    writeConnection(conn(), { label: "server ready", hint: "Connected to a host." });
    assert.equal(live().firstChild, node, "the same text node: nothing for a reader to hear again");
  });
});
