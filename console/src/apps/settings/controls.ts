// controls.ts - the PatternFly form pieces the Settings app is built from: a horizontal form group
// with wired helper text, radio and switch controls, the expandable section, and the status line.
// Kept apart from main.ts so every row on every tab takes the same markup.

import { h } from "../../desktop/view";
import { statusIcon, statusText, type Status } from "../../ui/status";

const NS = "http://www.w3.org/2000/svg";

let seq = 0;
const uid = (prefix: string): string => prefix + "-" + ++seq;

export type HelperKind = "default" | "success" | "error";

export interface HelperText {
  el: HTMLElement;
  id: string;
  // set replaces the text and kind; an empty text hides the whole line.
  set(text: string, kind?: HelperKind): void;
}

const HELPER_STATUS: Record<Exclude<HelperKind, "default">, Status> = {
  success: "success",
  error: "danger",
};

// helperText is PF's form helper text. Its item carries the id a control's aria-describedby names,
// and a status kind adds the shape and the hidden word beside the colour.
export function helperText(text = ""): HelperText {
  const id = uid("console-settings-help");
  const wrap = h("div", "pf-v6-c-form__helper-text");
  wrap.setAttribute("aria-live", "polite");
  const root = h("div", "pf-v6-c-helper-text");
  const item = h("div", "pf-v6-c-helper-text__item");
  item.id = id;
  root.append(item);
  wrap.append(root);
  const set = (next: string, kind: HelperKind = "default"): void => {
    item.replaceChildren();
    item.className = "pf-v6-c-helper-text__item" + (kind === "default" ? "" : " pf-m-" + kind);
    if (kind !== "default") {
      const icon = h("span", "pf-v6-c-helper-text__item-icon");
      icon.append(statusIcon(HELPER_STATUS[kind]));
      item.append(icon);
    }
    const body = h("span", "pf-v6-c-helper-text__item-text");
    if (kind !== "default")
      body.append(statusText(HELPER_STATUS[kind], kind === "error" ? "Error:" : "Success:"), " ");
    body.append(next);
    item.append(body);
    wrap.hidden = next === "";
  };
  set(text);
  return { el: wrap, id, set };
}

export interface FormGroupOptions {
  label: string;
  control: HTMLElement;
  // The id of the single input the label names. Without one the group is a radiogroup or a
  // group, named by the label through aria-labelledby.
  controlId?: string;
  groupRole?: "radiogroup" | "group";
  help?: HelperText;
}

// formGroup is one PF horizontal form row. A group of controls (radios, a switch) is named through
// aria-labelledby on the group; a single input through a label for.
export function formGroup(opts: FormGroupOptions): HTMLElement {
  const group = h("div", "pf-v6-c-form__group");
  const labelWrap = h("div", "pf-v6-c-form__group-label");
  const labelId = uid("console-settings-label");
  const text = h("span", "pf-v6-c-form__label-text", opts.label);
  if (opts.controlId) {
    const label = h("label", "pf-v6-c-form__label");
    label.htmlFor = opts.controlId;
    label.append(text);
    labelWrap.append(label);
  } else {
    const label = h("span", "pf-v6-c-form__label");
    label.id = labelId;
    label.append(text);
    labelWrap.append(label);
    group.setAttribute("role", opts.groupRole ?? "group");
    group.setAttribute("aria-labelledby", labelId);
    for (const input of opts.control.querySelectorAll("input")) {
      if (input.type === "checkbox") input.setAttribute("aria-labelledby", labelId);
    }
  }
  const controlWrap = h("div", "pf-v6-c-form__group-control");
  controlWrap.append(opts.control);
  if (opts.help) {
    controlWrap.append(opts.help.el);
    const target = opts.controlId
      ? opts.control.querySelector("#" + CSS.escape(opts.controlId))
      : group;
    target?.setAttribute("aria-describedby", opts.help.id);
  }
  group.append(labelWrap, controlWrap);
  return group;
}

// horizontalForm is the one form every settings row sits in, so General, Appearance and Keymap rows
// share a label column and spacing.
export function horizontalForm(...rows: HTMLElement[]): HTMLFormElement {
  const form = h("form", "pf-v6-c-form pf-m-horizontal");
  form.noValidate = true;
  form.addEventListener("submit", (e) => e.preventDefault());
  form.append(...rows);
  return form;
}

export interface RadioOption<T extends string> {
  value: T;
  label: string;
}

export interface RadioControl<T extends string> {
  el: HTMLElement;
  // set checks one option, or none when value is null (a state that matches no option).
  set(value: T | null): void;
}

