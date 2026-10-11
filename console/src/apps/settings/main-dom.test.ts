// main-dom.test.ts - the Settings app's mount. What is pinned is how the page reads and behaves:
// every row on every tab in one form layout, controls named and described, a boolean as a switch and
// a choice as radios, helper text a control actually points at, a status line that leaves no dead
// band, and a revoke that asks in the page's own modal.

import assert from "node:assert/strict";
import { test as nodeTest } from "node:test";
import { settingsApp, type SettingsDeps } from "./main";
import { persisted, type Persisted } from "../../lib/persist";
import { setDefaultHost } from "../../lib/settings";
import type { Keymap } from "../../desktop/commands";
import type { PageController } from "../../desktop/page";

const realFetch = globalThis.fetch;
let host: HTMLElement;
let page: PageController<null, null> | null = null;

// Set up and torn down around each test here rather than in root-level hooks: with the runner's
// isolation off, a root-level hook runs for every test in the process.
function test(name: string, fn: () => Promise<void> | void): void {
  nodeTest(name, async () => {
    localStorage.clear();
    sessionStorage.clear();
    document.body.replaceChildren();
    host = document.createElement("div");
    document.body.append(host);
    // The app adds its stylesheet link once, by id; happy-dom would try to fetch a file: URL for it.
    document.getElementById("app-css-settings")?.remove();
    const sheet = document.createElement("meta");
    sheet.id = "app-css-settings";
    document.head.append(sheet);
    try {
      await fn();
    } finally {
      page?.deactivate();
      page = null;
      setDefaultHost("");
      globalThis.fetch = realFetch;
    }
  });
}

async function settle(turns = 12): Promise<void> {
  for (let i = 0; i < turns; i++) await new Promise((r) => setTimeout(r, 0));
}

function deps(): SettingsDeps {
  const keymap: Persisted<Keymap> = persisted<Keymap>("settings-test-keymap", {});
  return {
    keybindings: {
      commands: [
        { id: "go.next", label: "Next tab", group: "Tabs", run: () => {} },
        { id: "go.prev", label: "Previous tab", group: "Tabs", run: () => {} },
      ],
      defaults: { "go.next": "mod+]", "go.prev": "mod+[" },
      keymap,
    },
    presets: { default: {}, vim: { "go.next": "g t" } },
    presetList: [
      { id: "default", label: "Default" },
      { id: "vim", label: "Vim" },
    ],
    install: {
      state: () => "ready",
      manualHint: () => "",
      prompt: () => Promise.resolve("accepted"),
      subscribe: () => () => {},
    },
  };
}

async function mount(): Promise<void> {
  page = await settingsApp(deps()).activate(host);
  await settle();
}

const byId = (id: string): Element | null =>
  id === "" ? null : document.querySelector("#" + CSS.escape(id));

const q = <T extends Element>(sel: string): T => {
  const el = host.querySelector<T>(sel);
  assert.ok(el, "no " + sel);
  return el;
};

test("every settings row sits in a PF horizontal form, so the tab reads as one layout", async () => {
  await mount();
  const forms = host.querySelectorAll("form.pf-v6-c-form.pf-m-horizontal");
  assert.equal(forms.length, 3, "connection, appearance and keymap preset");
  for (const form of forms) {
    assert.ok(form.querySelector(".pf-v6-c-form__group .pf-v6-c-form__group-label"));
  }
  assert.equal(host.querySelectorAll(".pf-v6-c-toggle-group").length, 1, "only the diff view");
});

test("a choice is radios, a boolean is a switch, and each group is named by its label", async () => {
  await mount();
  const theme = [...host.querySelectorAll("[role=radiogroup]")].find((g) =>
    g.textContent?.includes("Light"),
  );
  assert.ok(theme);
  assert.equal(theme.querySelectorAll("input[type=radio]").length, 3);
  const labelledBy = theme.getAttribute("aria-labelledby") ?? "";
  assert.equal(byId(labelledBy)?.textContent, "Theme");

  const switches = host.querySelectorAll("input[role=switch]");
  assert.equal(switches.length, 2, "focus ring and node shapes");
  for (const s of switches) {
    assert.ok(byId(s.getAttribute("aria-labelledby") ?? ""));
  }
});

