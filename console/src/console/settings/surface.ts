// surface.ts - the Settings surface: a console tab gathering every browser-side console setting under a
// TRANSACTIONAL, staged-config model. Controls edit an in-memory DRAFT seeded from the
// committed (live) values; the page shows the pending diff and three actions - Save & Apply (persist +
// hot-reload now), Save (persist for the next load), Reset (discard the draft). Nothing reaches the
// durable cells until Save or Save & Apply.
//
// It lives in the SHELL bundle (not a lazy surface bundle) because the Keybindings section embeds
// createKeybindingsEditor over a DRAFT-backed keymap cell, so rebinds stage like every other setting and
// only hit the real shared keymap cell on Save or Save & Apply.

import type { PageController, PageModule, SearchProvider } from "../page";
import { createKeybindingsEditor, type KeybindingsDeps } from "../keybindings";
import { formatChord, isMac, mergeKeymap, type Keymap } from "../commands";
import {
  getPollMs, setPollMs, savePollMs, getDefaultHost, setDefaultHost, saveDefaultHost,
} from "../../lib/settings";
import { showRefreshToast, showToast } from "../../lib/refresh-toast";
import { h } from "../view";
import {
  buildSettingsEnvelope, computePendingChanges, createDraftCell, diffLines, importSettings,
  type DiffContext, type PendingChange, type Settings, type ThemePref,
} from "./model";

// What the shell injects: the editable command list, their defaults (CONSOLE_KEYMAP), and the one shared
// live keymap cell the console reads - so a commit writes the same bindings the console honors.
export interface SettingsDeps {
  keybindings: KeybindingsDeps;
}

// A config surface has nothing to find in the shared search box, so it opts out.
const noSearch: SearchProvider<null> = { placeholder: "", parse: () => null, apply: () => ({ matches: 0 }) };

// The poll intervals the select offers, and their display labels (also used by the pending diff).
const POLL_OPTIONS: [string, string][] = [["5000", "5s"], ["10000", "10s"], ["20000", "20s"], ["60000", "60s"]];
const pollLabel = (ms: number): string => POLL_OPTIONS.find(([v]) => v === String(ms))?.[1] ?? Math.round(ms / 1000) + "s";

// The three theme choices, ordered for the toggle group. "auto" reads as "System" everywhere user-facing.
const THEME_ORDER: ThemePref[] = ["auto", "light", "dark"];
const THEME_LABEL: Record<ThemePref, string> = { auto: "System", light: "Light", dark: "Dark" };

// theme.ts persists the color theme under the un-namespaced localStorage "theme" key (absent = auto). It
// is a pre-paint script with no exports; reading feeds the committed baseline. Writes go over the
// magus:theme-set bridge on commit.
function getThemePref(): ThemePref {
  try {
    const v = localStorage.getItem("theme");
    return v === "light" || v === "dark" ? v : "auto";
  } catch {
    return "auto";
  }
}

// buildFormGroup wraps a control in a PF horizontal FormGroup. The label is a real <label for> when the
// control has an id, else a plain span.
function buildFormGroup(labelText: string, controlId: string | null, control: HTMLElement, help?: string): HTMLElement {
  const group = h("div", "pf-v6-c-form__group");
  const labelWrap = h("div", "pf-v6-c-form__group-label");
  if (controlId) {
    const label = h("label", "pf-v6-c-form__label");
    label.htmlFor = controlId;
    label.append(h("span", "pf-v6-c-form__label-text", labelText));
    labelWrap.append(label);
  } else {
    const label = h("span", "pf-v6-c-form__label");
    label.append(h("span", "pf-v6-c-form__label-text", labelText));
    labelWrap.append(label);
  }
  const controlWrap = h("div", "pf-v6-c-form__group-control");
  controlWrap.append(control);
  if (help) controlWrap.append(h("p", "console-settings-form__help", help));
  group.append(labelWrap, controlWrap);
  return group;
}

// buildSection builds a titled block: an h2 title, an optional lede, and the given body.
function buildSection(title: string, body: HTMLElement, lede?: string): HTMLElement {
  const section = h("section", "console-settings-section");
  const head = h("div", "console-settings-section__head");
  head.append(h("h2", "console-settings-section__title", title));
  if (lede) head.append(h("p", "console-settings-section__lede", lede));
  section.append(head, body);
  return section;
}

