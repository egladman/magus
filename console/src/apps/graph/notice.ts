// notice.ts - what the explorer says to the reader and how a toggle says what it is set to. Both are
// accessibility facts that used to be spread over main.ts one call at a time, so they live here where a
// test can hold them.

import { notify, reportFailure } from "../../lib/notifications";
import { inlineAlert } from "../../ui/alert";

const SOURCE = "Graph";

// info is a hint, warning is a request the graph refused, danger is a failure.
export type StatusLevel = "info" | "warning" | "danger";

export interface StatusAction {
  label: string;
  run: () => void;
}

// showStatus fills host with the app's one notice: ui/alert.ts's inline Alert in the pane and, for a
// refusal or a failure, a notification that also toasts, because a pane the reader has scrolled away
// from says nothing. The toast is deduped on the message, so a repeated refusal toasts once per
// session while the inline alert still answers every time. An empty message hides the host.
//
// The host carries no role of its own: the alert supplies the live role its severity warrants (alert
// for danger, status otherwise), and a second live ancestor would announce it twice.
export function showStatus(
  host: HTMLElement,
  msg: string,
  level: StatusLevel = "info",
  action?: StatusAction,
): void {
  host.replaceChildren();
  host.hidden = !msg;
  // The message as given, for a writer that needs to know whether its own text is still showing.
  host.dataset.message = msg;
  if (!msg) return;
  // Every write replaces the action with its own or none, so a control never outlives the message it
  // belongs to.
  let actions: HTMLElement[] | undefined;
  if (action) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "pf-v6-c-button pf-m-link pf-m-inline";
    button.textContent = action.label;
    button.addEventListener("click", () => action.run());
    actions = [button];
  }
  host.append(inlineAlert({ variant: level, title: msg, actions, live: true }));
  if (level === "danger") reportFailure(SOURCE, msg, "graph:" + msg);
  else if (level === "warning")
    notify({ source: SOURCE, message: msg, kind: "warn", key: "graph:" + msg, toast: true });
}

// setActive keeps a toggle's drawn mark (data-active) and its announced state (aria-pressed) as one
// fact, so a control cannot look pressed to the eye and unpressed to a screen reader.
export function setActive(button: Element, on: boolean): void {
  button.toggleAttribute("data-active", on);
  button.setAttribute("aria-pressed", on ? "true" : "false");
}

// setSelected is setActive for PF toggle-group buttons, whose selected mark is the pf-m-selected class.
export function setSelected(button: Element, on: boolean): void {
  button.classList.toggle("pf-m-selected", on);
  button.setAttribute("aria-pressed", on ? "true" : "false");
}
