// Mounts the Pulls app on its demo board and expands a row.

import assert from "node:assert/strict";
import { test as nodeTest } from "node:test";
import { activate } from "./main";
import { CHECKOUT_NOTE, renderCheckout } from "./view";
import type { AppInstance } from "../../desktop/standalone";

let mounted: AppInstance | null = null;
let host: HTMLElement;

// Not a root hook: with isolation off, a root hook runs for every test.
function test(name: string, fn: () => Promise<void> | void): void {
  nodeTest(name, async () => {
    localStorage.clear();
    document.body.replaceChildren();
    location.hash = "#demo";
    host = document.createElement("div");
    document.body.append(host);
    try {
      await fn();
    } finally {
      mounted?.deactivate();
      mounted = null;
      location.hash = "";
    }
  });
}

async function settle(turns = 6): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

const text = (sel: string): string => host.querySelector(sel)?.textContent ?? "";

test("the demo board shows the sync line, main, patterns, order and rows", async () => {
  mounted = activate(host);
  await settle();
  assert.match(
    text("[data-part=sync]"),
    /^synced \d+s ago · next in \d+s · checks running: every 1m · 4210\/5000 left$/,
  );
  assert.equal(text("[data-part=filter]"), "authors: egladman (you, default)");
  assert.ok(text("section[aria-label=Main]").includes("CI green"));
  assert.ok(text("section[aria-label=Patterns]").includes("runner backlog"));
  assert.equal(host.querySelectorAll("section[aria-label='Merge order'] li").length, 3);
  assert.equal(host.querySelectorAll("details[data-pull]").length, 3);
});

test("a row expands in place to its failed jobs", async () => {
  mounted = activate(host);
  await settle();
  const row = host.querySelector<HTMLDetailsElement>("details[data-pull='643']");
  assert.ok(row);
  assert.equal(row.open, false);
  row.open = true;
  assert.ok(row.querySelector("pre")?.textContent?.includes("src/session.ts:41 unused import"));
  assert.match(row.querySelector("code")?.textContent ?? "", /^magus query output grd/);
  assert.ok(
    row.querySelector("summary [data-field=blocked]")?.textContent?.includes("review required"),
  );
  assert.equal(host.querySelectorAll("details[data-pull]").length, 3);
});

test("renderCheckout states what the checkout does before it runs", () => {
  const el = renderCheckout(7, undefined, () => {});
  assert.equal(el.querySelector("p")?.textContent, CHECKOUT_NOTE);
  assert.equal(el.querySelector("button")?.textContent, "Check out locally");
  assert.equal(el.querySelector("button")?.disabled, false);
});

test("renderCheckout clicks report the pull number", () => {
  const seen: number[] = [];
  renderCheckout(7, undefined, (n) => seen.push(n))
    .querySelector("button")
    ?.click();
  assert.deepEqual(seen, [7]);
});

test("renderCheckout shows the error text on failure", () => {
  const el = renderCheckout(7, { kind: "failed", error: "fatal: no such ref" }, () => {});
  assert.equal(el.querySelector("[role=alert]")?.textContent, "fatal: no such ref");
});

test("renderCheckout disables the button while it runs", () => {
  const el = renderCheckout(7, { kind: "running" }, () => {});
  assert.equal(el.querySelector("button")?.disabled, true);
});

test("the checkout button shows the path, remove command and log in the row", async () => {
  mounted = activate(host);
  await settle();
  const row = host.querySelector<HTMLDetailsElement>("details[data-pull='640']");
  assert.ok(row);
  row.querySelector<HTMLButtonElement>("section[aria-label='Local checkout'] button")?.click();
  await settle();
  const after = host.querySelector("details[data-pull='640'] section[aria-label='Local checkout']");
  const codes = [...(after?.querySelectorAll("code") ?? [])].map((c) => c.textContent);
  assert.deepEqual(codes, [
    "/home/egladman/.cache/magus/tmp/checkout-640",
    "magus vcs checkout --remove /home/egladman/.cache/magus/tmp/checkout-640",
  ]);
  assert.equal(
    after?.querySelector("pre")?.textContent,
    "checked out the head commit with git\ninstalled: go, console",
  );
});

test("removing the author chip writes an empty filter", async () => {
  mounted = activate(host);
  await settle();
  host.querySelector<HTMLButtonElement>("ul[aria-label='Tracked authors'] button")?.click();
  await settle();
  assert.equal(text("[data-part=filter]"), "every author");
});
