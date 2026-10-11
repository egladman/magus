// statusbar.ts - one tab's status bar. The shell swaps the active tab's bar into the footer, so each
// bar is a real element whose handles survive a tab switch. The connection control inside it lives in
// connection.ts.

import { createClient } from "@connectrpc/connect";
import { StatusService } from "@wire/status/v1alpha1/status_pb";
import {
  createServerTransport,
  getLiveToken,
  isReadOnly,
  parseHash,
  resolveServerHost,
  wantsDemo,
} from "../lib/server";
import { buildConnection } from "./connection";
import { splitModeCell } from "./layoutPrefs";

export type SplitMode = "row" | "col";

// The words for the two split directions, used by the tray button, the tab menu, the Panes map and the
// command labels, so one thing is never called two names.
export const SPLIT_WORD: Record<SplitMode, string> = { row: "Side by side", col: "Stacked" };

const SVG_NS = "http://www.w3.org/2000/svg";

// svgIcon returns a blank inline SVG shell with the console's shared icon defaults: stroked in
// currentColor so it themes for free, aria-hidden because every caller pairs it with a labelled button.
export function svgIcon(size = 14): SVGElement {
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", String(size));
  svg.setAttribute("height", String(size));
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.7");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  return svg;
}

function shape<K extends keyof SVGElementTagNameMap>(
  tag: K,
  attrs: Record<string, string>,
): SVGElementTagNameMap[K] {
  const el = document.createElementNS(SVG_NS, tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  return el;
}

// closeGlyph is the console's one close mark: two strokes, drawn on the shared icon shell.
export function closeGlyph(size = 14): SVGElement {
  const svg = svgIcon(size);
  svg.append(shape("path", { d: "M6 6l12 12M18 6L6 18" }));
  return svg;
}

function keyboardIcon(): SVGElement {
  const svg = svgIcon();
  svg.setAttribute("stroke-width", "1.6");
  svg.append(
    shape("rect", { x: "2.5", y: "6", width: "19", height: "12", rx: "2" }),
    shape("path", { d: "M6 10h.01M10 10h.01M14 10h.01M18 10h.01M8 14h8" }),
  );
  return svg;
}

// shareGlyph is the conventional three-nodes-and-two-edges share mark. It names the action, not one
// destination: the same link puts Big Picture on a TV, a spare monitor or a teammate's laptop.
function shareGlyph(): SVGElement {
  const svg = svgIcon();
  const node = (cx: string, cy: string): SVGElement => shape("circle", { cx, cy, r: "2.6" });
  const edge = (x1: string, y1: string, x2: string, y2: string): SVGElement =>
    shape("line", { x1, y1, x2, y2 });
  svg.append(
    node("18", "5"),
    node("6", "12"),
    node("18", "19"),
    edge("8.3", "10.7", "15.7", "6.3"),
    edge("8.3", "13.3", "15.7", "17.7"),
  );
  return svg;
}

// activityGlyph is the pulse trace: it names liveness, which is what the drawer answers.
function activityGlyph(): SVGElement {
  const svg = svgIcon();
  svg.append(shape("path", { d: "M2.5 12h4l2.5-6 4 12 2.5-6h4" }));
  return svg;
}

// panesIcon is a framed rect divided by a line whose orientation mirrors the split mode: a vertical
// divider for "row" (side by side), a horizontal one for "col" (stacked).
export function panesIcon(mode: SplitMode): SVGElement {
  const svg = svgIcon();
  const line =
    mode === "row"
      ? shape("line", { x1: "12", y1: "4", x2: "12", y2: "20" })
      : shape("line", { x1: "3", y1: "12", x2: "21", y2: "12" });
  svg.append(shape("rect", { x: "3", y: "4", width: "18", height: "16", rx: "2" }), line);
  return svg;
}

// panesName is the tray button's accessible name. It contains the visible word, so a voice-control
// user who says what they see reaches it.
function panesName(mode: SplitMode): string {
  return "Panes: " + SPLIT_WORD[mode];
}

// setPanesIcon repaints an already-built tray button's glyph and word in place. Idempotent: it only
// swaps the svg when the rendered mode changed, because the popup's outside-click test reads the
// tapped node, and detaching that node mid-tap made the popup close the instant it opened.
export function setPanesIcon(btn: HTMLElement, mode: SplitMode): void {
  if (btn.dataset.panesMode === mode) return;
  btn.dataset.panesMode = mode;
  btn.querySelector<HTMLElement>(".pf-v6-c-button__icon")?.replaceChildren(panesIcon(mode));
  const label = btn.querySelector<HTMLElement>(".console-shell-statusbar__panes-label");
  if (label) label.textContent = SPLIT_WORD[mode];
  btn.setAttribute("aria-label", panesName(mode));
  btn.title = panesName(mode);
}

// ---- the build fingerprint ---------------------------------------------------------------

// The status bar shows the connected server's build: its version inline, the full fingerprint on hover.
// Cached once and applied to every tab's bar.
let buildVersion: string | null = null;
let buildFingerprint = "";

function fillVersionChip(el: HTMLElement): void {
  if (!buildVersion) return;
  el.textContent = buildVersion;
  el.title = buildFingerprint || "magus " + buildVersion;
  el.hidden = false;
}

function setBuild(version: string, fingerprint: string): void {
  if (!version) return;
  buildVersion = version;
  buildFingerprint = fingerprint;
  document.querySelectorAll<HTMLElement>("[data-version-chip]").forEach(fillVersionChip);
}

// loadBuildInfo reads the running binary's identity once. In the server-free demo it shows v0.0.0 on
// purpose: a plausible literal would invent a version and a commit, then drift behind the real binary.
export function loadBuildInfo(): void {
  const params = parseHash();
  if (wantsDemo(params)) {
    setBuild("v0.0.0", "synthesized demo data; no server is connected");
    return;
  }
  const host = resolveServerHost(params);
  if (!host) return;
  const client = createClient(StatusService, createServerTransport(host, getLiveToken()));
  client
    .getStatus({})
    .then((res) => {
      const b = res.status?.build;
      if (b?.version) setBuild(b.version, b.fingerprint || "");
    })
    // reported: by the server transport; the version chip keeps its placeholder
    .catch(() => {});
}

// ---- the bar -------------------------------------------------------------------------------

function trayButton(
  className: string,
  hook: string,
  name: string,
  title: string,
  glyph: SVGElement,
): HTMLButtonElement {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "pf-v6-c-button pf-m-plain " + className;
  btn.dataset[hook] = "";
  btn.setAttribute("aria-label", name);
  btn.title = title;
  const icon = document.createElement("span");
  icon.className = "pf-v6-c-button__icon";
  icon.append(glyph);
  btn.append(icon);
  return btn;
}

// makeStatusBar builds one tab's status bar: the SAME element ids the apps write to (#console-conn,
// #console-observing, #console-count) and the .console-shell-statusbar__right slot the log viewer
// injects its zoom control into. Only the ACTIVE tab's bar is attached to the footer, so
// getElementById resolves to the active app's status.
//
// Ownership (see enrichConnHealth in main.ts): an app with a link of its own claims the label and the
// state by stamping data-owner through publishStatus; the readiness poller owns the title and the
// health always, and the label and state on any bar nobody claimed.
//
// withPanesButton adds the Panes tray toggle; the launcher's bar has no panes to act on.
export function makeStatusBar(withPanesButton = true): HTMLElement {
  const bar = document.createElement("div");
  const left = buildConnection();

  const right = document.createElement("div");
  right.dataset.cluster = "";
  right.className = "console-shell-statusbar__right";
  // The counts change with every poll and are read by looking, so they are not live regions.
  for (const id of ["console-count", "console-observing"] as const) {
    const s = document.createElement("span");
    s.id = id;
    s.dataset.item = "";
    s.hidden = true;
    right.append(s);
  }

  // The shared-panel toggles start collapsed; main.ts's syncPanelToggles rewrites aria-expanded on
  // the docked bar whenever a panel opens or closes and whenever a tab swaps its bar in, because
  // there is one button per tab driving a single panel.
  const activity = trayButton(
    "console-shell-statusbar__activity",
    "activityToggle",
    "Activity",
    "Activity. What magus is running now, and what ran recently.",
    activityGlyph(),
  );
  activity.setAttribute("aria-controls", "console-activitypanel");
  activity.setAttribute("aria-expanded", "false");
  right.append(activity);

  if (withPanesButton) {
    const mode = splitModeCell.get();
    const panes = trayButton(
      "console-shell-statusbar__panes",
      "panesToggle",
      panesName(mode),
      panesName(mode),
      panesIcon(mode),
    );
    panes.setAttribute("aria-haspopup", "true");
    panes.setAttribute("aria-expanded", "false");
    panes.setAttribute("aria-controls", "console-panespopup");
    const word = document.createElement("span");
    word.className = "console-shell-statusbar__panes-label";
    word.textContent = SPLIT_WORD[mode];
    panes.append(word);
    panes.dataset.panesMode = mode;
    right.append(panes);
  }

  // Share is a loopback-console affordance: a read-only viewer cannot trigger it, and the server
  // rejects the loopback-guarded endpoint anyway.
  if (!isReadOnly()) {
    const share = trayButton(
      "console-shell-statusbar__share",
      "shareToggle",
      "Share a read-only view",
      "Share a read-only view. A time-boxed link any device on this network can open.",
      shareGlyph(),
    );
    share.setAttribute("aria-controls", "console-sharepanel");
    share.setAttribute("aria-expanded", "false");
    right.append(share);
  }

  right.append(
    trayButton(
      "console-shell-statusbar__shortcuts",
      "cheatsheetToggle",
      "Keyboard shortcuts",
      "Keyboard shortcuts",
      keyboardIcon(),
    ),
  );

  // Build fingerprint, far-right and quiet; hidden until the version loads.
  const ver = document.createElement("span");
  ver.className = "console-shell-statusbar__version";
  ver.dataset.versionChip = "";
  ver.hidden = true;
  fillVersionChip(ver);
  right.append(ver);

  bar.append(left, right);
  return bar;
}
