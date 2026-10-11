// pf.ts - the PatternFly markup the Diff app builds more than once. The console consumes PF as CSS
// only, so each helper spells out the structure its component renders; see README.md, "Styling
// standard", for why a control here is a PF component and not a bare button.

import { h } from "../../desktop/view";
import { expandableSection } from "../../ui/expandable";
import { svgGlyph } from "../../ui/glyph";

export const ANGLE_LEFT: readonly string[] = ["M15 18l-6-6 6-6"];
export const ANGLE_RIGHT: readonly string[] = ["M9 18l6-6-6-6"];
export const CLOSE: readonly string[] = ["M18 6L6 18", "M6 6l12 12"];
export const COPY: readonly string[] = ["M9 9h11v11H9z", "M5 15H4V4h11v1"];
export const CHECK: readonly string[] = ["M5 13l4 4L19 7"];

let seq = 0;

// uid is an element id unique within the document. Several bundles carry their own counter, so the
// prefix is this app's and the caller supplies the part that says what the id is for.
export function uid(what: string): string {
  let id = `console-diff-${what}-${++seq}`;
  while (document.getElementById(id)) id = `console-diff-${what}-${++seq}`;
  return id;
}

// buttonIcon wraps a mark in the structure PF's Icon component renders, which carries the -0.125em
// nudge that sits an svg on the optical centre of the text beside it.
export function buttonIcon(paths: readonly string[]): HTMLElement {
  const slot = h("span", "pf-v6-c-button__icon");
  const icon = h("span", "pf-v6-c-icon pf-m-inline");
  const content = h("span", "pf-v6-c-icon__content");
  content.append(svgGlyph(paths));
  icon.append(content);
  slot.append(icon);
  return slot;
}

// linkButton is a text action inside a row or a sentence: a PF link button, inline so it takes the
// line height of what it sits in instead of PF's control height.
export function linkButton(text: string, opts: { danger?: boolean } = {}): HTMLButtonElement {
  const button = h(
    "button",
    "pf-v6-c-button pf-m-link pf-m-inline" + (opts.danger ? " pf-m-danger" : ""),
    text,
  );
  button.type = "button";
  return button;
}

// plainIconButton is an icon-only PF button. The accessible name is mandatory because the mark
// carries none.
export function plainIconButton(label: string, paths: readonly string[]): HTMLButtonElement {
  const button = h("button", "pf-v6-c-button pf-m-plain");
  button.type = "button";
  button.setAttribute("aria-label", label);
  button.append(buttonIcon(paths));
  return button;
}

// toolbarRow is the PF Toolbar skeleton for one strip: the toolbar, its content and the one content
// section its items and groups go in.
export function toolbarRow(className: string): { el: HTMLElement; section: HTMLElement } {
  const el = h("div", `pf-v6-c-toolbar ${className}`);
  const content = h("div", "pf-v6-c-toolbar__content");
  const section = h("div", "pf-v6-c-toolbar__content-section");
  content.append(section);
  el.append(content);
  return { el, section };
}

// helperLine is one PF Helper text line with a status icon, for the sentence under a control that
// says what the control will do.
export function helperLine(
  status: "warning" | "info",
  icon: HTMLElement,
  text: string,
): HTMLElement {
  const wrap = h("div", "pf-v6-c-helper-text");
  const item = h("div", `pf-v6-c-helper-text__item pf-m-${status}`);
  item.append(
    h("span", "pf-v6-c-helper-text__item-icon"),
    h("span", "pf-v6-c-helper-text__item-text", text),
  );
  item.firstElementChild?.append(icon);
  wrap.append(item);
  return wrap;
}

export interface ToggleItem<T extends string> {
  readonly id: T;
  readonly text: string;
}

export interface ToggleGroup<T extends string> {
  readonly el: HTMLElement;
  readonly buttons: ReadonlyMap<T, HTMLButtonElement>;
  // select marks one item pressed and the rest not, without firing onPick.
  select(id: T | null): void;
}

// toggleGroup is PF's Toggle group over buttons that keep their own pressed state (aria-pressed), so
// a group of one is a toggle and a group of two is a switch between views.
export function toggleGroup<T extends string>(
  label: string,
  items: readonly ToggleItem<T>[],
  onPick: (id: T) => void,
): ToggleGroup<T> {
  const el = h("div", "pf-v6-c-toggle-group pf-m-compact");
  el.setAttribute("role", "group");
  el.setAttribute("aria-label", label);
  const buttons = new Map<T, HTMLButtonElement>();
  for (const item of items) {
    const wrap = h("div", "pf-v6-c-toggle-group__item");
    const button = h("button", "pf-v6-c-toggle-group__button");
    button.type = "button";
    button.setAttribute("aria-pressed", "false");
    button.append(h("span", "pf-v6-c-toggle-group__text", item.text));
    button.addEventListener("click", () => onPick(item.id));
    wrap.append(button);
    el.append(wrap);
    buttons.set(item.id, button);
  }
  return {
    el,
    buttons,
    select(id) {
      for (const [key, button] of buttons) {
        const on = key === id;
        button.setAttribute("aria-pressed", String(on));
        button.classList.toggle("pf-m-selected", on);
      }
    },
  };
}

