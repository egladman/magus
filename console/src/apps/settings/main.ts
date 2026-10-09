import { must } from "../../lib/guards";
// The Settings app: a console tab gathering every browser-side console setting under a
// TRANSACTIONAL, staged-config model. Controls edit an in-memory DRAFT seeded from the
// committed (live) values; the page shows the pending diff and three actions - Save & Apply (persist +
// hot-reload now), Save (persist for the next load), Reset (discard the draft). Nothing reaches the
// durable cells until Save or Save & Apply.
//
// It lives in the SHELL bundle (not a lazy app bundle) because the Keybindings section embeds
// createKeybindingsEditor over a DRAFT-backed keymap cell, so rebinds stage like every other setting and
// only hit the real shared keymap cell on Save or Save & Apply.

import type { PageController, PageModule, SearchProvider } from "../../desktop/page";
import { createKeybindingsEditor, type KeybindingsDeps } from "../../desktop/keybindings";
import { formatChord, isMac, mergeKeymap, type Keymap } from "../../desktop/commands";
import {
  getPollMs,
  setPollMs,
  savePollMs,
  getDefaultHost,
  setDefaultHost,
  saveDefaultHost,
  getFocusRing,
  setFocusRing,
  saveFocusRing,
  getMotion,
  setMotion,
  saveMotion,
  type MotionPref,
  getNodeShapes,
  setNodeShapes,
  saveNodeShapes,
} from "../../lib/settings";
import { showRefreshToast, showToast } from "../../lib/refresh-toast";
import { reportFailure } from "../../lib/notifications";
import { probeServer, normalizeServerHost, resolveServerHost } from "../../lib/server";
import { h } from "../../desktop/view";
import { SERVER_GUIDE_URL } from "../../desktop/connectPrompt";
import {
  bigPictureSplitCell,
  collapsedCardsCell,
  logsZoomCell,
  sidebarExpandedCell,
  splitModeCell,
  SPLIT_DEFAULT,
  SPLIT_SCHEMA,
} from "../../desktop/layoutPrefs";
import { LICENSE_TEXT } from "./license";
import { buildTokensSection } from "./tokens";
import { buildInstallSection } from "./install";
import type { InstallStore } from "../../lib/install";
import {
  expandable,
  formGroup,
  helperText,
  horizontalForm,
  radios,
  setStatusLine,
  switchControl,
} from "./controls";
import {
  buildSettingsEnvelope,
  type LayoutSettings,
  computePendingChanges,
  createDraftCell,
  diffLines,
  importSettings,
  type DiffContext,
  type PendingChange,
  type Settings,
  type ThemePref,
} from "./model";

// The project's canonical repository, derived from the Go module path (github.com/egladman/magus in
// ../go.mod). Every About link hangs off it, so it lives in one place.
const REPO_URL = "https://github.com/egladman/magus";

// What the shell injects: the editable command list, their defaults (CONSOLE_KEYMAP), and the one shared
// live keymap cell the console reads - so a commit writes the same bindings the console honors. presets
// (optional) are the "start from a preset" seeds: applying one stages that preset's full binding set
// into the draft, which the operator then edits and Saves like any other change.
// install is the shell's install-prompt store (lib/install.ts). It is a DEP rather than a module
// singleton imported here because the offer it holds is captured at shell boot, long before this app
// mounts - the shell owns it, this app only renders it.
export interface SettingsDeps {
  keybindings: KeybindingsDeps;
  presets?: Record<string, Keymap>;
  presetList?: { id: string; label: string }[];
  install: InstallStore;
}

// A config app has nothing to find in the shared search box, so it opts out.
const noSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};

// The poll intervals the radios offer, and their display labels (also used by the pending diff).
const POLL_OPTIONS: [string, string][] = [
  ["5000", "5s"],
  ["10000", "10s"],
  ["20000", "20s"],
  ["60000", "60s"],
];
const pollLabel = (ms: number): string =>
  POLL_OPTIONS.find(([v]) => v === String(ms))?.[1] ?? Math.round(ms / 1000) + "s";

// The three theme choices, in the order they are offered. "auto" reads as "System" everywhere user-facing.
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
    // not-a-failure: with storage disabled no preference was saved, so "auto" is the truth
    return "auto";
  }
}

// readLayout / writeLayout move the envelope's `layout` section over the shared cells in
// layoutPrefs.ts. They are read live and written straight through rather than staged: this
// app has no form for them, so there is no draft to hold them in and no Save that would
// ever commit them. Using the shared cells (not a second persisted() over the same keys) is
// what keeps the exporter and the owning apps agreeing on defaults.
function readLayout(): LayoutSettings {
  return {
    splitMode: splitModeCell.get(),
    bigPictureSplit: { ...bigPictureSplitCell.get() },
    logsZoom: logsZoomCell.get(),
    collapsedCards: collapsedCardsCell.get(),
    sidebarExpanded: sidebarExpandedCell.get(),
  };
}

