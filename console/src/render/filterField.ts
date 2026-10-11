// filterField.ts - the one filter box the render apps share: the log viewer's filter, the run
// browser's, the Runs page's and the Activity index's. It is PatternFly's search input (a text input
// group with a leading magnifier), plus the two things every filter needs and each app used to build
// differently or leave out: a Clear control, and a result count the reader can both see and hear.

import { h } from "../desktop/view";

const NS = "http://www.w3.org/2000/svg";
const SEARCH_PATH =
  "m30.796 29.205-8.557-8.557A11.945 11.945 0 0 0 25 13c0-6.617-5.383-12-12-12S1 6.383 1 13s5.383 12 12 12c2.904 0 5.57-1.038 7.648-2.761l8.556 8.556a1.122 1.122 0 0 0 1.592 0 1.127 1.127 0 0 0 0-1.591ZM3 13C3 7.486 7.486 3 13 3s10 4.486 10 10-4.486 10-10 10S3 18.514 3 13Z";
const CLEAR_PATH =
  "M17.8 16.2 11.59 10l6.21-6.21c.42-.46.39-1.17-.07-1.59-.43-.4-1.09-.4-1.52 0l-6.2 6.2-6.22-6.19c-.44-.44-1.15-.44-1.59 0-.44.44-.44 1.15 0 1.59l6.2 6.21-6.2 6.2c-.42.46-.39 1.17.07 1.59.43.4 1.09.4 1.52 0L10 11.59l6.2 6.2c.44.44 1.15.44 1.59 0 .44-.45.44-1.16 0-1.6Z";

function icon(viewBox: string, path: string): SVGElement {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("class", "pf-v6-svg");
  svg.setAttribute("viewBox", viewBox);
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  svg.setAttribute("fill", "currentColor");
  svg.setAttribute("aria-hidden", "true");
  const p = document.createElementNS(NS, "path");
  p.setAttribute("d", path);
  svg.append(p);
  return svg;
}

export interface FilterFieldOptions {
  // The input's accessible name: "Filter runs".
  label: string;
  placeholder: string;
  // Called with the box's text after the debounce, and at once on Clear.
  onChange: (value: string) => void;
  debounceMs?: number;
  id?: string;
  initial?: string;
}

export interface FilterField {
  readonly el: HTMLElement;
  readonly input: HTMLInputElement;
  value(): string;
  // setValue writes the box without calling onChange, for a caller that already applied the text
  // (a deep link, a facet click).
  setValue(value: string): void;
  // setResults shows the count beside the box (visible) and speaks the sentence (hidden). Empty
  // strings clear both.
  setResults(visible: string, spoken: string): void;
  focus(): void;
  // dispose cancels a pending debounce.
  dispose(): void;
}

// createFilterField builds the box. Escape clears it when it holds text, and stops there so a pane
// or panel that closes on Escape stays open.
export function createFilterField(opts: FilterFieldOptions): FilterField {
  const root = h("div", "pf-v6-c-text-input-group console-filter");
  const main = h("div", "pf-v6-c-text-input-group__main pf-m-icon");
  const text = h("span", "pf-v6-c-text-input-group__text");
  const mark = h("span", "pf-v6-c-text-input-group__icon");
  mark.append(icon("0 0 32 32", SEARCH_PATH));

  const input = document.createElement("input");
  input.className = "pf-v6-c-text-input-group__text-input";
  input.type = "text";
  input.setAttribute("role", "searchbox");
  input.setAttribute("aria-label", opts.label);
  input.placeholder = opts.placeholder;
  input.spellcheck = false;
  input.autocomplete = "off";
  input.enterKeyHint = "search";
  if (opts.id) input.id = opts.id;
  if (opts.initial) input.value = opts.initial;
  text.append(mark, input);
  main.append(text);

  const utilities = h("div", "pf-v6-c-text-input-group__utilities");
  const count = h("span", "pf-v6-c-badge pf-m-read console-filter__count");
  count.hidden = true;
  count.setAttribute("aria-hidden", "true");
  const group = h("div", "pf-v6-c-text-input-group__group");
  const clear = h("button", "pf-v6-c-button pf-m-plain console-filter__clear");
  clear.type = "button";
  clear.hidden = true;
  clear.setAttribute("aria-label", "Clear filter");
  const clearIcon = h("span", "pf-v6-c-button__icon");
  clearIcon.append(icon("0 0 20 20", CLEAR_PATH));
  clear.append(clearIcon);
  group.append(clear);
  // The spoken half. role=status is polite, so a count that changes on every debounced keystroke
  // does not interrupt what the reader is typing.
  const spoken = h("span", "pf-v6-screen-reader");
  spoken.setAttribute("role", "status");
  utilities.append(count, group, spoken);
  root.append(main, utilities);

  let timer: ReturnType<typeof setTimeout> | undefined;
  const syncClear = (): void => {
    clear.hidden = input.value === "";
  };
  syncClear();

  input.addEventListener("input", () => {
    syncClear();
    clearTimeout(timer);
    timer = setTimeout(() => opts.onChange(input.value), opts.debounceMs ?? 140);
  });
  const reset = (): void => {
    clearTimeout(timer);
    input.value = "";
    syncClear();
    opts.onChange("");
  };
  clear.addEventListener("click", () => {
    reset();
    input.focus();
  });
  input.addEventListener("keydown", (ev) => {
    if (ev.key === "Escape" && input.value !== "") {
      ev.preventDefault();
      ev.stopPropagation();
      reset();
    }
  });

  return {
    el: root,
    input,
    value: () => input.value,
    setValue(value) {
      clearTimeout(timer);
      input.value = value;
      syncClear();
    },
    setResults(visible, sentence) {
      count.textContent = visible;
      count.hidden = visible === "";
      spoken.textContent = sentence;
    },
    focus: () => input.focus(),
    dispose: () => clearTimeout(timer),
  };
}
