import assert from "node:assert/strict";
import { test, beforeEach, afterEach } from "node:test";
import { activate } from "./main";
import { rejected } from "../../lib/rejected";

const realFetch = globalThis.fetch;

beforeEach(() => {
  localStorage.clear();
  sessionStorage.clear();
  document.body.replaceChildren();
  globalThis.fetch = (() =>
    rejected(new Error("demo mode must not reach the network"))) as typeof fetch;
});

afterEach(() => {
  globalThis.fetch = realFetch;
  location.hash = "";
});

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

test("the toolbar links into the graph's affected view", async () => {
  location.hash = "#demo";
  const app = activate(document.body);
  await settle();

  const link = document.querySelector<HTMLAnchorElement>(".console-diff-toolbar__graph");
  assert.equal(link?.getAttribute("href"), "graph/#demo&view=affected");

  app.deactivate();
});