function writeLayout(next: LayoutSettings): void {
  splitModeCell.set(next.splitMode);
  bigPictureSplitCell.set({
    v: next.bigPictureSplit.v ?? SPLIT_SCHEMA,
    cols: next.bigPictureSplit.cols ?? SPLIT_DEFAULT,
  });
  logsZoomCell.set(next.logsZoom);
  collapsedCardsCell.set(next.collapsedCards);
  sidebarExpandedCell.set(next.sidebarExpanded);
}

interface SectionOptions {
  lede?: string;
  // Marks a section that only makes sense with a keyboard, which a touch-only device hides.
  keyboard?: boolean;
}

// buildSection wraps one settings section with a heading (and optional lede) so several sections read as
// distinct, titled blocks when several are stacked in a single tab panel.
function buildSection(title: string, body: HTMLElement, opts: SectionOptions = {}): HTMLElement {
  const sec = h("section", "console-settings-section");
  if (opts.keyboard) sec.dataset.input = "keyboard";
  sec.append(h("h2", "console-settings-section__title", title));
  if (opts.lede) sec.append(h("p", "console-settings-section__lede", opts.lede));
  sec.append(body);
  return sec;
}

// buildStackedPanel stacks several titled sections into one tab panel - the settings app keeps a
// small set of tabs (General, Access), so a tab groups its related sections rather than fanning
// out one tab per section.
function buildStackedPanel(...sections: HTMLElement[]): HTMLElement {
  const panel = h("div", "console-settings-panel");
  panel.append(...sections);
  return panel;
}

// A settings tab: its stable id, the label the strip shows, and the panel it reveals.
interface SettingsTab {
  id: string;
  label: string;
  panel: HTMLElement;
}

