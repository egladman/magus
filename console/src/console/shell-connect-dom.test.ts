// shell-connect-dom.test.ts - the shell's connect page. A surface declared as needing a daemon does
// not activate with no address: the shell shows one connect page in its place, and the surface opens
// once the reader applies an address. Driven through a real tile, the way a launcher pick, a restored
// layout and a deep link all mount.

import assert from "node:assert/strict";
import { afterEach, beforeEach, describe, test } from "node:test";
import { requireDaemon } from "./connectPrompt";
import { signalAuthLost } from "../lib/daemon";
import type { PageController, PageModule } from "./page";
import { createTileView, type TileView } from "./tileView";
import { rememberHost, setDefaultHost } from "../lib/settings";

const HOST = "127.0.0.1:7391";
const PURPOSE = "Stub reads a running daemon.";

const settle = async (turns = 6): Promise<void> => {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
};

interface Stub {
  module: PageModule<unknown, unknown>;
  activations: number;
  deactivations: number;
  visible: boolean[];
}

function stubSurface(id: string): Stub {
  const stub: Stub = {
    activations: 0,
    deactivations: 0,
    visible: [],
    module: {
      id,
      title: id,
      async activate(host): Promise<PageController<unknown, unknown>> {
        stub.activations++;
        const main = document.createElement("main");
        main.dataset.stubSurface = id;
        main.textContent = "surface data";
        host.append(main);
        return {
          search: { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) },
          setVisible: (v) => stub.visible.push(v),
          deactivate: () => {
            stub.deactivations++;
            host.replaceChildren();
          },
        };
      },
    },
  };
  return stub;
}

function tileFor(
  modules: PageModule<unknown, unknown>[],
  seedPage: string,
): { tile: TileView; pane: () => HTMLElement } {
  const registry = new Map(modules.map((m) => [m.id, m]));
  const tile = createTileView({
    seed: { kind: "leaf", id: "p1", pageId: seedPage },
    surfaces: [],
    mountSurface: async (pageId, host) => (await registry.get(pageId)?.activate(host)) ?? null,
    onLayoutChange() {},
  });
  document.body.append(tile.el);
  tile.setVisible(true);
  const pane = (): HTMLElement => {
    const el = tile.el.querySelector<HTMLElement>("[data-pane-id]");
    assert.ok(el);
    return el;
  };
  return { tile, pane };
}

const TOKEN_KEY = "magus-live-token";