export interface Tabs<T extends string> {
  readonly el: HTMLElement;
  readonly tabs: ReadonlyMap<T, HTMLButtonElement>;
  select(id: T): void;
}

// tabs is PF's Tabs with the ARIA tab pattern: one tab in the tab order, the arrow keys move
// between them, and each tab names the panel it controls.
export function tabs<T extends string>(
  label: string,
  items: readonly (ToggleItem<T> & { panel: HTMLElement })[],
  onPick: (id: T) => void,
): Tabs<T> {
  const el = h("div", "pf-v6-c-tabs pf-m-secondary");
  const list = h("ul", "pf-v6-c-tabs__list");
  list.setAttribute("role", "tablist");
  list.setAttribute("aria-label", label);
  const byId = new Map<T, HTMLButtonElement>();
  const listItems = new Map<T, HTMLElement>();
  const order = items.map((i) => i.id);
  const select = (id: T): void => {
    for (const [key, button] of byId) {
      const on = key === id;
      button.setAttribute("aria-selected", String(on));
      button.tabIndex = on ? 0 : -1;
      listItems.get(key)?.classList.toggle("pf-m-current", on);
    }
  };
  for (const item of items) {
    const li = h("li", "pf-v6-c-tabs__item");
    li.setAttribute("role", "presentation");
    const button = h("button", "pf-v6-c-tabs__link");
    button.type = "button";
    button.setAttribute("role", "tab");
    button.id = uid("tab");
    item.panel.setAttribute("role", "tabpanel");
    item.panel.setAttribute("aria-labelledby", button.id);
    if (!item.panel.id) item.panel.id = uid("panel");
    button.setAttribute("aria-controls", item.panel.id);
    button.append(h("span", "pf-v6-c-tabs__item-text", item.text));
    button.addEventListener("click", () => {
      select(item.id);
      onPick(item.id);
    });
    button.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowRight" && e.key !== "ArrowLeft") return;
      e.preventDefault();
      e.stopPropagation();
      const at = order.indexOf(item.id);
      const next = order[(at + (e.key === "ArrowRight" ? 1 : order.length - 1)) % order.length];
      if (next === undefined) return;
      select(next);
      byId.get(next)?.focus();
      onPick(next);
    });
    li.append(button);
    list.append(li);
    byId.set(item.id, button);
    listItems.set(item.id, li);
  }
  el.append(list);
  const first = order[0];
  if (first !== undefined) select(first);
  return { el, tabs: byId, select };
}

// radio is one PF Radio: the input, its label, and optional description under it.
export function radio(
  name: string,
  value: string,
  text: string,
  checked: boolean,
  onPick: () => void,
): HTMLElement {
  const wrap = h("div", "pf-v6-c-radio");
  const input = h("input", "pf-v6-c-radio__input");
  input.type = "radio";
  input.name = name;
  input.value = value;
  input.id = uid("radio");
  input.checked = checked;
  input.addEventListener("change", () => {
    if (input.checked) onPick();
  });
  const label = h("label", "pf-v6-c-radio__label", text);
  label.htmlFor = input.id;
  wrap.append(input, label);
  return wrap;
}

export interface Progress {
  readonly el: HTMLElement;
  set(done: number, total: number): void;
}

// progress is PF's small outside-labelled Progress. The bar is the glance; the numbers in its
// status are the exact version of what the bar approximates, which matters here because hunks are
// unequal and a bar over them advances unevenly.
export function progress(description: string, unit: string): Progress {
  const el = h("div", "pf-v6-c-progress pf-m-sm pf-m-outside pf-m-singleline");
  const descId = uid("progress");
  const desc = h("div", "pf-v6-c-progress__description", description);
  desc.id = descId;
  const status = h("div", "pf-v6-c-progress__status");
  status.setAttribute("aria-hidden", "true");
  const measure = h("span", "pf-v6-c-progress__measure");
  status.append(measure);
  const bar = h("div", "pf-v6-c-progress__bar");
  bar.setAttribute("role", "progressbar");
  bar.setAttribute("aria-labelledby", descId);
  bar.setAttribute("aria-valuemin", "0");
  const indicator = h("div", "pf-v6-c-progress__indicator");
  bar.append(indicator);
  el.append(desc, status, bar);
  return {
    el,
    set(done, total) {
      bar.setAttribute("aria-valuemax", String(total));
      bar.setAttribute("aria-valuenow", String(done));
      bar.setAttribute("aria-valuetext", `${done} of ${total} ${unit}`);
      measure.textContent = `${done} of ${total} ${unit}`;
      indicator.style.width = total > 0 ? `${(done / total) * 100}%` : "0%";
    },
  };
}

