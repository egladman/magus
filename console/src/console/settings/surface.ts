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
  getFocusRing, setFocusRing, saveFocusRing, applyFocusRing,
} from "../../lib/settings";
import { showRefreshToast, showToast } from "../../lib/refresh-toast";
import { probeDaemon, normalizeDaemonHost, resolveDaemonHost } from "../../lib/daemon";
import { h } from "../view";
import { LICENSE_TEXT } from "./license";
import { buildTokensSection } from "./tokens";
import { buildMemorySection } from "./memory";
import {
  buildSettingsEnvelope, computePendingChanges, createDraftCell, diffLines, importSettings,
  type DiffContext, type PendingChange, type Settings, type ThemePref,
} from "./model";

// The project's canonical repository, derived from the Go module path (github.com/egladman/magus in
// ../go.mod). Every About link hangs off it, so it lives in one place.
const REPO_URL = "https://github.com/egladman/magus";

// What the shell injects: the editable command list, their defaults (CONSOLE_KEYMAP), and the one shared
// live keymap cell the console reads - so a commit writes the same bindings the console honors. presets
// (optional) are the "start from a preset" seeds: applying one stages that preset's full binding set
// into the draft, which the operator then edits and Saves like any other change.
export interface SettingsDeps {
  keybindings: KeybindingsDeps;
  presets?: Record<string, Keymap>;
  presetList?: { id: string; label: string }[];
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

// buildPanel wraps a section body as a tab panel. The tab label already names the section, so there is
// no in-panel heading - the lede, when present, is the only intro copy above the body.
function buildPanel(body: HTMLElement, lede?: string): HTMLElement {
  const panel = h("div", "console-settings-panel");
  if (lede) panel.append(h("p", "console-settings-section__lede", lede));
  panel.append(body);
  return panel;
}

// A settings tab: its stable id, the label the strip shows, and the panel it reveals.
interface SettingsTab {
  id: string;
  label: string;
  panel: HTMLElement;
}

// buildSettingsTabs renders a horizontal tab strip (role=tablist) over the section panels, showing
// exactly one panel at a time so the surface is a set of focused views rather than one long scroll.
// Returns the nav strip, the panels host, and setHidden - the daemon-gated Access tokens / Agent memory
// tabs call setHidden(id, true) when the daemon declines the service, dropping both the tab and its
// panel; hiding the active tab falls back to the first still-visible one.
function buildSettingsTabs(tabs: SettingsTab[]): {
  root: HTMLElement; setHidden: (id: string, hidden: boolean) => void;
} {
  const root = h("div", "console-settings-tabs__wrap");
  const nav = h("div", "console-settings-tabs");
  nav.setAttribute("role", "tablist");
  nav.setAttribute("aria-label", "Settings sections");
  const panelsHost = h("div", "console-settings-tabs__panels");
  const buttons = new Map<string, HTMLButtonElement>();
  const panelById = new Map<string, HTMLElement>();
  let activeId = tabs[0].id;

  const visibleIds = (): string[] => tabs.map((t) => t.id).filter((id) => !buttons.get(id)!.hidden);

  function show(id: string): void {
    activeId = id;
    for (const t of tabs) {
      const on = t.id === id;
      const btn = buttons.get(t.id)!;
      btn.classList.toggle("pf-m-current", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
      btn.tabIndex = on ? 0 : -1;
      panelById.get(t.id)!.hidden = !on;
    }
  }

  // Roving keyboard on the tablist (WAI-ARIA): arrows move (and activate, since the panels are cheap to
  // swap) between visible tabs, Home/End jump to the ends. Mirrors the top tab bar's roving pattern.
  function onKey(ev: KeyboardEvent, id: string): void {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight" && ev.key !== "Home" && ev.key !== "End") return;
    ev.preventDefault();
    const ids = visibleIds();
    const here = ids.indexOf(id);
    if (here < 0) return;
    let next = here;
    if (ev.key === "ArrowLeft") next = (here - 1 + ids.length) % ids.length;
    else if (ev.key === "ArrowRight") next = (here + 1) % ids.length;
    else if (ev.key === "Home") next = 0;
    else if (ev.key === "End") next = ids.length - 1;
    const nid = ids[next];
    show(nid);
    buttons.get(nid)!.focus();
  }

  for (const t of tabs) {
    const btn = h("button", "console-settings-tabs__tab") as HTMLButtonElement;
    btn.type = "button";
    btn.id = "console-settings-tab-" + t.id;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-controls", "console-settings-panel-" + t.id);
    btn.append(h("span", "console-settings-tabs__label", t.label));
    btn.addEventListener("click", () => show(t.id));
    btn.addEventListener("keydown", (ev) => onKey(ev, t.id));
    buttons.set(t.id, btn);
    nav.append(btn);

    const panel = t.panel;
    panel.classList.add("console-settings-tabs__panel");
    panel.id = "console-settings-panel-" + t.id;
    panel.setAttribute("role", "tabpanel");
    panel.setAttribute("aria-labelledby", btn.id);
    panel.tabIndex = 0;
    panelById.set(t.id, panel);
    panelsHost.append(panel);
  }

  function setHidden(id: string, hidden: boolean): void {
    const btn = buttons.get(id);
    if (!btn) return;
    btn.hidden = hidden;
    if (hidden && activeId === id) {
      const first = visibleIds()[0];
      if (first) show(first);
    }
  }

  root.append(nav, panelsHost);
  show(activeId);
  return { root, setHidden };
}

// externalLink builds an anchor that opens off-app. The console is an installed PWA, so every outbound
// link goes to a new tab (target=_blank) with rel=noopener to sever the opener reference.
function externalLink(href: string, text: string): HTMLAnchorElement {
  const a = h("a", "console-settings-about__link", text);
  a.href = href;
  a.target = "_blank";
  a.rel = "noopener";
  return a;
}

// buildAbout builds the About section body: a source link, the reporting links, and the full license
// folded into a native <details> disclosure (the codebase's existing fold idiom, no JS needed) so it
// does not dominate the page. It is static, so it takes no draft/commit wiring.
function buildAbout(): HTMLElement {
  const body = h("div", "console-settings-about");

  // A short link list: source, then the issue tracker. Kept as a plain description-free row set -
  // version info already lives in the status bar, so this stays quiet. (Discussions/feature-request
  // link dropped: the repo does not enable Discussions; the issue tracker covers both.)
  const links = h("ul", "console-settings-about__links");
  const linkRow = (label: string, link: HTMLAnchorElement): HTMLElement => {
    const li = h("li", "console-settings-about__row");
    li.append(h("span", "console-settings-about__label", label), link);
    return li;
  };
  links.append(
    linkRow("Source code", externalLink(REPO_URL, REPO_URL)),
    linkRow("Report a bug", externalLink(REPO_URL + "/issues", "Open an issue")),
  );
  body.append(links);

  // The full license, verbatim from license.ts, in a collapsed disclosure. Preformatted + monospace so
  // the GPL's own layout is preserved, and scrollable within a bounded height so it never runs the page.
  const details = h("details", "console-settings-about__license");
  details.append(
    h("summary", "console-settings-about__licensesummary", "License (GPL-3.0-or-later)"),
    h("pre", "console-settings-about__licensetext", LICENSE_TEXT),
  );
  body.append(details);
  return body;
}

// buildSettings assembles the surface into host and returns a teardown. It stages every edit into a draft
// and commits (or discards) it as a transaction.
function buildSettings(host: HTMLElement, deps: SettingsDeps): () => void {
  const mac = isMac();
  const kb = deps.keybindings;

  // Committed baseline: what the running session currently has. Re-read after each commit.
  const readCommitted = (): Settings => ({
    poll: getPollMs(), host: getDefaultHost(), theme: getThemePref(), focusRing: getFocusRing(), keymap: kb.keymap.get(),
  });
  let committed = readCommitted();

  // The draft: scalar fields held here, keymap held in a draft-backed cell so the embedded editor drives
  // it live within the surface without touching the real shared cell. onChange recomputes the pending diff.
  const draftScalar = { poll: committed.poll, host: committed.host, theme: committed.theme, focusRing: committed.focusRing };
  const keymapDraft = createDraftCell<Keymap>({ ...committed.keymap }, () => recompute());
  const draftPrefs = (): Settings => ({
    // A bare port in the daemon-host field expands to the literal loopback IP (8787 -> 127.0.0.1:8787),
    // so the committed/stored value is a canonical host resolveDaemonHost accepts. Empty stays empty
    // (loopback default); an unparseable value is kept as-typed so the Test button can report on it.
    poll: draftScalar.poll, host: normalizeDaemonHost(draftScalar.host) ?? draftScalar.host.trim(),
    theme: draftScalar.theme, focusRing: draftScalar.focusRing,
    keymap: keymapDraft.get(),
  });

  // The diff formatters: human labels for scalars, effective (merged) display chords for keybindings.
  const ctx: DiffContext = {
    pollLabel,
    themeLabel: (t) => THEME_LABEL[t],
    hostLabel: (host) => (host === "" ? "loopback" : host),
    focusRingLabel: (on) => (on ? "On" : "Off"),
    commandLabel: (id) => kb.commands.find((c) => c.id === id)?.label ?? id,
    effectiveChord: (keymap, id) => formatChord(mergeKeymap(kb.defaults, keymap)[id] ?? "", mac) || "None",
    commandIds: kb.commands.map((c) => c.id),
  };

  const page = h("div", "console-settings-page");
  page.dataset.surface = "settings";
  // No page heading: the surface's own tab (the top tab bar) already reads "Settings", so an h1 here
  // just repeats it. The section sub-tabs below carry the naming from here down.

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
    // Hide the whole action bar when the draft matches the saved settings - there is nothing to save,
    // apply, or reset, so the bar (and its "No pending changes" line) is just noise. It reappears the
    // moment a control stages a change.
    bar.hidden = none;
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

  // Test attaches to the field so a typed host can be checked BEFORE saving it - the draft value is what
  // gets probed. It reports through a toast rather than the pending-changes bar: this is a one-off action,
  // not a staged edit.
  const testBtn = h("button", "pf-v6-c-button pf-m-secondary console-settings-host__test", "Test") as HTMLButtonElement;
  testBtn.type = "button";
  testBtn.title = "Try to reach a daemon at this address";
  testBtn.addEventListener("click", () => {
    const raw = hostInput.value.trim();
    if (!raw) { showToast("Settings", "Enter a host to test, for example 127.0.0.1:7391.", "error"); return; }
    testBtn.disabled = true;
    void probeDaemon(raw).then((res) => {
      testBtn.disabled = false;
      // "Answered", not "connected" or "200": the response is opaque cross-origin, so the status code
      // and body are unreadable - this proves a server answered at that address, nothing more.
      if (res.ok) showToast("Settings", "Answered: " + res.url + " (status not readable cross-origin).");
      else showToast("Settings", res.reason, "error");
    });
  });

  const hostGroup = h("div", "pf-v6-c-input-group");
  const hostFill = h("div", "pf-v6-c-input-group__item pf-m-fill");
  hostFill.append(hostControl);
  const testItem = h("div", "pf-v6-c-input-group__item");
  testItem.append(testBtn);
  hostGroup.append(hostFill, testItem);

  generalForm.append(buildFormGroup("Refresh rate", pollSelect.id, pollControl, "How often the VCS insight lenses re-poll the daemon."));
  generalForm.append(buildFormGroup("Daemon host", hostInput.id, hostGroup, "The loopback daemon to connect to by default. Enter a bare port (for example 8787) and it expands to 127.0.0.1:8787, or give a full 127.0.0.1:port. Leave empty for the default loopback."));

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
  // The Appearance panel body: the two toggle groups stacked in a column (same layout as a panel).
  const themeBody = h("div", "console-settings-panel");
  themeBody.append(buildFormGroup("Theme", null, themeGroup, "System follows your operating system. Applies on Save & Apply."));

  // A 2-way focus-ring toggle group, mirrored on the theme toggle above. Off (default) shows the
  // split-pane focus outline only during keyboard navigation; On always shows it, including after a
  // mouse click.
  const focusRingGroup = h("div", "pf-v6-c-toggle-group console-settings-focusring");
  focusRingGroup.setAttribute("role", "group");
  focusRingGroup.setAttribute("aria-label", "Focus ring");
  const focusRingButtons = new Map<boolean, HTMLButtonElement>();
  for (const v of [false, true]) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button") as HTMLButtonElement;
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-toggle-group__text", v ? "On" : "Off"));
    btn.addEventListener("click", () => { draftScalar.focusRing = v; paintFocusRingToggle(); recompute(); });
    item.append(btn);
    focusRingGroup.append(item);
    focusRingButtons.set(v, btn);
  }
  function paintFocusRingToggle(): void {
    for (const [v, btn] of focusRingButtons) {
      const on = draftScalar.focusRing === v;
      btn.classList.toggle("pf-m-selected", on);
      btn.setAttribute("aria-pressed", on ? "true" : "false");
    }
  }
  paintFocusRingToggle();
  themeBody.append(buildFormGroup(
    "Focus ring", null, focusRingGroup,
    "Always show the outline on the focused pane. Off shows it only during keyboard navigation.",
  ));

  // --- Keybindings: an optional keymap-PROFILE strip above the shared editor core over the DRAFT keymap.
  // The strip is a truthful readout of the current bindings, not a separate selection. Picking a named
  // preset stages its whole binding set into the draft immediately - there is no separate "Apply", since
  // the page's own Save / Save & Apply is what commits it, so a preset Apply button just duplicated that.
  // "Custom" is the derived fallback the strip lands on whenever the draft matches no preset - including
  // after any manual edit in the editor below - so the strip can never claim a preset the bindings no
  // longer match. ---
  const editor = createKeybindingsEditor({ commands: kb.commands, defaults: kb.defaults, keymap: keymapDraft });
  let keybindingsContent: HTMLElement = editor.el;
  let disposeProfile = (): void => {};
  if (deps.presets && deps.presetList && deps.presetList.length) {
    const presets = deps.presets;
    const presetList = deps.presetList;

    // keymapsEqual compares two override layers for the SAME effective bindings, treating an unbound row
    // and an absent row alike (both drop out of the normalized map). It decides which named preset, if
    // any, the draft currently equals; no match means Custom.
    const normalize = (k: Keymap): Record<string, string> => {
      const out: Record<string, string> = {};
      for (const [id, chord] of Object.entries(k)) if (chord) out[id] = chord;
      return out;
    };
    const keymapsEqual = (a: Keymap, b: Keymap): boolean => {
      const na = normalize(a), nb = normalize(b);
      const ka = Object.keys(na);
      return ka.length === Object.keys(nb).length && ka.every((id) => na[id] === nb[id]);
    };
    // The profile the draft currently IS: the first preset it equals, else "custom". Empty overrides equal
    // the "default" preset, so untouched bindings read as Default (they genuinely are the defaults).
    const activeProfile = (): string => {
      const cur = keymapDraft.get();
      for (const p of presetList) if (keymapsEqual(cur, presets[p.id])) return p.id;
      return "custom";
    };

    // The strip is the presets as one-click loads: clicking a segment REPLACES the draft with that whole
    // binding set (the page's Save / Save & Apply commits it - a separate preset Apply just duplicated
    // that). The segment matching the current draft lights up; when the draft matches no preset, none
    // lights and a muted "Custom" tag names that state. Custom is a READOUT, never a button - so it can
    // never be "picked" and never competes with the Default preset (which just means "the console
    // defaults"); you reach Custom only by editing a row below.
    const presetGroup = h("div", "pf-v6-c-toggle-group console-settings-presets__group");
    presetGroup.setAttribute("role", "group");
    presetGroup.setAttribute("aria-label", "Keymap preset");
    const presetButtons = new Map<string, HTMLButtonElement>();
    const customTag = h("span", "console-settings-presets__custom", "Custom") as HTMLElement;
    customTag.title = "Your bindings match no preset. Pick one to replace them, or keep editing the rows below.";
    const paintProfile = (): void => {
      const active = activeProfile();
      for (const [id, btn] of presetButtons) {
        const on = id === active;
        btn.classList.toggle("pf-m-selected", on);
        btn.setAttribute("aria-pressed", on ? "true" : "false");
      }
      customTag.hidden = active !== "custom";
    };
    for (const p of presetList) {
      const item = h("div", "pf-v6-c-toggle-group__item");
      const btn = h("button", "pf-v6-c-toggle-group__button") as HTMLButtonElement;
      btn.type = "button";
      btn.append(h("span", "pf-v6-c-toggle-group__text", p.label));
      btn.title = "Replace the keymap with the " + p.label + " preset. It stages into the draft; Save or Save & Apply keeps it.";
      btn.addEventListener("click", () => {
        keymapDraft.set({ ...presets[p.id] }); // fires the subscription (repaint) and the cell's onChange (recompute)
        setStatus("Staged the " + p.label + " keymap. Edit any row, or Save / Save & Apply to keep it.", "ok");
      });
      item.append(btn);
      presetGroup.append(item);
      presetButtons.set(p.id, btn);
    }
    // Repaint on every keymap change - a preset click, an editor edit, an import, or a Reset - so the lit
    // segment always reflects the real bindings. recompute() is driven separately by the draft cell's onChange.
    disposeProfile = keymapDraft.subscribe(() => paintProfile());
    paintProfile();

    const strip = h("div", "console-settings-presets__row");
    strip.append(presetGroup, customTag);
    const wrap = h("div");
    wrap.append(
      buildFormGroup("Keymap preset", null, strip, "Pick a preset to replace your bindings, then edit any row below. The strip shows Custom once your bindings differ from every preset. The Emacs, Vim, and VS Code presets use multi-key sequences like Ctrl+X then O."),
      editor.el,
    );
    keybindingsContent = wrap;
  }

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

  // Turn importSettings' terse hard-error into an actionable sentence that points the operator at the
  // expected format (which they can see by exporting from this very page). The inline status keeps the
  // raw error as the aria-live anchor; the toast is the primary, glanceable signal.
  const importFailureToast = (error: string): string => {
    if (error.includes("valid JSON")) return "Import failed: not valid JSON. Export from this page to see the expected format.";
    if (error.includes("settings object")) return "Import failed: not a magus console settings file. Export from this page to see the expected format.";
    return "Import failed: no recognizable settings in that file. Export from this page to see the expected format.";
  };

  // joinIgnored renders a key list for the partial-import warning, truncating a silly-long list so the
  // toast stays readable (show the first few, then "and N more").
  const joinIgnored = (keys: string[]): string => {
    const max = 5;
    if (keys.length <= max) return keys.join(", ");
    return keys.slice(0, max).join(", ") + ", and " + (keys.length - max) + " more";
  };

  // Import stages onto the draft (merged over the current draft), so the operator reviews the pending diff
  // and then Saves or Applies - import never commits on its own.
  const applyImport = (text: string): void => {
    const res = importSettings(text, draftPrefs());
    if (!res.ok) {
      setStatus(res.error, "error"); // aria-live anchor keeps the terse reason
      showToast("Settings", importFailureToast(res.error), "error");
      return;
    }
    loadDraft(res.next);
    setStatus("Staged import: " + res.applied.join(", ") + ". Review, then Save or Save & Apply.", "ok");
    // Partial import: some of the file did not land. One warn toast tells the operator what was dropped so
    // a typo or a stale file does not silently vanish.
    const parts: string[] = [];
    if (res.newerSchema !== undefined) parts.push("File is from a newer console (schemaVersion " + res.newerSchema + "); unknown settings were ignored.");
    if (res.unknown.length > 0) parts.push("Ignored unknown keys: " + joinIgnored(res.unknown) + ".");
    if (res.skipped.length > 0) parts.push("Ignored invalid values for: " + joinIgnored(res.skipped) + ".");
    if (parts.length > 0) showToast("Settings", parts.join(" "), "warn");
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
    draftScalar.focusRing = p.focusRing;
    pollSelect.value = String(p.poll);
    hostInput.value = p.host;
    paintThemeToggle();
    paintFocusRingToggle();
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
      if (keys.has("focusRing")) { setFocusRing(d.focusRing); applyFocusRing(d.focusRing); }
      if (keys.has("keymap")) kb.keymap.set(d.keymap);
    } else {
      if (keys.has("poll")) savePollMs(d.poll);
      if (keys.has("host")) saveDefaultHost(d.host);
      if (keys.has("theme")) setTheme(true);
      if (keys.has("focusRing")) saveFocusRing(d.focusRing);
      if (keys.has("keymap")) kb.keymap.persistOnly(d.keymap);
    }
    committed = { ...d, keymap: { ...d.keymap } };
    recompute();
    const msg = applyLive ? "Applied changes to this session." : "Saved. Takes effect on the next load.";
    // Confirm the commit with a TOAST, not a lingering inline line: the reload prompt when a live change
    // needs a reload to take effect (poll/host), otherwise a transient success toast so a save is never
    // silent. Clear any prior inline status so a stale message does not sit under the heading.
    setStatus("", "ok");
    if (applyLive && (keys.has("poll") || keys.has("host"))) {
      showRefreshToast("Settings", "Console settings changed. Reload to apply.");
    } else {
      showToast("Settings", msg);
    }
  }