// buildSettings assembles the surface into host and returns a teardown. It stages every edit into a draft
// and commits (or discards) it as a transaction.
function buildSettings(host: HTMLElement, deps: SettingsDeps): () => void {
  const mac = isMac();
  const kb = deps.keybindings;

  // Committed baseline: what the running session currently has. Re-read after each commit.
  const readCommitted = (): Settings => ({
    poll: getPollMs(), host: getDefaultHost(), theme: getThemePref(), keymap: kb.keymap.get(),
  });
  let committed = readCommitted();

  // The draft: scalar fields held here, keymap held in a draft-backed cell so the embedded editor drives
  // it live within the surface without touching the real shared cell. onChange recomputes the pending diff.
  const draftScalar = { poll: committed.poll, host: committed.host, theme: committed.theme };
  const keymapDraft = createDraftCell<Keymap>({ ...committed.keymap }, () => recompute());
  const draftPrefs = (): Settings => ({
    poll: draftScalar.poll, host: draftScalar.host.trim(), theme: draftScalar.theme, keymap: keymapDraft.get(),
  });

  // The diff formatters: human labels for scalars, effective (merged) display chords for keybindings.
  const ctx: DiffContext = {
    pollLabel,
    themeLabel: (t) => THEME_LABEL[t],
    hostLabel: (host) => (host === "" ? "loopback" : host),
    commandLabel: (id) => kb.commands.find((c) => c.id === id)?.label ?? id,
    effectiveChord: (keymap, id) => formatChord(mergeKeymap(kb.defaults, keymap)[id] ?? "", mac) || "None",
    commandIds: kb.commands.map((c) => c.id),
  };

  const page = h("div", "console-settings-page");
  page.dataset.surface = "settings";
  page.append(h("h1", "console-settings-title", "Settings"));

  // --- Action bar: a staged-config bar - pending indicator + Save & Apply / Save / Reset ---
  const bar = h("div", "console-settings-actionbar");
  const count = h("span", "console-settings-actionbar__count");
  const actions = h("div", "console-settings-actionbar__actions");
  // Standard PatternFly button hierarchy, no custom accent colors: Save & Apply = primary (persist +
  // hot-reload now), Save = secondary (persist for the next load), Reset = a quiet link (discard the draft).
  const applyBtn = h("button", "pf-v6-c-button pf-m-primary", "Save & Apply") as HTMLButtonElement;
  const saveBtn = h("button", "pf-v6-c-button pf-m-secondary", "Save") as HTMLButtonElement;
  const resetBtn = h("button", "pf-v6-c-button pf-m-link", "Reset") as HTMLButtonElement;
  for (const b of [applyBtn, saveBtn, resetBtn]) b.type = "button";
  applyBtn.title = "Persist and apply changes to this session now";
  saveBtn.title = "Persist changes for the next load, without applying them now";
  resetBtn.title = "Discard staged changes and restore the saved values";
  actions.append(applyBtn, saveBtn, resetBtn);
  bar.append(count, actions);

  const status = h("p", "console-settings-actionbar__status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const setStatus = (msg: string, kind: "ok" | "error"): void => { status.textContent = msg; status.dataset.kind = kind; };

  // The pending diff, hidden when the draft matches the baseline. A header carries the title and a
  // Pretty|Raw view toggle (a PF ToggleGroup, matching the log viewer's Pretty|Raw switch): Pretty is
  // the readable field list, Raw is the settings envelope as a git-style line diff (removed red, added
  // green).
  let diffView: "pretty" | "raw" = "pretty";
  const diffWrap = h("section", "console-settings-diff");
  const diffHead = h("div", "console-settings-diff__head");
  diffHead.append(h("h2", "console-settings-diff__title", "Pending changes"));

  const viewToggle = h("div", "pf-v6-c-toggle-group console-settings-diff__view");
  viewToggle.setAttribute("role", "group");
  viewToggle.setAttribute("aria-label", "Pending changes view");
  const viewButtons: [("pretty" | "raw"), HTMLButtonElement][] = [];
  for (const [mode, labelText] of [["pretty", "Pretty"], ["raw", "Raw"]] as const) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button") as HTMLButtonElement;
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-toggle-group__text", labelText));
    btn.addEventListener("click", () => { diffView = mode; paintDiffView(); });
    item.append(btn);
    viewToggle.append(item);
    viewButtons.push([mode, btn]);
  }
  diffHead.append(viewToggle);
  diffWrap.append(diffHead);

  const diffList = h("ul", "console-settings-diff__list");
  const rawPre = h("pre", "console-settings-diff__raw");
  diffWrap.append(diffList, rawPre);

  // paintDiffView reflects the selected mode on the toggle and shows the matching body.
  function paintDiffView(): void {
    for (const [mode, btn] of viewButtons) {
      const on = diffView === mode;
      btn.classList.toggle("pf-m-selected", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
    }
    diffList.hidden = diffView !== "pretty";
    rawPre.hidden = diffView !== "raw";
  }

  // renderRaw rebuilds the raw view: a line diff of the committed vs draft settings envelope, one span
  // per line tagged with its diff kind (styled red/green/muted in settings.css).
  function renderRaw(): void {
    const before = JSON.stringify(buildSettingsEnvelope(committed), null, 2);
    const after = JSON.stringify(buildSettingsEnvelope(draftPrefs()), null, 2);
    rawPre.replaceChildren();
    for (const line of diffLines(before, after)) {
      const sign = line.kind === "del" ? "-" : line.kind === "add" ? "+" : " ";
      const row = h("span", "console-settings-diff__rawline", sign + " " + line.text);
      row.dataset.diff = line.kind;
      rawPre.append(row);
    }
  }

  function renderPending(changes: PendingChange[]): void {
    count.textContent = changes.length === 0
      ? "No pending changes"
      : changes.length + (changes.length === 1 ? " pending change" : " pending changes");
    diffList.replaceChildren();
    for (const c of changes) {
      const item = h("li", "console-settings-diff__item");
      item.append(h("span", "console-settings-diff__label", c.label));
      const change = h("span", "console-settings-diff__change");
      change.append(
        h("span", "console-settings-diff__before", c.before),
        h("span", "console-settings-diff__arrow", "->"),
        h("span", "console-settings-diff__after", c.after),
      );
      item.append(change);
      diffList.append(item);
    }
    renderRaw();
    paintDiffView();
    diffWrap.hidden = changes.length === 0;
  }

  function recompute(): void {
    const changes = computePendingChanges(committed, draftPrefs(), ctx);
    renderPending(changes);
    const none = changes.length === 0;
    saveBtn.disabled = none;
    applyBtn.disabled = none;
    resetBtn.disabled = none;
  }

  // --- General: refresh rate + daemon host ---
  const generalForm = h("form", "pf-v6-c-form pf-m-horizontal");
  generalForm.addEventListener("submit", (e) => e.preventDefault());

  const pollControl = h("span", "pf-v6-c-form-control");
  const pollSelect = h("select");
  pollSelect.id = "console-settings-poll";
  for (const [ms, lab] of POLL_OPTIONS) {
    const opt = h("option");
    opt.value = ms;
    opt.textContent = lab;
    pollSelect.append(opt);
  }
  pollSelect.value = String(draftScalar.poll);
  pollControl.append(pollSelect);
  pollSelect.addEventListener("change", () => { draftScalar.poll = Number(pollSelect.value); recompute(); });

  const hostControl = h("span", "pf-v6-c-form-control");
  const hostInput = h("input");
  hostInput.id = "console-settings-host";
  hostInput.type = "text";
  hostInput.placeholder = "127.0.0.1:7391";
  hostInput.spellcheck = false;
  hostInput.autocomplete = "off";
  hostInput.value = draftScalar.host;
  hostControl.append(hostInput);
  hostInput.addEventListener("input", () => { draftScalar.host = hostInput.value; recompute(); });

  generalForm.append(
    buildFormGroup("Refresh rate", pollSelect.id, pollControl, "How often the VCS insight lenses re-poll the daemon."),
    buildFormGroup("Daemon host", hostInput.id, hostControl, "Default host:port when no live link is present. Blank uses loopback."),
  );

  // --- Appearance: a 3-way theme toggle group (staged; applies on Save & Apply) ---
  const themeGroup = h("div", "pf-v6-c-toggle-group console-settings-theme");
  themeGroup.setAttribute("role", "group");
  themeGroup.setAttribute("aria-label", "Color theme");
  const themeButtons = new Map<ThemePref, HTMLButtonElement>();
  for (const t of THEME_ORDER) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button") as HTMLButtonElement;
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-toggle-group__text", THEME_LABEL[t]));
    btn.addEventListener("click", () => { draftScalar.theme = t; paintThemeToggle(); recompute(); });
    item.append(btn);
    themeGroup.append(item);
    themeButtons.set(t, btn);
  }
  function paintThemeToggle(): void {
    for (const [t, btn] of themeButtons) {
      const on = draftScalar.theme === t;
      btn.classList.toggle("pf-m-selected", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
    }
  }
  paintThemeToggle();
  const themeBody = h("div", "console-settings-section__body");
  themeBody.append(buildFormGroup("Theme", null, themeGroup, "System follows your operating system. Applies on Save & Apply."));

  // --- Keybindings: the shared editor core over the DRAFT keymap ---
  const editor = createKeybindingsEditor({ commands: kb.commands, defaults: kb.defaults, keymap: keymapDraft });

  // --- Backup: export / import (staged into the draft) ---
  const io = h("div", "console-settings-io");
  const ioActions = h("div", "console-settings-io__actions");
  const copyBtn = h("button", "pf-v6-c-button pf-m-secondary", "Copy to clipboard");
  copyBtn.type = "button";
  const downloadBtn = h("button", "pf-v6-c-button pf-m-secondary", "Download");
  downloadBtn.type = "button";
  ioActions.append(copyBtn, downloadBtn);

  const importActions = h("div", "console-settings-io__actions");
  const fileLabel = h("label", "pf-v6-c-button pf-m-secondary console-settings-io__file");
  fileLabel.append(h("span", "pf-v6-c-button__text", "Import from file"));
  const fileInput = h("input") as HTMLInputElement;
  fileInput.type = "file";
  fileInput.accept = "application/json,.json";
  fileLabel.append(fileInput);
  importActions.append(fileLabel);

  const exportJson = (): string => JSON.stringify(buildSettingsEnvelope(draftPrefs()), null, 2);

  copyBtn.addEventListener("click", () => {
    const text = exportJson();
    const clip = navigator.clipboard;
    if (clip && typeof clip.writeText === "function") {
      clip.writeText(text).then(
        () => setStatus("Copied settings to the clipboard.", "ok"),
        () => setStatus("Could not access the clipboard. Use Download instead.", "error"),
      );
    } else {
      setStatus("Clipboard is unavailable here. Use Download instead.", "error");
    }
  });

  downloadBtn.addEventListener("click", () => {
    const blob = new Blob([exportJson()], { type: "application/json" });
    const url = URL.createObjectURL(blob);
    const a = h("a");
    a.href = url;
    a.download = "magus-console-settings.json";
    a.click();
    URL.revokeObjectURL(url);
    setStatus("Downloaded magus-console-settings.json.", "ok");
  });

  // Import stages onto the draft (merged over the current draft), so the operator reviews the pending diff
  // and then Saves or Applies - import never commits on its own.
  const applyImport = (text: string): void => {
    const res = importSettings(text, draftPrefs());
    if (!res.ok) { setStatus(res.error, "error"); return; }
    loadDraft(res.next);
    setStatus("Staged import: " + res.applied.join(", ") + ". Review, then Save or Save & Apply.", "ok");
  };

  fileInput.addEventListener("change", () => {
    const file = fileInput.files?.[0];
    if (!file) return;
    file.text().then(
      (text) => applyImport(text),
      () => setStatus("Could not read that file.", "error"),
    );
    fileInput.value = ""; // allow re-picking the same file
  });

  io.append(
    h("p", "console-settings-io__lede", "Settings live in this browser. Copy or download the current draft to move it to another machine, or import a saved file to stage it."),
    ioActions,
    importActions,
  );

  // loadDraft replaces the whole draft (scalars + keymap) and reseeds every control. Backs Reset
  // (loads the committed baseline) and Import (loads the merged snapshot).
  function loadDraft(p: Settings): void {
    draftScalar.poll = p.poll;
    draftScalar.host = p.host;
    draftScalar.theme = p.theme;
    pollSelect.value = String(p.poll);
    hostInput.value = p.host;
    paintThemeToggle();
    keymapDraft.set({ ...p.keymap }); // re-renders the editor via its subscription; onChange recomputes
    recompute();
  }

  // commitDraft persists the draft. applyLive true = Save & Apply (persist + hot-reload now), false = Save
  // (persist only; takes effect on the next load). After either, the committed baseline becomes the draft
  // so the pending diff clears.
  function commitDraft(applyLive: boolean): void {
    const d = draftPrefs();
    const keys = new Set(computePendingChanges(committed, d, ctx).map((c) => (c.key.startsWith("keymap:") ? "keymap" : c.key)));
    if (keys.size === 0) return;
    const setTheme = (persistOnly: boolean): void => {
      document.dispatchEvent(new CustomEvent("magus:theme-set", { detail: { theme: d.theme, persistOnly } }));
    };
    if (applyLive) {
      if (keys.has("poll")) setPollMs(d.poll);
      if (keys.has("host")) setDefaultHost(d.host);
      if (keys.has("theme")) setTheme(false);
      if (keys.has("keymap")) kb.keymap.set(d.keymap);
    } else {
      if (keys.has("poll")) savePollMs(d.poll);
      if (keys.has("host")) saveDefaultHost(d.host);
      if (keys.has("theme")) setTheme(true);
      if (keys.has("keymap")) kb.keymap.persistOnly(d.keymap);
    }
    committed = { ...d, keymap: { ...d.keymap } };
    recompute();
    const msg = applyLive ? "Applied changes to this session." : "Saved. Takes effect on the next load.";
    setStatus(msg, "ok");
    // Confirm every commit with a toast: the reload prompt when a live change needs a reload to take
    // effect (poll/host), otherwise a transient success toast so a save is never silent.
    if (applyLive && (keys.has("poll") || keys.has("host"))) {
      showRefreshToast("Console settings changed. Reload to apply.");
    } else {
      showToast(msg);
    }
  }

  saveBtn.addEventListener("click", () => commitDraft(false));
  applyBtn.addEventListener("click", () => commitDraft(true));
  resetBtn.addEventListener("click", () => { loadDraft(committed); setStatus("Reset pending changes.", "ok"); });

  page.append(
    bar,
    status,
    diffWrap,
    buildSection("General", generalForm),
    buildSection("Appearance", themeBody),
    buildSection("Keybindings", editor.el, "Rebind the console's tab, pane, and command-bar shortcuts. Changes stage here and land on Save or Save & Apply."),
    buildSection("Backup", io, "Export the current draft, or import a saved set to stage it."),
  );
  host.append(page);

  recompute();
  return () => { editor.destroy(); };
}

// ensureStylesheet adds the surface's page-scoped stylesheet once (idempotent by id).
function ensureStylesheet(id: string, href: string): void {
  if (document.getElementById(id)) return;
  const link = document.createElement("link");
  link.id = id;
  link.rel = "stylesheet";
  link.href = href;
  document.head.append(link);
}

// settingsSurface builds the Settings PageModule; the shell registers it and drives it through the
// single-instance open() path.
export function settingsSurface(deps: SettingsDeps): PageModule<null, null> {
  const cssId = "surface-css-settings";
  // A variable (not a string literal) so esbuild leaves it a runtime load: gen/settings/settings.css.
  const cssFile = "settings/settings.css";
  return {
    id: "settings",
    title: "Settings",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      ensureStylesheet(cssId, new URL("./" + cssFile, import.meta.url).href);
      const teardown = buildSettings(host, deps);
      return {
        search: noSearch,
        deactivate(): void { teardown(); host.replaceChildren(); },
      };
    },
  };
}
