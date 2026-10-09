// sections.ts - the shared DOM renderers for a status-accented, foldable section of text.
// The log viewer and the activity view both paint the same markup and console-render-* classes
// (styled in frame.css), so a run's output and the server's audit trail read as one design.
//
// The log viewer keeps its own scanning loop (render.ts) - it interleaves the #q= filter,
// global line numbering, and the timeline/raw modes - but builds each line and header line
// through the leaf helpers here (renderContent / fillAnsi / renderLine) and the same section
// frame (createSection), so both apps share the exact ANSI-color, status-badge, line and head
// markup. The activity view, which needs none of that machinery, assembles whole sections through
// buildSection.

import { STATUS_RE, parseAnsi, statusToken, stripAnsi } from "./ansi";
import { copyText } from "./clipboard";
import type { Section } from "./model";

// renderContent fills host with a line, promoting a leading "[status]" token to a
// styled badge (dropping the brackets) and rendering the remainder. Non-status lines
// fall through to the ANSI renderer unchanged.
export function renderContent(host: HTMLElement, raw: string): void {
  const plain = stripAnsi(raw);
  const m = STATUS_RE.exec(plain);
  if (!m) {
    fillAnsi(host, raw);
    return;
  }
  const badge = document.createElement("span");
  badge.className = "console-render-badge console-render-badge--" + m[1].toLowerCase();
  badge.textContent = m[1].toLowerCase();
  host.appendChild(badge);
  host.appendChild(document.createTextNode(plain.slice(m[0].length)));
}

// fillAnsi renders raw (an output line, possibly with ANSI SGR escapes) into host as
// styled spans. Shared by body lines and section heads so both carry the same color.
export function fillAnsi(host: HTMLElement, raw: string): void {
  for (const seg of parseAnsi(raw)) {
    if (seg.cls.length) {
      const span = document.createElement("span");
      span.className = seg.cls.join(" ");
      span.textContent = seg.text;
      host.appendChild(span);
    } else {
      host.appendChild(document.createTextNode(seg.text));
    }
  }
}

// renderLine builds one ".log-line" row: a line-number gutter (when lineNo is a number)
// plus the ANSI/badge-rendered content. onClick, when given, fires on a line-number click
// (the log viewer's GitHub-style #L deep-link); the activity view omits both.
export function renderLine(
  raw: string,
  lineNo: number | null,
  onClick?: (n: number, ev: MouseEvent) => void,
): HTMLElement {
  const line = document.createElement("div");
  line.className = "console-render-line";
  if (lineNo !== null) {
    const ln = document.createElement("span");
    ln.className = "console-render-line__gutter";
    ln.textContent = String(lineNo);
    if (onClick) ln.addEventListener("click", (ev) => onClick(lineNo, ev));
    line.append(ln);
  }
  const lc = document.createElement("span");
  lc.className = "console-render-line__content";
  renderContent(lc, raw);
  line.append(lc);
  return line;
}

// sectionToggle is the button that folds a section: the one control in its head that carries
// aria-expanded. The head also holds the section's action buttons, and a button inside a button is
// not valid markup, so the head itself is a plain row.
export function sectionToggle(secEl: Element): HTMLElement | null {
  return secEl.querySelector<HTMLElement>(".console-render-section__toggle");
}

// setSectionOpen folds or unfolds a section and syncs its toggle's aria-expanded. Fold state rides a
// data-collapsed attribute (the console's state-on-data-* convention), not a modifier class.
export function setSectionOpen(secEl: Element, open: boolean): void {
  secEl.toggleAttribute("data-collapsed", !open);
  sectionToggle(secEl)?.setAttribute("aria-expanded", open ? "true" : "false");
}

// toggleSection flips a section and reports whether it is now open.
export function toggleSection(secEl: Element): boolean {
  const open = secEl.hasAttribute("data-collapsed");
  setSectionOpen(secEl, open);
  return open;
}