  saveBtn.addEventListener("click", () => commitDraft(false));
  applyBtn.addEventListener("click", () => commitDraft(true));
  resetBtn.addEventListener("click", () => { loadDraft(committed); setStatus("Reset pending changes.", "ok"); });

  // The two LIVE sections talk to the daemon directly (not the staged-config model): they act on the
  // daemon's own state - its auth tokens and the durable agent-memory files - so their edits apply
  // immediately over RPC rather than staging into the draft. Both resolve the same loopback host and
  // degrade to a clear "connect first" state when none is found.
  //
  // The two LIVE sections are gated by the SERVER, not a client-side mode guess: they always build,
  // and each hides its own TAB (via tabs.setHidden below) if the daemon declines the service to this
  // client (onDenied) - a read-only phone share cannot reach TokenService/MemoryService (not mounted
  // on the share listener, and guarded by token class), so those RPCs come back denied and the tab
  // vanishes. Enforcement lives at the daemon; this only mirrors what the daemon already refuses.
  // (tabs is const-declared below; onDenied only fires after an async RPC, so it is initialized by then.)
  const tokensSection = buildTokensSection(resolveDaemonHost(), { onDenied: () => tabs.setHidden("tokens", true) });
  const memorySection = buildMemorySection(resolveDaemonHost(), { onDenied: () => tabs.setHidden("memory", true) });