// buildSettingsTabs renders a horizontal tab strip (role=tablist) over the section panels, showing
// exactly one panel at a time so the app is a set of focused views rather than one long scroll.
// Returns the nav strip, the panels host, and setHidden - the server-gated Access tokens tab calls
// setHidden(id, true) when the server declines the service, dropping both the tab and its panel;
// hiding the active tab falls back to the first still-visible one.
function buildSettingsTabs(tabs: SettingsTab[]): {
  root: HTMLElement;
  setHidden: (id: string, hidden: boolean) => void;
} {
  const root = h("div", "console-settings-tabs__wrap");
  const nav = h("div", "pf-v6-c-tabs");
  nav.dataset.controlSize = "default";
  const list = h("ul", "pf-v6-c-tabs__list");
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", "Settings sections");
  nav.append(list);
  const panelsHost = h("div");
  const buttons = new Map<string, HTMLButtonElement>();
  const items = new Map<string, HTMLElement>();
  const panelById = new Map<string, HTMLElement>();
  let activeId = tabs[0].id;

  const visibleIds = (): string[] =>
    tabs.map((t) => t.id).filter((id) => !must(items.get(id)).hidden);

  function show(id: string): void {
    activeId = id;
    for (const t of tabs) {
      const on = t.id === id;
      const btn = must(buttons.get(t.id));
      must(items.get(t.id)).classList.toggle("pf-m-current", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
      btn.tabIndex = on ? 0 : -1;
      must(panelById.get(t.id)).hidden = !on;
    }
  }

  // Roving keyboard on the tablist (WAI-ARIA): arrows move (and activate, since the panels are cheap to
  // swap) between visible tabs, Home/End jump to the ends. Mirrors the top tab bar's roving pattern.
  function onKey(ev: KeyboardEvent, id: string): void {
    if (ev.key !== "ArrowLeft" && ev.key !== "ArrowRight" && ev.key !== "Home" && ev.key !== "End")
      return;
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
    must(buttons.get(nid)).focus();
  }

  for (const t of tabs) {
    const item = h("li", "pf-v6-c-tabs__item");
    item.setAttribute("role", "presentation");
    const btn = h("button", "pf-v6-c-tabs__link");
    btn.type = "button";
    btn.id = "console-settings-tab-" + t.id;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-controls", "console-settings-panel-" + t.id);
    btn.append(h("span", "pf-v6-c-tabs__item-text", t.label));
    btn.addEventListener("click", () => show(t.id));
    btn.addEventListener("keydown", (ev) => onKey(ev, t.id));
    buttons.set(t.id, btn);
    items.set(t.id, item);
    item.append(btn);
    list.append(item);

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
    const item = items.get(id);
    if (!item) return;
    item.hidden = hidden;
    if (hidden && activeId === id) {
      const first = visibleIds()[0];
      if (first) show(first);
    }
  }

  root.append(nav, panelsHost);
  show(activeId);
  return { root, setHidden };
}

// externalLink builds an anchor that opens off-app, as a PF inline link button. The console is an
// installed PWA, so every outbound link goes to a new tab (target=_blank) with rel=noopener to sever
// the opener reference.
function externalLink(href: string, text: string): HTMLAnchorElement {
  const a = h("a", "pf-v6-c-button pf-m-link pf-m-inline", text);
  a.href = href;
  a.target = "_blank";
  a.rel = "noopener";
  return a;
}

// buildAbout builds the About section body: a source link, the reporting links, and the full license
// folded into a PF expandable section so it does not dominate the page. It is static, so it takes no
// draft/commit wiring.
function buildAbout(): HTMLElement {
  const body = h("div", "console-settings-about");

  // Version info already lives in the status bar, so this stays quiet. The issue tracker covers
  // bugs and feature requests alike: the repository does not enable Discussions.
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

  // The full license, verbatim from license.ts. Preformatted + monospace so the GPL's own layout is
  // preserved, and scrollable within a bounded height so it never runs the page.
  body.append(
    expandable(
      "License (GPL-3.0-or-later)",
      h("pre", "console-settings-about__licensetext", LICENSE_TEXT),
    ),
  );
  return body;
}

// buildSettings assembles the app into host and returns a teardown. It stages every edit into a draft
// and commits (or discards) it as a transaction.
function buildSettings(host: HTMLElement, deps: SettingsDeps): () => void {
  const mac = isMac();
  const kb = deps.keybindings;

  // Committed baseline: what the running session currently has. Re-read after each commit.
  const readCommitted = (): Settings => ({
    poll: getPollMs(),
    host: getDefaultHost(),
    theme: getThemePref(),
    focusRing: getFocusRing(),
    motion: getMotion(),
    nodeShapes: getNodeShapes(),
    keymap: kb.keymap.get(),
  });
  let committed = readCommitted();

  // The draft: scalar fields held here, keymap held in a draft-backed cell so the embedded editor drives
  // it live within the app without touching the real shared cell. onChange recomputes the pending diff.
  const draftScalar = {
    poll: committed.poll,
    host: committed.host,
    theme: committed.theme,
    focusRing: committed.focusRing,
    motion: committed.motion,
    nodeShapes: committed.nodeShapes,
  };
  const keymapDraft = createDraftCell<Keymap>({ ...committed.keymap }, () => recompute());
  const draftPrefs = (): Settings => ({
    // A bare port in the server-address field expands to the literal loopback IP (8787 -> 127.0.0.1:8787),
    // so the committed/stored value is a canonical host resolveServerHost accepts. Empty stays empty
    // (loopback default); an unparsable value is kept as-typed so the Test button can report on it.
    poll: draftScalar.poll,
    host: normalizeServerHost(draftScalar.host) ?? draftScalar.host.trim(),
    theme: draftScalar.theme,
    focusRing: draftScalar.focusRing,
    motion: draftScalar.motion,
    nodeShapes: draftScalar.nodeShapes,
    keymap: keymapDraft.get(),
  });

  // The diff formatters: human labels for scalars, effective (merged) display chords for keybindings.
  const ctx: DiffContext = {
    pollLabel,
    themeLabel: (t) => THEME_LABEL[t],
    hostLabel: (host) => (host === "" ? "loopback" : host),
    focusRingLabel: (on) => (on ? "On" : "Off"),
    motionLabel: (v) => (v === "reduced" ? "Reduced" : "System"),
    nodeShapesLabel: (on) => (on ? "On" : "Off"),
    commandLabel: (id) => kb.commands.find((c) => c.id === id)?.label ?? id,
    effectiveChord: (keymap, id) =>
      formatChord(mergeKeymap(kb.defaults, keymap)[id] ?? "", mac) || "None",
    commandIds: kb.commands.map((c) => c.id),
  };

  const page = h("div", "console-settings-page");
  page.dataset.app = "settings";
  // The app's tab reads "Settings" but the page has no heading of its own, so a hidden one gives the
  // sections beneath it an outline to hang from.
  const body = h("div", "console-settings-page__body");
  body.append(h("h1", "pf-v6-screen-reader", "Settings"));

  // --- Action bar: a staged-config bar - what the buttons do + Save & Apply / Save / Reset ---
  const bar = h("div", "console-settings-actionbar");
  const barNote = h(
    "p",
    "console-settings-actionbar__note",
    "Save & Apply applies the changes to this session now. Save keeps them for the next load. Reset discards them.",
  );
  barNote.id = "console-settings-actionbar-note";
  const actions = h("div", "console-settings-actionbar__actions");
  // Standard PatternFly button hierarchy, no custom accent colors: Save & Apply = primary (persist +
  // hot-reload now), Save = secondary (persist for the next load), Reset = a quiet link (discard the draft).
  const applyBtn = h("button", "pf-v6-c-button pf-m-primary", "Save & Apply");
  const saveBtn = h("button", "pf-v6-c-button pf-m-secondary", "Save");
  const resetBtn = h("button", "pf-v6-c-button pf-m-link", "Reset");
  for (const b of [applyBtn, saveBtn, resetBtn]) {
    b.type = "button";
    b.setAttribute("aria-describedby", barNote.id);
  }
  actions.append(applyBtn, saveBtn, resetBtn);
  bar.append(barNote, actions);

  // A live line for what just happened (a staged preset, an import, a clipboard failure). Hidden when
  // there is nothing to say, so it leaves no empty band.
  const status = h("p", "console-settings-actionbar__status");
  status.setAttribute("role", "status");
  status.setAttribute("aria-live", "polite");
  const setStatus = (msg: string, kind: "ok" | "error"): void => setStatusLine(status, msg, kind);
  setStatus("", "ok");

  // The pending diff, hidden when the draft matches the baseline. A header carries the title and a
  // Pretty|Raw view toggle (a PF ToggleGroup, matching the log viewer's Pretty|Raw switch): Pretty is
  // the readable field list, Raw is the settings envelope as a git-style line diff (removed red, added
  // green).
  let diffView: "pretty" | "raw" = "pretty";
  const diffWrap = h("section", "console-settings-diff");
  diffWrap.setAttribute("aria-label", "Pending changes");
  const diffHead = h("div", "console-settings-diff__head");
  const diffTitle = h("h2", "console-settings-diff__title", "Pending changes");
  diffHead.append(diffTitle);

  const viewToggle = h("div", "pf-v6-c-toggle-group console-settings-diff__view");
  viewToggle.dataset.controlSize = "compact";
  viewToggle.setAttribute("role", "group");
  viewToggle.setAttribute("aria-label", "Pending changes view");
  const viewButtons: ["pretty" | "raw", HTMLButtonElement][] = [];
  for (const [mode, labelText] of [
    ["pretty", "Pretty"],
    ["raw", "Raw"],
  ] as const) {
    const item = h("div", "pf-v6-c-toggle-group__item");
    const btn = h("button", "pf-v6-c-toggle-group__button");
    btn.type = "button";
    btn.append(h("span", "pf-v6-c-toggle-group__text", labelText));
    btn.addEventListener("click", () => {
      diffView = mode;
      paintDiffView();
    });
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
    const before = JSON.stringify(buildSettingsEnvelope(committed, readLayout()), null, 2);
    const after = JSON.stringify(buildSettingsEnvelope(draftPrefs(), readLayout()), null, 2);
    rawPre.replaceChildren();
    for (const line of diffLines(before, after)) {
      const sign = line.kind === "del" ? "-" : line.kind === "add" ? "+" : " ";
      const row = h("span", "console-settings-diff__rawline", sign + " " + line.text);
      row.dataset.diff = line.kind;
      rawPre.append(row);
    }
  }

  function renderPending(changes: PendingChange[]): void {
    diffTitle.textContent =
      changes.length === 0 ? "Pending changes" : "Pending changes (" + changes.length + ")";
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
    // apply, or reset, so the bar is just noise. It reappears the moment a control stages a change.
    bar.hidden = none;
    saveBtn.disabled = none;
    applyBtn.disabled = none;
    resetBtn.disabled = none;
  }

  // --- Connection: refresh rate + server address ---
  const pollRadios = radios(
    "console-settings-poll",
    POLL_OPTIONS.map(([value, label]) => ({ value, label })),
    (value) => {
      draftScalar.poll = Number(value);
      recompute();
    },
  );
  pollRadios.set(String(draftScalar.poll));

  const hostControl = h("span", "pf-v6-c-form-control");
  const hostInput = h("input");
  hostInput.id = "console-settings-host";
  hostInput.type = "text";
  hostInput.placeholder = "Example 127.0.0.1:7391";
  hostInput.spellcheck = false;
  hostInput.autocomplete = "off";
  hostInput.value = draftScalar.host;
  hostControl.append(hostInput);

  // The address field's own help names what to type and where the guide is, as text the input
  // points at; the Test verdict sits under it, where a failure's instructions can be read while
  // fixing the address, rather than in a toast that is gone in six seconds.
  const hostHelp = helperText(
    "The loopback server to connect to by default. A bare port such as 8787 expands to 127.0.0.1:8787. Leave empty for the default loopback.",
  );
  const guide = h("a", "pf-v6-c-button pf-m-link pf-m-inline", "Setup guide");
  guide.href = SERVER_GUIDE_URL;
  guide.target = "_blank";
  guide.rel = "noopener";
  hostHelp.el.querySelector(".pf-v6-c-helper-text__item-text")?.append(" ", guide);
  const hostTest = helperText();
  // Bumped whenever the status is cleared, so a probe that answers after the field was edited, reset
  // or imported does not write a verdict about an address no longer in it.
  let hostTestGeneration = 0;
  const clearHostTestStatus = (): void => {
    hostTestGeneration++;
    testBtn.disabled = false;
    hostTest.set("");
  };

  hostInput.addEventListener("input", () => {
    draftScalar.host = hostInput.value;
    clearHostTestStatus();
    recompute();
  });

  // Test attaches to the field so a typed address can be checked BEFORE saving it - the draft value is
  // what gets probed.
  const testBtn = h("button", "pf-v6-c-button pf-m-secondary", "Test");
  testBtn.type = "button";
  testBtn.addEventListener("click", () => {
    const raw = hostInput.value.trim();
    if (!raw) {
      hostTest.set("Enter an address to test, for example 127.0.0.1:7391.", "error");
      return;
    }
    const generation = hostTestGeneration;
    testBtn.disabled = true;
    hostTest.set("Testing...");
    void probeServer(raw).then((res) => {
      if (generation !== hostTestGeneration) return;
      testBtn.disabled = false;
      // "Answered", not "connected" or "200": the response is opaque cross-origin, so the status code
      // and body are unreadable - this proves a server answered at that address, nothing more.
      if (res.ok) {
        hostTest.set(
          "A server answered at " + res.url + ". Save & Apply to connect the console to it.",
          "success",
        );
      } else {
        hostTest.set(res.reason, "error");
      }
    });
  });

  const hostGroup = h("div", "pf-v6-c-input-group");
  const hostFill = h("div", "pf-v6-c-input-group__item pf-m-fill");
  hostFill.append(hostControl);
  const testItem = h("div", "pf-v6-c-input-group__item");
  testItem.append(testBtn);
  hostGroup.append(hostFill, testItem);
  const hostField = h("div");
  hostField.append(hostGroup);

  const hostRow = formGroup({
    label: "Server address",
    control: hostField,
    controlId: hostInput.id,
    help: hostHelp,
  });
  hostRow.querySelector(".pf-v6-c-form__group-control")?.append(hostTest.el);
  hostInput.setAttribute("aria-describedby", hostHelp.id + " " + hostTest.id);

  const connectionForm = horizontalForm(
    formGroup({
      label: "Refresh rate",
      control: pollRadios.el,
      groupRole: "radiogroup",
      help: helperText("How often the views that poll re-read the server."),
    }),
    hostRow,
  );

  // --- Appearance: theme, motion, focus ring, node shapes (staged; applies on Save & Apply) ---
  const themeRadios = radios(
    "console-settings-theme",
    THEME_ORDER.map((value) => ({ value, label: THEME_LABEL[value] })),
    (value) => {
      draftScalar.theme = value;
      recompute();
    },
  );
  themeRadios.set(draftScalar.theme);

  // Motion: "System" is not "no reduction": it honors prefers-reduced-motion, which is why the other
  // option is labeled Reduced rather than Off - it reduces motion here even when the OS is not asking
  // for it anywhere.
  const motionRadios = radios<MotionPref>(
    "console-settings-motion",
    [
      { value: "auto", label: "System" },
      { value: "reduced", label: "Reduced" },
    ],
    (value) => {
      draftScalar.motion = value;
      recompute();
    },
  );
  motionRadios.set(draftScalar.motion);

  // Off (default) shows the split-pane focus outline only during keyboard navigation; On always shows
  // it, including after a mouse click.
  const focusRingSwitch = switchControl((on) => {
    draftScalar.focusRing = on;
    recompute();
  });
  focusRingSwitch.set(draftScalar.focusRing);

  // Node shapes: a shape per family as well as a colour; off draws every node as a circle.
  const shapesSwitch = switchControl((on) => {
    draftScalar.nodeShapes = on;
    recompute();
  });
  shapesSwitch.set(draftScalar.nodeShapes);

  const appearanceForm = horizontalForm(
    formGroup({
      label: "Theme",
      control: themeRadios.el,
      groupRole: "radiogroup",
      help: helperText("System follows your operating system. Applies on Save & Apply."),
    }),
    formGroup({
      label: "Motion",
      control: motionRadios.el,
      groupRole: "radiogroup",
      help: helperText(
        "Reduced stills animation across the console, including the graph's physics layout. System follows your operating system's reduced-motion setting.",
      ),
    }),
    formGroup({
      label: "Focus ring",
      control: focusRingSwitch.el,
      help: helperText(
        "On always shows the outline on the focused pane. Off shows it only during keyboard navigation.",
      ),
    }),
    formGroup({
      label: "Node shapes",
      control: shapesSwitch.el,
      help: helperText(
        "Gives each graph node a shape for its family as well as a color, so kinds stay tellable apart without relying on hue. Off draws every node as a circle.",
      ),
    }),
  );

  // --- Keybindings: an optional keymap-PROFILE row above the shared editor core over the DRAFT keymap.
  // The row is a truthful readout of the current bindings, not a separate selection. Picking a named
  // preset stages its whole binding set into the draft immediately - there is no separate "Apply", since
  // the page's own Save / Save & Apply is what commits it.
  // "Custom" is the derived fallback the row lands on whenever the draft matches no preset - including
  // after any manual edit in the editor below - so it can never claim a preset the bindings no longer
  // match. ---
  const editor = createKeybindingsEditor({
    commands: kb.commands,
    defaults: kb.defaults,
    keymap: keymapDraft,
  });
  const stopNaming = nameEditorControls(editor.el);
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
      const na = normalize(a),
        nb = normalize(b);
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

    // The presets are one-click loads: picking one REPLACES the draft with that whole binding set (the
    // page's Save / Save & Apply commits it). The option matching the current draft is checked; when the
    // draft matches no preset none is, and a "Custom" label names that state. Custom is a READOUT,
    // never an option - you reach it only by editing a row below.
    const presetRadios = radios(
      "console-settings-presets",
      presetList.map((p) => ({ value: p.id, label: p.label })),
      (id) => {
        keymapDraft.set({ ...presets[id] }); // fires the subscription (repaint) and the cell's onChange (recompute)
        const label = presetList.find((p) => p.id === id)?.label ?? id;
        setStatus(
          "Staged the " + label + " keymap. Edit any row, or Save / Save & Apply to keep it.",
          "ok",
        );
      },
    );
    const customTag = h("span", "pf-v6-c-label pf-m-outline pf-m-compact");
    const customContent = h("span", "pf-v6-c-label__content");
    customContent.append(h("span", "pf-v6-c-label__text", "Custom bindings"));
    customTag.append(customContent);
    presetRadios.el.append(customTag);
    const paintProfile = (): void => {
      const active = activeProfile();
      presetRadios.set(active === "custom" ? null : active);
      customTag.hidden = active !== "custom";
    };
    // Repaint on every keymap change - a preset pick, an editor edit, an import, or a Reset - so the
    // checked option always reflects the real bindings. recompute() is driven separately by the draft
    // cell's onChange.
    disposeProfile = keymapDraft.subscribe(() => paintProfile());
    paintProfile();

    const wrap = h("div");
    wrap.append(
      horizontalForm(
        formGroup({
          label: "Keymap preset",
          control: presetRadios.el,
          groupRole: "radiogroup",
          help: helperText(
            "Picking a preset replaces your bindings and stays pending until you save. The Emacs, Vim and VS Code presets use sequences like Ctrl+X then O.",
          ),
        }),
      ),
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

  // A real button opens the picker, so it takes the console's one focus ring; the file input behind it
  // is never reached by keyboard.
  const importBtn = h("button", "pf-v6-c-button pf-m-secondary", "Import from file");
  importBtn.type = "button";
  const fileInput = h("input");
  fileInput.type = "file";
  fileInput.accept = "application/json,.json";
  fileInput.hidden = true;
  fileInput.tabIndex = -1;
  importBtn.addEventListener("click", () => fileInput.click());
  ioActions.append(copyBtn, downloadBtn, importBtn, fileInput);

  const exportJson = (): string =>
    JSON.stringify(buildSettingsEnvelope(draftPrefs(), readLayout()), null, 2);

  const clipboardFailed = (why: string): void => {
    setStatus(why + " Use Download instead.", "error");
    reportFailure(
      "Settings",
      "Could not copy the settings: " + why + " Use Download instead.",
      "settings:copy",
    );
  };
  copyBtn.addEventListener("click", () => {
    const text = exportJson();
    const clip = navigator.clipboard;
    if (clip && typeof clip.writeText === "function") {
      clip.writeText(text).then(
        () => setStatus("Copied settings to the clipboard.", "ok"),
        () => clipboardFailed("The browser would not let this page use the clipboard."),
      );
    } else {
      clipboardFailed("Clipboard is unavailable here.");
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
    if (error.includes("valid JSON"))
      return "Import failed: not valid JSON. Export from this page to see the expected format.";
    if (error.includes("settings object"))
      return "Import failed: not a magus console settings file. Export from this page to see the expected format.";
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
    const res = importSettings(text, draftPrefs(), readLayout());
    if (!res.ok) {
      setStatus(res.error, "error"); // aria-live anchor keeps the terse reason
      showToast("Settings", importFailureToast(res.error), "error");
      return;
    }
    loadDraft(res.next);
    // The layout section is applied immediately rather than staged. It has no form on this
    // app, so there is nothing to review it against and nothing that would ever commit it -
    // staging it would mean silently discarding it. The apps that own these cells read them
    // at mount, so the change shows up on their next open (or a reload), which is the same
    // nudge an imported theme already gets.
    if (res.appliedLayout.length > 0) writeLayout(res.nextLayout);
    setStatus(
      "Staged import: " +
        [...res.applied, ...res.appliedLayout].join(", ") +
        ". Review, then Save or Save & Apply.",
      "ok",
    );
    // Partial import: some of the file did not land. One warn toast tells the operator what was dropped so
    // a typo or a stale file does not silently vanish.
    const parts: string[] = [];
    if (res.newerSchema !== undefined)
      parts.push(
        "File is from a newer console (schemaVersion " +
          res.newerSchema +
          "); unknown settings were ignored.",
      );
    if (res.unknown.length > 0)
      parts.push("Ignored unknown keys: " + joinIgnored(res.unknown) + ".");
    if (res.skipped.length > 0)
      parts.push("Ignored invalid values for: " + joinIgnored(res.skipped) + ".");
    if (parts.length > 0) showToast("Settings", parts.join(" "), "warn");
  };

  fileInput.addEventListener("change", () => {
    const file = fileInput.files?.[0];
    if (!file) return;
    file.text().then(
      (text) => applyImport(text),
      () => {
        setStatus("Could not read that file.", "error");
        reportFailure("Settings", "Could not read that file.", "settings:import:read");
      },
    );
    fileInput.value = ""; // allow re-picking the same file
  });

  io.append(
    h(
      "p",
      "console-settings-io__lede",
      "Settings live in this browser. Copy or download them, unsaved changes included, to move them to another machine, or import a saved file to stage it.",
    ),
    ioActions,
  );

  // loadDraft replaces the whole draft (scalars + keymap) and reseeds every control. Backs Reset
  // (loads the committed baseline) and Import (loads the merged snapshot).
  function loadDraft(p: Settings): void {
    draftScalar.poll = p.poll;
    draftScalar.host = p.host;
    draftScalar.theme = p.theme;
    draftScalar.focusRing = p.focusRing;
    draftScalar.motion = p.motion;
    draftScalar.nodeShapes = p.nodeShapes;
    pollRadios.set(String(p.poll));
    hostInput.value = p.host;
    clearHostTestStatus();
    themeRadios.set(p.theme);
    motionRadios.set(p.motion);
    focusRingSwitch.set(p.focusRing);
    shapesSwitch.set(p.nodeShapes);
    keymapDraft.set({ ...p.keymap }); // re-renders the editor via its subscription; onChange recomputes
    recompute();
  }

  // commitDraft persists the draft. applyLive true = Save & Apply (persist + hot-reload now), false = Save
  // (persist only; takes effect on the next load). After either, the committed baseline becomes the draft
  // so the pending diff clears.
  function commitDraft(applyLive: boolean): void {
    const d = draftPrefs();
    const keys = new Set(
      computePendingChanges(committed, d, ctx).map((c) =>
        c.key.startsWith("keymap:") ? "keymap" : c.key,
      ),
    );
    if (keys.size === 0) return;
    const setTheme = (persistOnly: boolean): void => {
      document.dispatchEvent(
        new CustomEvent("magus:theme-set", { detail: { theme: d.theme, persistOnly } }),
      );
    };
    if (applyLive) {
      if (keys.has("poll")) setPollMs(d.poll);
      if (keys.has("host")) setDefaultHost(d.host);
      if (keys.has("theme")) setTheme(false);
      if (keys.has("focusRing")) setFocusRing(d.focusRing);
      if (keys.has("motion")) setMotion(d.motion);
      if (keys.has("nodeShapes")) setNodeShapes(d.nodeShapes);
      if (keys.has("keymap")) kb.keymap.set(d.keymap);
    } else {
      if (keys.has("poll")) savePollMs(d.poll);
      if (keys.has("host")) saveDefaultHost(d.host);
      if (keys.has("theme")) setTheme(true);
      if (keys.has("focusRing")) saveFocusRing(d.focusRing);
      if (keys.has("motion")) saveMotion(d.motion);
      if (keys.has("nodeShapes")) saveNodeShapes(d.nodeShapes);
      if (keys.has("keymap")) kb.keymap.persistOnly(d.keymap);
    }
    committed = { ...d, keymap: { ...d.keymap } };
    recompute();
    const msg = applyLive
      ? "Applied changes to this session."
      : "Saved. Takes effect on the next load.";
    // Confirm the commit with a TOAST, not a lingering inline line: the reload prompt when a live change
    // needs a reload to take effect, otherwise a transient success toast so a save is never silent.
    // A host applies live only to views still waiting on a server (subscribeDefaultHost); a view
    // already showing data keeps the server it came from, so the reload prompt stays for it too. Clear
    // any prior inline status so a stale message does not sit under the heading.
    setStatus("", "ok");
    if (applyLive && keys.has("poll")) {
      showRefreshToast("Settings", "Console settings changed. Reload to apply.");
    } else if (applyLive && keys.has("host")) {
      showRefreshToast(
        "Settings",
        "Server address applied. Views already showing data keep their server until you reload.",
      );
    } else {
      showToast("Settings", msg);
    }
  }

  saveBtn.addEventListener("click", () => commitDraft(false));
  applyBtn.addEventListener("click", () => commitDraft(true));
  resetBtn.addEventListener("click", () => {
    loadDraft(committed);
    setStatus("Reset pending changes.", "ok");
  });

  // The tokens section is LIVE: it talks to the server directly (not the staged-config model) because
  // it acts on the server's own auth tokens, so its edits apply immediately over RPC rather than
  // staging into the draft. It resolves the loopback host and degrades to a clear "connect first"
  // state when none is found.
  //
  // It is gated by the SERVER, not a client-side mode guess: it always builds, and hides ITS OWN TAB
  // (via tabs.setHidden below) if the server declines the service to this client (onDenied) - a
  // read-only phone share cannot reach TokenService (not mounted on the share listener, and guarded
  // by token class), so those RPCs come back denied and the tab vanishes. Enforcement lives at the
  // server; this only mirrors what the server already refuses. (tabs is const-declared below;
  // onDenied only fires after an async RPC, so it is initialized by then.)
  // Install is LIVE too, for a different reason: it acts on the browser, not on console state. There is
  // nothing to persist, nothing to diff, and nothing Reset could undo, so it stays out of the staged
  // model and applies the moment it is clicked.
  const installSection = buildInstallSection(deps.install);

  const tokensSection = buildTokensSection(resolveServerHost(), {
    onDenied: () => tabs.setHidden("access", true),
  });

  // Two tabs. General stacks the staged client sections (connection, appearance, keybindings, backup)
  // plus Install and About; Access hosts the one live server-facing section. The action bar and pending
  // diff stay above the tabs: the staged draft is shared across the staged sections, so its commit
  // controls are global to the app, not per-tab.
  const tabs = buildSettingsTabs([
    {
      id: "general",
      label: "General",
      panel: buildStackedPanel(
        buildSection("Connection", connectionForm),
        buildSection("Appearance", appearanceForm),
        buildSection("Install", installSection.el, {
          lede: "Install the console as an app on this device. This acts on your browser, so it applies immediately rather than staging above.",
        }),
        buildSection("Keybindings", keybindingsContent, { keyboard: true }),
        buildSection("Backup", io),
        buildSection("About", buildAbout()),
      ),
    },
    {
      id: "access",
      label: "Access",
      panel: buildStackedPanel(
        buildSection("Access tokens", tokensSection.el, {
          lede: "List and revoke the server's connector tokens and the active read-only share token. Minting stays a CLI-only operation: the console can never create a token.",
        }),
      ),
    },
  ]);

  body.append(bar, status, diffWrap, tabs.root);
  page.append(body);
  host.append(page);

  recompute();
  return () => {
    disposeProfile();
    stopNaming();
    editor.destroy();
    installSection.destroy();
    tokensSection?.destroy();
  };
}

// nameEditorControls gives each keybinding row's controls a name that says which command they act on.
// The editor (desktop/keybindings.ts) repaints its rows whenever the keymap changes, and every row
// carries the same Record, Clear and reset buttons, so a reader that lists the controls hears the same
// three names thirty times over. The labels are stamped on every repaint.
function nameEditorControls(root: HTMLElement): () => void {
  const stamp = (): void => {
    for (const row of root.querySelectorAll<HTMLElement>("[data-krow]")) {
      const label = row.firstElementChild?.textContent ?? row.dataset.command ?? "";
      const buttons = row.querySelectorAll<HTMLButtonElement>("[data-kactions] button");
      const verbs = ["", "Clear the shortcut for ", "Reset to default: "];
      buttons.forEach((btn, i) => {
        if (i === 0) {
          const text = btn.textContent?.trim() ?? "Record";
          btn.setAttribute(
            "aria-label",
            (text === "Cancel" ? "Cancel recording the shortcut for " : "Record a shortcut for ") +
              label,
          );
        } else {
          btn.setAttribute("aria-label", (verbs[i] ?? "") + label);
        }
      });
    }
  };
  stamp();
  if (typeof MutationObserver === "undefined") return () => {};
  const observer = new MutationObserver(stamp);
  observer.observe(root, { childList: true, subtree: true });
  return () => observer.disconnect();
}

// ensureStylesheet adds the app's page-scoped stylesheet once (idempotent by id).
function ensureStylesheet(id: string, href: string): void {
  if (document.getElementById(id)) return;
  const link = document.createElement("link");
  link.id = id;
  link.rel = "stylesheet";
  link.href = href;
  document.head.append(link);
}

// settingsApp builds the Settings PageModule; the shell registers it and drives it through the
// single-instance open() path.
export function settingsApp(deps: SettingsDeps): PageModule<null, null> {
  const cssId = "app-css-settings";
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
        // The console's app contract (page.ts). Settings is a form over persisted cells: no
        // timer, no stream, and no slice of the shared status bar, so there is nothing to go quiet
        // about. The hook is declared anyway so every app answers the same shape and a future
        // background read has a defined place to be suppressed.
        setVisible(): void {},
        deactivate(): void {
          teardown();
          host.replaceChildren();
        },
      };
    },
  };
}