test("helper text is wired to its control, and the setup guide is in it, not in the input group", async () => {
  await mount();
  const input = q<HTMLInputElement>("#console-settings-host");
  assert.equal(input.placeholder, "Example 127.0.0.1:7391");
  for (const id of (input.getAttribute("aria-describedby") ?? "").split(" ")) {
    assert.ok(byId(id), "describedby " + id + " resolves");
  }
  const guide = [...host.querySelectorAll("a")].find((a) => a.textContent === "Setup guide");
  assert.ok(guide);
  assert.ok(guide.closest(".pf-v6-c-helper-text"));
  assert.equal(guide.closest(".pf-v6-c-input-group"), null);
});

test("the status line leaves no band when empty, and the action bar explains Save vs Save & Apply in text", async () => {
  await mount();
  assert.equal(q<HTMLElement>(".console-settings-actionbar__status").hidden, true);
  const note = q(".console-settings-actionbar__note");
  assert.match(note.textContent ?? "", /Save & Apply applies/);
  assert.match(note.textContent ?? "", /Save keeps/);
  for (const b of host.querySelectorAll(".console-settings-actionbar__actions button")) {
    assert.equal(b.getAttribute("aria-describedby"), note.id);
    assert.equal(b.getAttribute("title"), null, "no tooltip-only explanation");
  }
});

test("staging a change shows the bar and one count, and Reset hides them", async () => {
  await mount();
  assert.equal(q<HTMLElement>(".console-settings-actionbar").hidden, true);
  const dark = q<HTMLInputElement>('input[type=radio][value="dark"]');
  dark.checked = true;
  dark.dispatchEvent(new Event("change"));
  assert.equal(q<HTMLElement>(".console-settings-actionbar").hidden, false);
  assert.equal(q(".console-settings-diff__title").textContent, "Pending changes (1)");
  assert.equal(host.textContent?.includes("1 pending change"), false, "the count is said once");

  [...host.querySelectorAll("button")].find((b) => b.textContent === "Reset")?.click();
  assert.equal(q<HTMLElement>(".console-settings-actionbar").hidden, true);
});

test("a keybinding row's controls say which command they act on", async () => {
  await mount();
  const names = [...host.querySelectorAll("[data-krow] button")].map((b) =>
    b.getAttribute("aria-label"),
  );
  assert.equal(names.length, 6);
  assert.equal(new Set(names).size, 6, "no two controls share a name");
  assert.ok(names.includes("Record a shortcut for Next tab"));
  assert.ok(names.includes("Clear the shortcut for Previous tab"));
});

test("the page, tabs and sections give the outline a start: h1, then h2s", async () => {
  await mount();
  assert.equal(q("h1").textContent, "Settings");
  const h2 = [...host.querySelectorAll("h2")].map((e) => e.textContent);
  assert.deepEqual(h2.slice(0, 2), ["Pending changes", "Connection"]);
  assert.equal(
    h2.filter((t) => t === "General").length,
    0,
    "the tab is General; no section repeats it",
  );
  for (const li of host.querySelectorAll(".pf-v6-c-tabs__item")) {
    assert.equal(li.getAttribute("role"), "presentation");
  }
});

test("Install is a secondary button, About is an expandable section with link buttons", async () => {
  await mount();
  const install = [...host.querySelectorAll("button")].find((b) => b.textContent === "Install");
  assert.ok(install?.classList.contains("pf-m-secondary"));
  assert.equal(
    host.querySelectorAll("#console-settings-panel-general .pf-v6-c-button.pf-m-primary").length,
    0,
    "no primary beside the action bar's",
  );
  assert.equal(host.querySelectorAll(".console-settings-actionbar .pf-m-primary").length, 1);

  assert.equal(host.querySelector("details"), null);
  const toggle = q<HTMLButtonElement>(".pf-v6-c-expandable-section__toggle button");
  const region = q<HTMLElement>(".pf-v6-c-expandable-section__content");
  assert.equal(region.hidden, true);
  toggle.click();
  assert.equal(toggle.getAttribute("aria-expanded"), "true");
  assert.equal(region.hidden, false);
  for (const a of host.querySelectorAll(".console-settings-about a")) {
    assert.ok(a.classList.contains("pf-m-link"), "a link button, not an unstyled anchor");
  }
});