export interface Disclosure {
  readonly el: HTMLElement;
  readonly toggle: HTMLButtonElement;
  readonly body: HTMLElement;
  set(open: boolean): void;
}

// disclosure is a show/hide section: PF's Expandable section, a toggle over a region.
export function disclosure(text: string, open = false): Disclosure {
  return expandableSection(text, { open, toggleClass: "console-diff-disclosure__toggle" });
}

export interface Popover {
  readonly el: HTMLElement;
  readonly body: HTMLElement;
  readonly isOpen: () => boolean;
  open(): void;
  close(restoreFocus?: boolean): void;
}

// popover is a PF Popover anchored under its trigger by the caller's positioned wrapper. It closes
// on Escape and on a click outside it, and its document listeners exist only while it is open.
export function popover(trigger: HTMLElement, label: string): Popover {
  const el = h("div", "pf-v6-c-popover pf-m-bottom console-diff-popover");
  el.id = uid("popover");
  el.setAttribute("role", "dialog");
  el.setAttribute("aria-label", label);
  el.hidden = true;
  const content = h("div", "pf-v6-c-popover__content");
  const close = plainIconButton("Close " + label.toLowerCase(), CLOSE);
  const closeWrap = h("div", "pf-v6-c-popover__close");
  closeWrap.append(close);
  const body = h("div", "pf-v6-c-popover__body");
  content.append(closeWrap, body);
  el.append(content);
  trigger.setAttribute("aria-haspopup", "dialog");
  trigger.setAttribute("aria-controls", el.id);
  trigger.setAttribute("aria-expanded", "false");

  let listeners: AbortController | null = null;
  const api: Popover = {
    el,
    body,
    isOpen: () => !el.hidden,
    open() {
      if (!el.hidden) return;
      el.hidden = false;
      trigger.setAttribute("aria-expanded", "true");
      listeners = new AbortController();
      const { signal } = listeners;
      document.addEventListener(
        "keydown",
        (e) => {
          if (e.key !== "Escape") return;
          e.preventDefault();
          e.stopPropagation();
          api.close(true);
        },
        { signal, capture: true },
      );
      // Attached a turn later: the click that opened it may still be on its way up to the
      // document, and it must not be the one that closes it.
      window.setTimeout(() => {
        if (signal.aborted) return;
        document.addEventListener(
          "click",
          (e) => {
            const target = e.target instanceof Node ? e.target : null;
            if (el.contains(target) || trigger.contains(target)) return;
            api.close(false);
          },
          { signal },
        );
      }, 0);
    },
    close(restoreFocus = false) {
      if (el.hidden) return;
      el.hidden = true;
      trigger.setAttribute("aria-expanded", "false");
      listeners?.abort();
      listeners = null;
      if (restoreFocus) trigger.focus();
    },
  };
  close.addEventListener("click", () => api.close(true));
  trigger.addEventListener("click", () => (api.isOpen() ? api.close(false) : api.open()));
  return api;
}

export interface ClipboardCopy {
  readonly el: HTMLElement;
  readonly text: HTMLElement;
  setText(text: string): void;
  readonly button: HTMLButtonElement;
}

// COPIED_MS is how long a copy button says it copied before it goes back to its own label.
export const COPIED_MS = 1200;

// clipboardCopy is PF's inline, compact, read-only Clipboard copy: the text is selectable and never
// editable, and the one action puts it on the clipboard. The copy itself is the caller's, so a
// failure reaches the caller's toast.
export function clipboardCopy(
  label: string,
  onCopy: (text: string, done: () => void) => void,
): ClipboardCopy {
  const el = h("div", "pf-v6-c-clipboard-copy pf-m-inline pf-m-readonly");
  const text = h("code", "pf-v6-c-clipboard-copy__text pf-m-code");
  const actions = h("span", "pf-v6-c-clipboard-copy__actions");
  const item = h("span", "pf-v6-c-clipboard-copy__actions-item");
  const button = plainIconButton(label, COPY);
  button.classList.add("pf-m-no-padding");
  let timer: number | undefined;
  button.addEventListener("click", () => {
    onCopy(text.textContent ?? "", () => {
      button.replaceChildren(buttonIcon(CHECK));
      window.clearTimeout(timer);
      timer = window.setTimeout(() => button.replaceChildren(buttonIcon(COPY)), COPIED_MS);
    });
  });
  item.append(button);
  actions.append(item);
  el.append(text, actions);
  return {
    el,
    text,
    button,
    setText(next) {
      text.textContent = next;
    },
  };
}