// radios is a PF radio set. The browser gives it one tab stop and arrow-key movement; change
// fires once per pick.
export function radios<T extends string>(
  name: string,
  options: RadioOption<T>[],
  onPick: (value: T) => void,
): RadioControl<T> {
  const el = h("div", "console-settings-radios");
  const inputs = new Map<T, HTMLInputElement>();
  for (const o of options) {
    const id = uid("console-settings-radio");
    const wrap = h("div", "pf-v6-c-radio");
    const input = h("input", "pf-v6-c-radio__input");
    input.type = "radio";
    input.name = name;
    input.id = id;
    input.value = o.value;
    input.addEventListener("change", () => {
      if (input.checked) onPick(o.value);
    });
    const label = h("label", "pf-v6-c-radio__label", o.label);
    label.htmlFor = id;
    wrap.append(input, label);
    el.append(wrap);
    inputs.set(o.value, input);
  }
  return {
    el,
    set(value) {
      for (const [v, input] of inputs) input.checked = v === value;
    },
  };
}

export interface SwitchControl {
  el: HTMLElement;
  set(on: boolean): void;
}

// switchControl is a PF switch for a boolean. Its state is the checkbox's, announced by role=switch;
// the On and Off words are shown beside it so the state never rests on the knob's position.
export function switchControl(onChange: (on: boolean) => void): SwitchControl {
  const id = uid("console-settings-switch");
  const label = h("label", "pf-v6-c-switch");
  label.htmlFor = id;
  const input = h("input", "pf-v6-c-switch__input");
  input.type = "checkbox";
  input.id = id;
  input.setAttribute("role", "switch");
  const state = h("span", "pf-v6-c-switch__label", "Off");
  state.setAttribute("aria-hidden", "true");
  const paint = (): void => {
    state.textContent = input.checked ? "On" : "Off";
  };
  input.addEventListener("change", () => {
    paint();
    onChange(input.checked);
  });
  label.append(input, h("span", "pf-v6-c-switch__toggle"), state);
  return {
    el: label,
    set(next) {
      input.checked = next;
      paint();
    },
  };
}

const CHEVRON =
  "M18.71 5.29a.996.996 0 0 0-1.41 0l-7.29 7.29-7.3-7.29a.987.987 0 0 0-1.41-.02.987.987 0 0 0-.02 1.41l.02.02 7.65 7.65c.29.29.68.44 1.06.44s.77-.15 1.06-.44l7.65-7.65a.996.996 0 0 0 0-1.41Z";

// expandable is a PF expandable section: a link-button toggle over a region, closed by default.
export function expandable(toggleText: string, content: HTMLElement): HTMLElement {
  const id = uid("console-settings-expand");
  const root = h("div", "pf-v6-c-expandable-section");
  const toggleBox = h("div", "pf-v6-c-expandable-section__toggle");
  const button = h("button", "pf-v6-c-button pf-m-link");
  button.type = "button";
  button.id = id + "-toggle";
  button.setAttribute("aria-expanded", "false");
  button.setAttribute("aria-controls", id);
  const slot = h("span", "pf-v6-c-button__icon pf-m-start");
  const icon = h("span", "pf-v6-c-expandable-section__toggle-icon");
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", "0 0 20 20");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", CHEVRON);
  svg.append(path);
  icon.append(svg);
  slot.append(icon);
  button.append(slot, h("span", "pf-v6-c-button__text", toggleText));
  toggleBox.append(button);
  const region = h("div", "pf-v6-c-expandable-section__content");
  region.id = id;
  region.hidden = true;
  region.setAttribute("role", "region");
  region.setAttribute("aria-labelledby", button.id);
  region.append(content);
  button.addEventListener("click", () => {
    const open = button.getAttribute("aria-expanded") !== "true";
    button.setAttribute("aria-expanded", String(open));
    region.hidden = !open;
    root.classList.toggle("pf-m-expanded", open);
  });
  root.append(toggleBox, region);
  return root;
}

// statusLine fills a live status element with a shape beside its words, or hides it when there is
// nothing to say. An empty live region would otherwise hold a dead band of space.
export function setStatusLine(el: HTMLElement, message: string, kind: "ok" | "error"): void {
  el.replaceChildren();
  el.dataset.kind = kind;
  el.hidden = message === "";
  if (message === "") return;
  const status: Status = kind === "ok" ? "success" : "danger";
  el.append(
    statusIcon(status),
    statusText(status, kind === "ok" ? "Done:" : "Error:"),
    " ",
    message,
  );
}