test("import runs from a real button, so it takes the console's focus ring", async () => {
  await mount();
  const importBtn = [...host.querySelectorAll("button")].find(
    (b) => b.textContent === "Import from file",
  );
  assert.ok(importBtn);
  assert.equal(importBtn.closest("label"), null);
});

const TOKENS = {
  tokens: [
    {
      name: "ci",
      id: "ab12cd34",
      class: "CREDENTIAL_CLASS_STORED",
      grant: { mcp: "LEVEL_READ" },
      expireTime: "2031-01-01T00:00:00Z",
    },
  ],
};

function serveTokens(revoked: string[]): void {
  setDefaultHost("127.0.0.1:7391");
  globalThis.fetch = ((input: RequestInfo | URL, init?: RequestInit) => {
    const url = String(input instanceof Request ? input.url : input);
    const json = (b: unknown): Promise<Response> =>
      Promise.resolve(
        new Response(JSON.stringify(b), {
          status: 200,
          headers: { "content-type": "application/json" },
        }),
      );
    if (url.includes("TokenService/ListTokens"))
      return json(revoked.length ? { tokens: [] } : TOKENS);
    if (url.includes("TokenService/RevokeToken")) {
      revoked.push(String(init?.body ?? ""));
      return json({});
    }
    return Promise.reject(new Error("stub: no network"));
  }) as typeof fetch;
}

test("tokens are a PF table with a Revoke row action, in a container PF's grid layout keys on", async () => {
  serveTokens([]);
  await mount();
  const table = q("table.pf-v6-c-table.pf-m-compact.pf-m-grid-md");
  assert.equal(table.getAttribute("aria-label"), "Access tokens");
  assert.equal(table.closest(".console-settings-tokens__table")?.nodeName, "DIV");
  assert.deepEqual(
    [...table.querySelectorAll("tbody td")].map((td) => (td as HTMLElement).dataset.label),
    ["Class", "Grant", "Name", "ID", "Expires", "Actions"],
  );
  assert.ok(table.querySelector("td.pf-m-action button"));
});

test("Revoke asks in a danger modal: Cancel keeps the token, Revoke removes it", async () => {
  const revoked: string[] = [];
  serveTokens(revoked);
  await mount();
  const revoke = q<HTMLButtonElement>("td.pf-m-action button");
  revoke.focus();
  revoke.click();

  const dialog = document.querySelector<HTMLElement>(".pf-v6-c-modal-box.pf-m-danger[role=dialog]");
  assert.ok(dialog, "the modal is on the page");
  assert.equal(dialog.getAttribute("aria-modal"), "true");
  assert.ok(byId(dialog.getAttribute("aria-labelledby") ?? ""));
  assert.equal(document.activeElement?.textContent, "Cancel", "focus starts on the safe choice");

  [...dialog.querySelectorAll("button")].find((b) => b.textContent === "Cancel")?.click();
  await settle();
  assert.equal(document.querySelector(".pf-v6-c-modal-box"), null);
  assert.ok(document.activeElement === revoke, "focus returns to the control that opened it");
  assert.equal(revoked.length, 0);

  revoke.click();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
  await settle();
  assert.equal(revoked.length, 0, "Escape cancels");

  revoke.click();
  [...document.querySelectorAll(".pf-v6-c-modal-box button")]
    .find((b) => b.textContent === "Revoke")
    ?.dispatchEvent(new MouseEvent("click", { bubbles: true }));
  await settle(30);
  assert.equal(revoked.length, 1);
  assert.equal(host.querySelector("table.pf-v6-c-table"), null, "the list reloads without it");
});

test("with no server the tokens section shows the shared connect prompt, under the section's h2", async () => {
  await mount();
  const empty = q(".console-settings-tokens .pf-v6-c-empty-state");
  assert.equal(empty.querySelector("h3")?.textContent, "No server connected");
  assert.ok(
    [...empty.querySelectorAll("button")].some((b) => b.textContent === "Set server address"),
  );
});