  // The action bar and pending diff stay above the tabs: the staged draft is shared across the staged
  // sections (General, Appearance, Keybindings, Backup), so its commit controls are global to the
  // surface, not per-tab. Each section is a tab panel; the Access tokens / Agent memory tabs drop out
  // when the daemon declines the service.
  const tabs = buildSettingsTabs([
    { id: "general", label: "General", panel: buildPanel(generalForm) },
    { id: "appearance", label: "Appearance", panel: buildPanel(themeBody) },
    { id: "keybindings", label: "Keybindings", panel: buildPanel(keybindingsContent, "Rebind the console's tab, pane, and command-bar shortcuts. Changes stage here and land on Save or Save & Apply.") },
    { id: "tokens", label: "Access tokens", panel: buildPanel(tokensSection.el, "List and revoke the daemon's connector tokens and the active phone-share token. Minting stays a CLI-only operation - the console can never create a token.") },
    { id: "memory", label: "Agent memory", panel: buildPanel(memorySection.el, "View and edit the durable memory files agents write across sessions. Editing is the safety valve against the store growing unbounded.") },
    { id: "backup", label: "Backup", panel: buildPanel(io, "Export the current draft, or import a saved set to stage it.") },
    { id: "about", label: "About", panel: buildPanel(buildAbout(), "Source, license, and where to report bugs.") },
  ]);

  page.append(bar, status, diffWrap, tabs.root);
  host.append(page);

  recompute();
  return () => { disposeProfile(); editor.destroy(); tokensSection?.destroy(); memorySection?.destroy(); };
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