describe("the shell connect page", () => {
  const tiles: TileView[] = [];

  // Signed in: this suite is about the ADDRESS. The sign-in suite below covers the token.
  beforeEach(() => sessionStorage.setItem(TOKEN_KEY, "test-token"));

  afterEach(() => {
    for (const t of tiles.splice(0)) {
      t.deactivate();
      t.el.remove();
    }
    setDefaultHost("");
    rememberHost("");
    location.hash = "";
    sessionStorage.clear();
  });

  test("a daemon surface opened with no address shows the page, not the surface", async () => {
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 0, "the surface module must not activate");
    const page = pane().querySelector<HTMLElement>("[data-connect-page]");
    assert.ok(page, "the connect page stands in the surface's place");
    assert.equal(page.dataset.connectPage, "runs");
    assert.match(page.textContent ?? "", /No daemon connected/);
    assert.match(page.textContent ?? "", new RegExp(PURPOSE));
    assert.equal(pane().querySelector("[data-stub-surface]"), null);
  });

  test("applying an address opens the pending surface, once", async () => {
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    setDefaultHost(HOST);
    await settle();
    assert.equal(stub.activations, 1);
    assert.equal(pane().querySelector("[data-connect-page]"), null, "the page is gone");
    assert.ok(pane().querySelector("[data-stub-surface]"));
    assert.equal(stub.visible.at(-1), true, "it inherits the pane's visibility");
    setDefaultHost("127.0.0.1:7392");
    await settle();
    assert.equal(stub.activations, 1, "a later address change does not remount it");
  });

  test("an address applied while the pane is hidden waits until it is revealed", async () => {
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    tile.el.hidden = true;
    tile.setVisible(false);
    setDefaultHost(HOST);
    await settle();
    assert.equal(stub.activations, 0, "a hidden pane has no dimensions to activate into");
    tile.el.hidden = false;
    tile.setVisible(true);
    await settle();
    assert.equal(stub.activations, 1);
    assert.ok(pane().querySelector("[data-stub-surface]"));
  });

  test("with an address already applied the surface opens directly", async () => {
    setDefaultHost(HOST);
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 1);
    assert.equal(pane().querySelector("[data-connect-page]"), null);
  });

  test("demo mode opens a daemon surface directly", async () => {
    location.hash = "#demo";
    const stub = stubSurface("dashboard");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "dashboard");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 1);
    assert.equal(pane().querySelector("[data-connect-page]"), null);
  });

  test("a surface with no declared need is not wrapped and opens offline", async () => {
    const stub = stubSurface("logs");
    const wrapped = requireDaemon(stub.module, undefined);
    assert.equal(wrapped, stub.module);
    const { tile, pane } = tileFor([wrapped], "logs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 1);
    assert.equal(pane().querySelector("[data-connect-page]"), null);
  });

  test("a daemon that drops mid-session keeps the open surface and its data", async () => {
    setDefaultHost(HOST);
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    setDefaultHost("");
    await settle();
    assert.equal(stub.deactivations, 0, "the surface is not torn down");
    assert.equal(pane().querySelector("[data-connect-page]"), null, "no page replaces it");
    assert.equal(pane().querySelector("[data-stub-surface]")?.textContent, "surface data");
  });

  test("closing the pane before connecting leaves nothing to open later", async () => {
    const stub = stubSurface("runs");
    const { tile } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tile.deactivate();
    tile.el.remove();
    await settle();
    setDefaultHost(HOST);
    await settle();
    assert.equal(stub.activations, 0);
  });
});

// Every daemon route needs a bearer token, so a page with an address and no token can show nothing
// true. It shows one sign-in state naming the command that fixes it, and a token the daemon later
// refuses brings it back there.
describe("the shell sign-in gate", () => {
  const tiles: TileView[] = [];

  afterEach(() => {
    for (const t of tiles.splice(0)) {
      t.deactivate();
      t.el.remove();
    }
    setDefaultHost("");
    rememberHost("");
    location.hash = "";
    sessionStorage.clear();
  });

  test("an address with no token shows the sign-in state, not the surface", async () => {
    setDefaultHost(HOST);
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 0, "an unauthenticated surface must not activate");
    const page = pane().querySelector<HTMLElement>("[data-connect-page]");
    assert.ok(page);
    assert.match(page.textContent ?? "", /Sign in to this daemon/);
    const cmd = page.querySelector("[data-sign-in-command]")?.textContent ?? "";
    assert.match(
      cmd,
      /\/console\/runs\/#code=\$\(magus config console token create --code --expires 12h\)"$/,
    );
    assert.doesNotMatch(cmd, /test-token/);
  });

  test("demo needs no token", async () => {
    location.hash = "#demo";
    const stub = stubSurface("runs");
    const { tile } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 1);
  });

  test("a refused token tears the surface down and returns to sign-in with a notice", async () => {
    setDefaultHost(HOST);
    sessionStorage.setItem(TOKEN_KEY, "test-token");
    const stub = stubSurface("runs");
    const { tile, pane } = tileFor([requireDaemon(stub.module, { purpose: PURPOSE })], "runs");
    tiles.push(tile);
    await settle();
    assert.equal(stub.activations, 1);

    signalAuthLost(HOST);
    await settle();
    assert.equal(stub.deactivations, 1, "the surface is torn down");
    assert.equal(sessionStorage.getItem(TOKEN_KEY), null, "the refused token is forgotten");
    const text = pane().querySelector("[data-connect-page]")?.textContent ?? "";
    assert.match(text, /Sign in to this daemon/);
    assert.match(text, /expired or was revoked/);
  });
});
