// connectPrompt-dom.test.ts - the one prompt a surface shows while it has no server, and the
// behavior around it that is easy to get wrong.
//
// Pinned here: every way forward is a control the reader presses, the docs link is present
// wherever the reader is stuck, re-rendering the same prompt keeps the same buttons, an address
// applied in another bundle reaches this one, and a surface never paints an answer from an address
// the reader has already moved off.

import assert from "node:assert/strict";
import { afterEach, describe, test } from "node:test";
import { Code, ConnectError } from "@connectrpc/connect";
import {
  SERVER_GUIDE_URL,
  REQUEST_SERVER_SETTINGS_EVENT,
  renderConnectPrompt,
  renderEmptyMessage,
  type EmptyStateSlots,
} from "./connectPrompt";
import { buildLauncher, syncLauncherConnectPrompt } from "./home";
import { activate as activateActivity } from "./activity/main";
import { isUnreachable } from "../lib/server";
import {
  DEFAULT_HOST_EVENT,
  getDefaultHost,
  setDefaultHost,
  subscribeDefaultHost,
} from "../lib/settings";

const HOST = "127.0.0.1:7391";
const OTHER_HOST = "127.0.0.1:7392";

function newSlots(): EmptyStateSlots {
  return {
    title: document.createElement("h1"),
    message: document.createElement("p"),
    actions: document.createElement("div"),
  };
}

function controlLabels(slots: EmptyStateSlots): string[] {
  return [...slots.actions.querySelectorAll(".pf-v6-c-button")].map((b) =>
    (b.textContent ?? "").trim(),
  );
}

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

describe("renderConnectPrompt", () => {
  test("with no address it offers to set one, the guide, and the demo", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "none" }, { purpose: "Purpose line." });
    assert.equal(slots.title.textContent, "No server connected");
    assert.equal(slots.message.textContent, "Purpose line.");
    assert.deepEqual(controlLabels(slots), ["Set server address", "Setup guide"]);
    assert.equal(slots.actions.querySelector<HTMLAnchorElement>("a")?.href, SERVER_GUIDE_URL);
    assert.match(slots.actions.textContent ?? "", /Try the demo/);
  });

  test("Set server address asks the shell to open the field", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "none" });
    let requests = 0;
    const onRequest = (): void => {
      requests++;
    };
    document.addEventListener(REQUEST_SERVER_SETTINGS_EVENT, onRequest);
    try {
      slots.actions.querySelector<HTMLElement>(".pf-m-primary")?.click();
    } finally {
      document.removeEventListener(REQUEST_SERVER_SETTINGS_EVENT, onRequest);
    }
    assert.equal(requests, 1);
  });

  test("a disconnected server names the address and retries only when pressed", () => {
    const slots = newSlots();
    let retries = 0;
    renderConnectPrompt(
      slots,
      { connection: "disconnected", host: HOST, reason: "refused" },
      { onRetry: () => retries++ },
    );
    assert.equal(slots.title.textContent, "Could not reach the server");
    assert.equal(slots.message.textContent, "The console could not reach " + HOST + " (refused).");
    assert.deepEqual(controlLabels(slots), ["Retry", "Change address", "Setup guide"]);
    assert.equal(retries, 0);
    slots.actions.querySelector<HTMLElement>(".pf-m-primary")?.click();
    assert.equal(retries, 1);
  });

  test("without onRetry there is no Retry button", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "disconnected", host: HOST });
    assert.deepEqual(controlLabels(slots), ["Change address", "Setup guide"]);
  });

  test("connecting offers nothing to press", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "connecting", host: HOST });
    assert.equal(slots.title.textContent, "Connecting");
    assert.equal(slots.actions.childElementCount, 0);
  });

  test("the same prompt keeps the same buttons, so a poll cannot steal focus", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "disconnected", host: HOST });
    const before = slots.actions.querySelector(".pf-v6-c-button");
    renderConnectPrompt(slots, { connection: "disconnected", host: HOST });
    assert.equal(slots.actions.querySelector(".pf-v6-c-button"), before);
  });

  test("an empty message in between makes the next prompt draw again", () => {
    const slots = newSlots();
    renderConnectPrompt(slots, { connection: "none" });
    renderEmptyMessage(slots, "No activity yet", "Nothing recorded.");
    assert.equal(slots.title.textContent, "No activity yet");
    assert.equal(slots.actions.childElementCount, 0);
    renderConnectPrompt(slots, { connection: "none" });
    assert.equal(slots.title.textContent, "No server connected");
    assert.notEqual(slots.actions.childElementCount, 0);
  });
});

