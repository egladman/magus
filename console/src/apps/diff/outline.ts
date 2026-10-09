// outline.ts - what an agent suggests a review conversation should cover, shown to the person.
//
// An outline is a pointer, not a draft. The reply on a review is typed by the person, so nothing
// here can be copied, dragged or sent, and the reply box refuses text that came from one.

import { reportFailure } from "../../lib/notifications";
import type { DiffOutline } from "./session";

// outlineHeading is the line above an outline's topics. An agent that gave no name is still
// labelled as an agent: the topics are never the reader's own words.
export function outlineHeading(outline: DiffOutline): string {
  return `${outline.agent_name || "an agent"} suggests covering:`;
}

// outlinesByThread keys outlines by the thread id they were left for. The server keeps one per
// thread, so a later entry for the same id would only be a stale duplicate.
export function outlinesByThread(
  outlines: readonly DiffOutline[] | undefined,
): ReadonlyMap<string, DiffOutline> {
  const out = new Map<string, DiffOutline>();
  for (const o of outlines ?? []) out.set(o.thread, o);
  return out;
}

// outlineKey is a value that changes exactly when the outlines on screen would, so a poll that
// brought a new one can tell the stream to redraw.
export function outlineKey(outlines: readonly DiffOutline[] | undefined): string {
  return JSON.stringify((outlines ?? []).map((o) => [o.thread, o.agent_name ?? "", o.topics]));
}

const squash = (s: string): string => s.replace(/\s+/g, " ").trim().toLowerCase();

// pastesOutline reports whether text carries any topic of any outline, ignoring case and the
// whitespace a copy through a terminal or a chat window rewraps. Every outline counts, not just
// the one beside the reply box: an agent's words are no more the person's for being about
// another conversation.
export function pastesOutline(text: string, outlines: readonly DiffOutline[]): boolean {
  const pasted = squash(text);
  if (pasted === "") return false;
  for (const o of outlines) {
    for (const topic of o.topics) {
      const t = squash(topic);
      if (t !== "" && pasted.includes(t)) return true;
    }
  }
  return false;
}

// guardOutline makes an outline row read-only to the person's hands: no selection (diff.css), no
// copy, no cut, no drag, and no context menu to reach Copy from.
export function guardOutline(el: HTMLElement): void {
  el.draggable = false;
  for (const type of ["copy", "cut", "dragstart", "contextmenu"]) {
    el.addEventListener(type, (e) => e.preventDefault());
  }
}

export const OUTLINE_PASTE_REFUSAL =
  "That text is an agent's outline. You type the reply yourself; the outline is only a pointer to what it could cover.";

// guardReply refuses a paste or drop into the reply box when the text holds an outline topic.
// outlines is read per event, because the session behind it moves while the box is open.
export function guardReply(
  field: HTMLTextAreaElement,
  outlines: () => readonly DiffOutline[],
): void {
  const refuse = (e: Event, data: DataTransfer | null | undefined): void => {
    if (!pastesOutline(data?.getData("text/plain") ?? "", outlines())) return;
    e.preventDefault();
    reportFailure("Review", OUTLINE_PASTE_REFUSAL, "outline:paste");
  };
  field.addEventListener("paste", (e) => refuse(e, (e as ClipboardEvent).clipboardData));
  field.addEventListener("drop", (e) => refuse(e, (e as DragEvent).dataTransfer));
}