// sectionAccent derives the status accent class stem for a section from its title: a
// "(cached, 42ms)" note mutes and folds it; otherwise the leading status token drives it.
// Returns "" for an unaccented section. A cache hit is "[pass] api (cached, 42ms)" - the
// outcome token says pass and the note is what carries the cache state, so the note is the
// only thing to read here.
export function sectionAccent(title: string): string {
  const st = statusToken(title);
  return /\(cached/i.test(stripAnsi(title)) ? "cached" : st;
}

// sectionAction builds one button for a section head's action group. The label is sentence case, the
// same voice as the toolbar's buttons; the title says what it will do.
export function sectionAction(
  label: string,
  title: string,
  onClick: (btn: HTMLButtonElement) => void,
): HTMLButtonElement {
  const btn = document.createElement("button");
  btn.type = "button";
  btn.className = "pf-v6-c-button pf-m-secondary pf-m-small console-render-section__action";
  btn.title = title;
  const text = document.createElement("span");
  text.className = "pf-v6-c-button__text";
  text.textContent = label;
  btn.append(text);
  btn.addEventListener("click", () => onClick(btn));
  return btn;
}

export interface SectionFrameOptions {
  // Accent stem for data-status ("fail", "pass", "cached"); "" for none.
  status: string;
  collapsed: boolean;
  // The line-number cell the log viewer puts first; the activity view has none.
  gutter?: HTMLElement;
  // Fills the title element (badge + ANSI text, or the activity head's labels).
  fillTitle: (title: HTMLElement) => void;
  // The count shown after the title ("3 lines"); "" for none.
  countText: string;
  // Buttons for the action group, in order. Empty omits the group.
  actions: HTMLElement[];
}

export interface SectionFrame {
  secEl: HTMLElement;
  toggle: HTMLButtonElement;
  // The element the caller appends the body lines to.
  lines: HTMLElement;
}

function uniqueId(): string {
  return "console-sec-" + Math.random().toString(36).slice(2, 10);
}

// createSection builds the frame both apps share: a head row holding the fold toggle (with
// aria-expanded and aria-controls) and, beside it, the action group, over the lines the caller fills.
// The actions are siblings of the toggle, never inside it, so each is its own tab stop and a screen
// reader hears the toggle's name without the buttons run into it.
export function createSection(opts: SectionFrameOptions): SectionFrame {
  const secEl = document.createElement("div");
  secEl.className = "console-render-section";
  if (opts.status) secEl.setAttribute("data-status", opts.status);
  if (opts.collapsed) secEl.setAttribute("data-collapsed", "");

  const linesId = uniqueId();
  const head = document.createElement("div");
  head.className = "console-render-section__head";

  const toggle = document.createElement("button");
  toggle.type = "button";
  toggle.className = "console-render-section__toggle";
  toggle.setAttribute("aria-expanded", opts.collapsed ? "false" : "true");
  toggle.setAttribute("aria-controls", linesId);

  const twist = document.createElement("span");
  twist.className = "console-render-section__twist"; // caret drawn in CSS; no glyph, so the source stays ASCII
  twist.setAttribute("aria-hidden", "true");

  const title = document.createElement("span");
  title.className = "console-render-section__title console-render-line__content";
  opts.fillTitle(title);

  const count = document.createElement("span");
  count.className = "console-render-section__count";
  count.textContent = opts.countText;

  if (opts.gutter) toggle.append(opts.gutter);
  toggle.append(twist, title, count);
  head.append(toggle);

  if (opts.actions.length > 0) {
    const group = document.createElement("span");
    group.className = "console-render-section__actions";
    group.setAttribute("role", "group");
    group.setAttribute("aria-label", "Section actions");
    group.append(...opts.actions);
    head.append(group);
  }
  toggle.addEventListener("click", () => toggleSection(secEl));

  const lines = document.createElement("div");
  lines.className = "console-render-section__lines";
  lines.id = linesId;

  secEl.append(head, lines);
  return { secEl, toggle, lines };
}

// countLabel is a body-line count in the section's own noun: "3 lines", "1 detail".
export function countLabel(n: number, noun: readonly [string, string]): string {
  return n > 0 ? n + " " + (n === 1 ? noun[0] : noun[1]) : "";
}

export interface BuildSectionOpts {
  // Accent stem (status-<x>); defaults to sectionAccent(title). Pass "" for no accent.
  status?: string;
  // Fold on build; defaults to true only for a cached section.
  collapsed?: boolean;
  // Body lines to render beneath the head; defaults to sec.lines after the head line.
  bodyLines?: string[];
  // A copy button in the head that copies this text; omitted when undefined.
  copyText?: string;
  // The app named in a failed copy's toast.
  source?: string;
  // Extra action buttons appended after copy (e.g. the log viewer's "Copy command").
  extraActions?: HTMLElement[];
  // The word for the body-line count: ["line", "lines"] by default.
  countNoun?: readonly [string, string];
  // Replaces the default badge-and-ANSI title when the head carries structure of its own.
  fillTitle?: (title: HTMLElement, sec: Section) => void;
}

// buildSection assembles one ".console-render-section" element from a Section: a fold-toggle head
// (twist + badge/ANSI title + line count + actions) over its body lines. It is the whole-
// section path the activity view uses; the log viewer builds sections inline so it can
// weave in per-line filtering and numbering, but through the same createSection frame.
export function buildSection(sec: Section, opts: BuildSectionOpts = {}): HTMLElement {
  const title = sec.title ?? "";
  const bodyLines = opts.bodyLines ?? sec.lines.slice(1);
  const status = opts.status ?? sectionAccent(title);
  const collapsed = opts.collapsed ?? status === "cached";

  const actions: HTMLElement[] = [];
  if (opts.copyText !== undefined) {
    const text = opts.copyText;
    actions.push(
      sectionAction("Copy", "Copy this section's text", (btn) => {
        void copyText(text, {
          source: opts.source ?? "Console",
          what: "this section",
          button: btn,
        });
      }),
    );
  }
  actions.push(...(opts.extraActions ?? []));

  const { secEl, lines } = createSection({
    status,
    collapsed,
    countText: countLabel(bodyLines.length, opts.countNoun ?? ["line", "lines"]),
    fillTitle: (el) => (opts.fillTitle ? opts.fillTitle(el, sec) : renderContent(el, title)),
    actions,
  });
  for (const raw of bodyLines) lines.appendChild(renderLine(raw, null));
  return secEl;
}
