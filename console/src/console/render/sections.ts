// sections.ts - the shared DOM renderers for a status-accented, foldable section of text.
// The log viewer and the activity view both paint the same markup and console-render-* classes
// (styled in logs.css), so a run's output and the daemon's audit trail read as one design.
//
// The log viewer keeps its own scanning loop (render.ts) - it interleaves the #q= filter,
// global line numbering, and the timeline/raw modes - but builds each line and header line
// through the leaf helpers here (renderContent / fillAnsi / renderLine), so both surfaces
// share the exact ANSI-color, status-badge, and line markup. The activity view, which needs
// none of that machinery, assembles whole sections through buildSection.

import { STATUS_RE, parseAnsi, statusToken, stripAnsi } from "./ansi";
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

// toggleSection folds/unfolds a section and syncs the head's aria-expanded. Fold state rides a
// data-collapsed attribute (the console's state-on-data-* convention), not a modifier class.
export function toggleSection(secEl: HTMLElement, head: HTMLElement): void {
  const collapsed = secEl.toggleAttribute("data-collapsed");
  head.setAttribute("aria-expanded", collapsed ? "false" : "true");
}

// sectionAccent derives the status accent class stem for a section from its title: a
// structured "[cached]" head or a heuristic "(cached)" note both mute+fold; otherwise the
// leading status token drives it. Returns "" for an unaccented section.
export function sectionAccent(title: string): string {
  const st = statusToken(title);
  const cached = st === "cached" || /\(cached/i.test(stripAnsi(title));
  return cached ? "cached" : st;
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
  // Extra action buttons appended after copy (e.g. the log viewer's "cmd").
  extraActions?: HTMLElement[];
}

// buildSection assembles one ".console-render-section" element from a Section: a fold-toggle head
// (twist + badge/ANSI title + line count + actions) over its body lines. It is the whole-
// section path the activity view uses; the log viewer builds sections inline so it can
// weave in per-line filtering and numbering, but through the same leaf helpers above.
export function buildSection(sec: Section, opts: BuildSectionOpts = {}): HTMLElement {
  const title = sec.title ?? "";
  const bodyLines = opts.bodyLines ?? sec.lines.slice(1);

  const secEl = document.createElement("div");
  secEl.className = "console-render-section";

  const status = opts.status ?? sectionAccent(title);
  if (status) secEl.setAttribute("data-status", status);
  const collapsed = opts.collapsed ?? status === "cached";
  if (collapsed) secEl.setAttribute("data-collapsed", "");

  const head = document.createElement("button");
  head.type = "button";
  head.className = "console-render-section__head";
  head.setAttribute("aria-expanded", collapsed ? "false" : "true");

  const twist = document.createElement("span");
  twist.className = "console-render-section__twist"; // caret drawn in CSS; no glyph, so the source stays ASCII
  twist.setAttribute("aria-hidden", "true");

  const titleEl = document.createElement("span");
  titleEl.className = "console-render-section__title console-render-line__content";
  renderContent(titleEl, title);

  const count = document.createElement("span");
  count.className = "console-render-section__count";
  count.textContent =
    bodyLines.length > 0 ? bodyLines.length + (bodyLines.length === 1 ? " line" : " lines") : "";

  const actions = document.createElement("span");
  actions.className = "console-render-section__actions";
  if (opts.copyText !== undefined) {
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "console-render-section__action";
    copy.textContent = "copy";
    copy.title = "Copy this section's text";
    const text = opts.copyText;
    copy.addEventListener("click", (ev) => {
      ev.stopPropagation();
      copyText(text, copy);
    });
    actions.append(copy);
  }
  for (const a of opts.extraActions ?? []) actions.append(a);

  head.append(twist, titleEl, count, actions);
  head.addEventListener("click", () => toggleSection(secEl, head));

  const linesWrap = document.createElement("div");
  linesWrap.className = "console-render-section__lines";
  for (const raw of bodyLines) linesWrap.appendChild(renderLine(raw, null));

  secEl.append(head, linesWrap);
  return secEl;
}

// copyText writes text to the clipboard and briefly flashes the control's label, matching
// the log viewer's section copy affordance without pulling in its DOM module.
export function copyText(text: string, btn: HTMLElement): void {
  const done = (ok: boolean): void => {
    const prev = btn.textContent;
    btn.textContent = ok ? "copied" : "failed";
    setTimeout(() => {
      btn.textContent = prev;
    }, 1200);
  };
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(
      () => done(true),
      () => done(false),
    );
  } else {
    done(false);
  }
}