describe("isUnreachable", () => {
  test("separates no response from an error the server sent", () => {
    assert.equal(isUnreachable(new TypeError("Failed to fetch")), true);
    assert.equal(isUnreachable(ConnectError.from(new TypeError("Failed to fetch"))), true);
    assert.equal(isUnreachable(new ConnectError("deadline", Code.DeadlineExceeded)), true);
    assert.equal(isUnreachable(new ConnectError("boom", Code.Internal)), false);
    assert.equal(isUnreachable(new Error("HTTP 500")), false);
  });
});

describe("the default host", () => {
  afterEach(() => setDefaultHost(""));

  // Every surface bundle holds its own copy of the host cell. The in-document event is how an
  // address applied in the shell's copy reaches theirs; dispatching it by hand is what another
  // bundle's setDefaultHost does.
  test("an address announced by another bundle is applied and reaches subscribers", () => {
    let calls = 0;
    const unsubscribe = subscribeDefaultHost(() => calls++);
    try {
      document.dispatchEvent(new CustomEvent(DEFAULT_HOST_EVENT, { detail: OTHER_HOST }));
      assert.equal(getDefaultHost(), OTHER_HOST);
      assert.equal(calls, 1);
      document.dispatchEvent(new CustomEvent(DEFAULT_HOST_EVENT, { detail: 7392 }));
      assert.equal(getDefaultHost(), OTHER_HOST, "a detail that is not a string is ignored");
      setDefaultHost(OTHER_HOST);
      assert.equal(calls, 1, "applying the address already in place notifies nobody");
    } finally {
      unsubscribe();
    }
  });
});

describe("syncLauncherConnectPrompt", () => {
  test("replaces the ways while no server answers, and gives them back when one does", () => {
    const root = buildLauncher([], () => {});
    const connect = root.querySelector<HTMLElement>("[data-launcher-connect]");
    const ways = root.querySelector<HTMLElement>("[data-launcher-ways]");
    assert.ok(connect && ways);
    assert.equal(connect.hidden, true, "nothing is claimed before the first poll answers");

    syncLauncherConnectPrompt(root, { connection: "none" });
    assert.equal(connect.hidden, false);
    assert.equal(ways.hidden, true);
    assert.match(connect.textContent ?? "", /No server connected/);

    syncLauncherConnectPrompt(root, null);
    assert.equal(connect.hidden, true);
    assert.equal(ways.hidden, false);
  });
});

describe("Activity", () => {
  const realFetch = globalThis.fetch;
  let deactivate: (() => void) | null = null;

  afterEach(() => {
    deactivate?.();
    deactivate = null;
    setDefaultHost("");
    globalThis.fetch = realFetch;
    document.body.replaceChildren();
    localStorage.clear();
  });

  function mount(): HTMLElement {
    const host = document.createElement("div");
    document.body.append(host);
    const surface = activateActivity(host);
    deactivate = () => surface.deactivate();
    return host;
  }

  const title = (host: HTMLElement): string =>
    (host.querySelector(".pf-v6-c-empty-state__title-text")?.textContent ?? "").trim();

  // Activity once read only the host the dashboard remembered, so an address set in Settings left
  // it saying "No server connected" until the dashboard had been opened.
  test("reads the Settings address, and follows it when it changes", async () => {
    globalThis.fetch = (() => Promise.reject(new TypeError("refused"))) as typeof fetch;
    setDefaultHost(HOST);
    const host = mount();
    await settle();
    assert.equal(title(host), "Could not reach the server");
    assert.ok((host.textContent ?? "").includes(HOST));

    setDefaultHost("");
    await settle();
    assert.equal(title(host), "No server connected");
  });

  // The slow address answering last must not repaint the page for an address nobody is on any more.
  test("an answer from the previous address is dropped", async () => {
    let failFirst: (e: unknown) => void = () => {};
    globalThis.fetch = ((input: RequestInfo | URL) => {
      const url = String(input instanceof Request ? input.url : input);
      if (url.includes(HOST)) return new Promise((_, reject) => (failFirst = reject));
      return Promise.reject(new TypeError("refused"));
    }) as typeof fetch;
    setDefaultHost(HOST);
    const host = mount();
    await settle();
    assert.equal(title(host), "Connecting");

    setDefaultHost(OTHER_HOST);
    await settle();
    assert.ok((host.textContent ?? "").includes(OTHER_HOST));

    failFirst(new TypeError("refused"));
    await settle();
    assert.ok(
      (host.textContent ?? "").includes(OTHER_HOST),
      "the newer address still owns the page",
    );
    assert.ok(!(host.textContent ?? "").includes(HOST + " "), "the older address never painted");
  });
});
