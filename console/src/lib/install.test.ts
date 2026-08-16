import test from "node:test";
import assert from "node:assert/strict";
import { must } from "./guards";
import {
  createInstallStore,
  type BeforeInstallPromptEvent,
  type InstallHost,
  type InstallState,
} from "./install";

// These pin the store's state machine: what the Install button may claim at each point in the
// browser's install lifecycle. The real window binding (matchMedia, navigator.standalone) is a thin
// adapter exercised in the browser, not here.

// A fake host plus the two event hooks the store listens on, so a test can drive the browser's side of
// the lifecycle directly.
function fakeHost(opts: Partial<Pick<InstallHost, "installed" | "hasPromptApi" | "isIOS">> = {}): {
  host: InstallHost;
  fire(type: string, ev: Event): void;
} {
  const handlers = new Map<string, (ev: Event) => void>();
  return {
    host: {
      on: (type, fn) => handlers.set(type, fn),
      installed: opts.installed ?? (() => false),
      hasPromptApi: opts.hasPromptApi ?? (() => true),
      isIOS: opts.isIOS ?? (() => false),
    },
    fire: (type, ev) => must(handlers.get(type))(ev),
  };
}

// A stand-in for the Chromium event. Records whether the store neutralized the browser's own infobar
// and how many times it prompted, since the real event permits exactly one prompt().
function fakePrompt(outcome: "accepted" | "dismissed"): BeforeInstallPromptEvent & {
  prevented: boolean;
  prompts: number;
} {
  const ev = new Event("beforeinstallprompt") as Event & {
    prevented: boolean;
    prompts: number;
    prompt(): Promise<void>;
    userChoice: Promise<{ outcome: "accepted" | "dismissed"; platform: string }>;
  };
  ev.prevented = false;
  ev.prompts = 0;
  ev.preventDefault = () => {
    ev.prevented = true;
  };
  ev.prompt = () => {
    ev.prompts++;
    return Promise.resolve();
  };
  ev.userChoice = Promise.resolve({ outcome, platform: "web" });
  return ev as BeforeInstallPromptEvent & { prevented: boolean; prompts: number };
}

test("a browser with no install API reports the manual route, not a failure", () => {
  const { host } = fakeHost({ hasPromptApi: () => false });
  const s = createInstallStore(host);
  assert.equal(s.state(), "manual");
  assert.match(s.manualHint(), /Add to Dock/);
});

test("iOS gets its own manual route (the Share sheet, not a menu)", () => {
  const { host } = fakeHost({ hasPromptApi: () => false, isIOS: () => true });
  const s = createInstallStore(host);
  assert.equal(s.state(), "manual");
  assert.match(s.manualHint(), /Add to Home Screen/);
});

test("the API present but silent is pending, not manual - the criteria are simply unmet", () => {
  const { host } = fakeHost({ hasPromptApi: () => true });
  const s = createInstallStore(host);
  assert.equal(s.state(), "pending");
  assert.equal(s.manualHint(), "", "a hint would be a lie: there is no manual route to name here");
});

test("already running as an app reports installed, whatever the API says", () => {
  const { host } = fakeHost({ installed: () => true });
  assert.equal(createInstallStore(host).state(), "installed");
});

test("the captured offer flips the state to ready and suppresses the browser's own infobar", () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  const seen: InstallState[] = [];
  s.subscribe((st) => seen.push(st));

  const ev = fakePrompt("accepted");
  fire("beforeinstallprompt", ev);
  assert.equal(ev.prevented, true, "preventDefault is what makes the offer deferrable");
  assert.equal(s.state(), "ready");
  assert.deepEqual(seen, ["ready"]);
});

test("prompting with no captured offer is unavailable, never a silent no-op", async () => {
  const { host } = fakeHost();
  assert.equal(await createInstallStore(host).prompt(), "unavailable");
});

test("accepting installs, and the offer is spent after one prompt", async () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  const ev = fakePrompt("accepted");
  fire("beforeinstallprompt", ev);

  assert.equal(await s.prompt(), "accepted");
  assert.equal(ev.prompts, 1);
  assert.equal(s.state(), "installed");
  // The event permits exactly one prompt(); a second call must not reach it.
  assert.equal(await s.prompt(), "unavailable");
  assert.equal(ev.prompts, 1);
});

test("declining lands in dismissed, so the surface can say reload rather than offer a dead button", async () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  fire("beforeinstallprompt", fakePrompt("dismissed"));

  assert.equal(await s.prompt(), "dismissed");
  assert.equal(s.state(), "dismissed");
  assert.equal(s.manualHint(), "");
});

test("a re-offer after a decline supersedes it", async () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  fire("beforeinstallprompt", fakePrompt("dismissed"));
  await s.prompt();
  assert.equal(s.state(), "dismissed");

  fire("beforeinstallprompt", fakePrompt("accepted"));
  assert.equal(s.state(), "ready");
});

test("an install started outside the page (the browser's own menu) still lands as installed", () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  fire("beforeinstallprompt", fakePrompt("accepted"));
  assert.equal(s.state(), "ready");

  fire("appinstalled", new Event("appinstalled"));
  assert.equal(s.state(), "installed");
});

test("unsubscribe stops delivery", () => {
  const { host, fire } = fakeHost();
  const s = createInstallStore(host);
  let calls = 0;
  const off = s.subscribe(() => calls++);
  fire("beforeinstallprompt", fakePrompt("accepted"));
  assert.equal(calls, 1);
  off();
  fire("appinstalled", new Event("appinstalled"));
  assert.equal(calls, 1);
});
