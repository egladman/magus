// main.ts - the console's Notes app: the workspace's human-authored notes, both the
// shared store in the checkout and the private one on this machine, separated by scope so a
// reader never has to guess who else can see a note.
//
// READ ONLY, and that is the feature rather than a limitation. A note is the one node class
// the knowledge graph does not derive from the workspace: nothing in the repository
// corroborates it later, so its only provenance is the person who wrote it. A browser edit
// would put an unattributable author on that store, so the way in stays `magus notes edit` -
// an editor, and a commit under the author's name. NotesService has no Update and no Delete to
// call even if this app wanted one.
//
// Read-only makes the app's whole job TRIAGE: which note do I need, is it still true, and
// what do I type when I am back at a keyboard. That is why it is a filtered list against a
// reading pane rather than a gallery of cards - a card that shows a note's path, its anchors
// and its edit command spends more room on the metadata than on the title, and the prose the
// reader came for ends up behind an expander.
//
// There is no delete either, and the reasoning is worth stating because it runs backwards
// from the intuition: SHARED notes are the safe ones to delete (git brings them back) and
// PRIVATE ones are the dangerous ones (nothing does). So if either store ever grew a delete it
// would be the shared one - which is exactly where the CLI and a reviewed commit already do
// the job better. Neither gets one.
//
// Like the activity trail it has no standalone page; activate(host) builds into a console
// host and returns a teardown.

import { createClient } from "@connectrpc/connect";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
  NotesService,
  Scope,
  AnchorStatus,
  Staleness,
  type Note,
  type Anchor,
  type StoreStatus,
} from "@wire/notes/v1alpha1/notes_pb";
import {
  parseHash,
  wantsDemo,
  resolveServerHostOrRemembered,
  isUnreachable,
  adoptServerOrigin,
  consumeLiveToken,
  createServerTransport,
} from "../../lib/server";
import { persisted } from "../../lib/persist";
import { reportFailure } from "../../lib/notifications";
import { subscribeDefaultHost } from "../../lib/settings";
import { h } from "../../desktop/view";
import {
  renderConnectPrompt,
  renderEmptyMessage,
  type ConnectPromptState,
  type EmptyStateSlots,
} from "../../desktop/connectPrompt";
import type { AppInstance } from "../../desktop/standalone";
import { inlineAlert } from "../../ui/alert";
import { svgGlyph } from "../../ui/glyph";
import { statusGlyph, statusIcon, statusText, type Status } from "../../ui/status";
import { demoNotes } from "./demo";
import { parseTranscript, type Transcript } from "./transcript";
import { renderMarkdown } from "./markdown";

const PURPOSE =
  "Notes are prose a person wrote about this workspace, anchored to what it is about.";

// SCOPE_COPY names each store by its CONSEQUENCE rather than by its config key. "shared" and
// "private" are the words in magus.yaml, but what a reader needs at a glance is who ends up
// able to read the note, which is the only difference between the two. The consequence is
// visible text in the store heading and again beside the open note's scope label.
const SCOPE_COPY: Record<
  number,
  { title: string; key: string; consequence: string; color: string }
> = {
  [Scope.SHARED]: {
    title: "Shared",
    key: "shared",
    consequence: "committed, and reviews can read it",
    color: "pf-m-blue",
  },
  [Scope.PRIVATE]: {
    title: "Private",
    key: "private",
    consequence: "this machine only, never committed",
    color: "pf-m-purple",
  },
};

// ANCHOR_COPY maps a status to its label and the status mark that carries its shape. UNVERIFIED
// is deliberately not treated as healthy: it means nothing was checked, which is a different
// answer from "fine", and presenting it as a pass would tell a reader their notes were verified
// when no verification ran.
const ANCHOR_COPY: Record<number, { label: string; status: Status }> = {
  [AnchorStatus.RESOLVES]: { label: "resolves", status: "success" },
  [AnchorStatus.DANGLING]: { label: "dangling", status: "danger" },
  [AnchorStatus.DRIFTED]: { label: "drifted", status: "warning" },
  [AnchorStatus.UNVERIFIED]: { label: "unverified", status: "neutral" },
  [AnchorStatus.BODY_CHANGED]: { label: "body changed", status: "info" },
};

const ANCHOR_KIND_NAME: Record<number, string> = {
  1: "symbol",
  2: "file",
  3: "project",
  4: "target",
  5: "note",
};

const STALENESS_STATUS: Record<number, Status> = {
  [Staleness.OUTRUN]: "warning",
  [Staleness.PETRIFIED]: "danger",
};

// Collapsed store keys, remembered across mounts. A reader who folds "Private" away has made
// a standing choice about what they want to see, not a gesture that should reset on every tab
// switch.
const collapsedCell = persisted<string[]>("notes-collapsed", []);

const COPY_ICON = [
  "M11 9h9a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2h-9a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2Z",
  "M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1",
];
const CHECK_ICON = ["M20 6 9 17l-5-5"];
const BACK_ICON = ["M19 12H5", "m12 19-7-7 7-7"];
const CLEAR_ICON = ["M18 6 6 18", "m6 6 12 12"];
const CHEVRON_ICON = ["m9 6 6 6-6 6"];

interface Refs {
  panel: HTMLElement;
  bar: HTMLElement;
  main: HTMLElement;
  pane: HTMLElement;
  search: HTMLInputElement;
  clear: HTMLButtonElement;
  count: HTMLElement;
  list: HTMLElement;
  detail: HTMLElement;
  back: HTMLButtonElement;
  detailBody: HTMLElement;
  live: HTMLElement;
  empty: HTMLElement;
  emptySlots: EmptyStateSlots;
  emptyHeader: HTMLElement;
}

// tsMillis converts a protobuf Timestamp to epoch milliseconds, or null when absent. A note
// whose store could not stat it has no modify time, and inventing one would put a freshness
// claim on the app that nothing measured.
function tsMillis(t: Timestamp | undefined): number | null {
  if (!t) return null;
  return Number(t.seconds) * 1000 + Math.floor(t.nanos / 1e6);
}

// age renders an elapsed span at one significant unit: a reader scanning the column wants the
// order of magnitude, and "412 days" costs three characters to say what "1y" says.
export function age(ms: number): string {
  const days = Math.floor((Date.now() - ms) / 86400000);
  if (days <= 0) return "today";
  if (days < 31) return days + "d";
  if (days < 365) return Math.floor(days / 30) + "mo";
  return Math.floor(days / 365) + "y";
}

// edited phrases the same span as a sentence. Separate from age because the column wants a
// token and the sentence wants grammar: "today" is already a complete answer, and running it
// through the "edited X ago" frame produces "edited today ago".
export function edited(ms: number): string {
  const span = age(ms);
  return span === "today" ? "Edited today" : "Edited " + span + " ago";
}

// worstAnchor reports the anchor verdict a row should be marked by. Dangling outranks drifted
// because the subject is gone rather than merely changed, and both outrank an unverified
// anchor, which is an absence of evidence rather than a finding.
function worstAnchor(n: Note): { label: string; status: Status; count: number } | null {
  for (const status of [
    AnchorStatus.DANGLING,
    AnchorStatus.DRIFTED,
    AnchorStatus.UNVERIFIED,
    AnchorStatus.BODY_CHANGED,
  ]) {
    const hits = n.anchors.filter((a) => a.status === status);
    const copy = ANCHOR_COPY[status];
    if (hits.length > 0 && copy) {
      return { label: copy.label, status: copy.status, count: hits.length };
    }
  }
  return null;
}

// behind is the staleness phrase, or null when magus measured no divergence. UNMEASURED renders
// nothing at all rather than a reassuring badge: absence of evidence is not evidence of
// freshness. The list row and the open note read the same words.
export function behind(n: Note): { text: string; status: Status } | null {
  const status = STALENESS_STATUS[n.staleness];
  if (!status) return null;
  const unit = n.outrunDays === 1 ? " day" : " days";
  return { text: n.outrunDays + unit + " behind its subject", status };
}

// statusPhrase is a status mark followed by its words, so a hue is never the only carrier.
function statusPhrase(status: Status, words: string): HTMLElement {
  const el = h("span", "console-notes-app__status");
  el.dataset.status = status;
  el.append(statusIcon(status), document.createTextNode(words));
  return el;
}

interface LabelOptions {
  color?: string;
  icon?: Element;
  // Words a screen reader hears after the visible text.
  hidden?: string;
}

// pfLabel builds a compact outline PF Label.
function pfLabel(text: string, opts: LabelOptions = {}): HTMLElement {
  const el = h(
    "span",
    "pf-v6-c-label pf-m-outline pf-m-compact" + (opts.color ? " " + opts.color : ""),
  );
  const content = h("span", "pf-v6-c-label__content");
  if (opts.icon) {
    const icon = h("span", "pf-v6-c-label__icon");
    icon.append(opts.icon);
    content.append(icon);
  }
  content.append(h("span", "pf-v6-c-label__text", text));
  if (opts.hidden) content.append(h("span", "pf-v6-screen-reader", opts.hidden));
  el.append(content);
  return el;
}

// tagList is the PF label group for a note's tags, as a real list a reader can count.
function tagList(tags: string[]): HTMLElement {
  const group = h("div", "pf-v6-c-label-group");
  const main = h("div", "pf-v6-c-label-group__main");
  const list = h("ul", "pf-v6-c-label-group__list");
  list.setAttribute("role", "list");
  list.setAttribute("aria-label", "Tags");
  for (const tag of tags) {
    const item = h("li", "pf-v6-c-label-group__list-item");
    item.append(pfLabel(tag));
    list.append(item);
  }
  main.append(list);
  group.append(main);
  return group;
}

// glyphButton is a PF Button with an icon and visible text.
function glyphButton(
  label: string,
  modifiers: string,
  icon?: readonly string[],
): HTMLButtonElement {
  const b = h("button", "pf-v6-c-button " + modifiers);
  b.type = "button";
  if (icon) {
    const slot = h("span", "pf-v6-c-button__icon pf-m-start");
    slot.append(svgGlyph(icon, 16));
    b.append(slot);
  }
  b.append(h("span", "pf-v6-c-button__text", label));
  return b;
}

// copyRow is a PF inline Clipboard copy: the value beside a button that copies it. The console
// cannot run a command for the reader - and on a phone nothing can - so copying it is the whole
// of the affordance. A failure is a toast; success is announced and shown.
function copyRow(value: string, what: string, announce: (msg: string) => void): HTMLElement {
  const box = h("div", "pf-v6-c-clipboard-copy pf-m-inline");
  box.append(h("code", "pf-v6-c-clipboard-copy__text pf-m-code", value));
  const actions = h("span", "pf-v6-c-clipboard-copy__actions");
  const item = h("span", "pf-v6-c-clipboard-copy__actions-item");
  const btn = h("button", "pf-v6-c-button pf-m-plain");
  btn.type = "button";
  btn.setAttribute("aria-label", "Copy " + what);
  const slot = h("span", "pf-v6-c-button__icon");
  slot.append(svgGlyph(COPY_ICON, 16));
  btn.append(slot);
  const done = h("span", "console-notes-app__copied", "Copied");
  done.hidden = true;
  item.append(btn, done);
  actions.append(item);
  box.append(actions);

  let timer: ReturnType<typeof setTimeout> | undefined;
  const fail = (why: string): void =>
    reportFailure(
      "Notes",
      "Could not copy the " + what + " (" + why + "). Select it and copy it by hand.",
      "notes:copy:" + what,
    );
  btn.addEventListener("click", () => {
    if (!navigator.clipboard?.writeText) {
      fail("this browser has no clipboard access here");
      return;
    }
    navigator.clipboard.writeText(value).then(
      () => {
        announce("Copied the " + what + ".");
        slot.replaceChildren(svgGlyph(CHECK_ICON, 16));
        done.hidden = false;
        clearTimeout(timer);
        timer = setTimeout(() => {
          slot.replaceChildren(svgGlyph(COPY_ICON, 16));
          done.hidden = true;
        }, 1500);
      },
      (e: unknown) => fail(e instanceof Error ? e.message : String(e)),
    );
  });
  return box;
}

// emptyBlock is a PF small Empty state: a title, a sentence, and optional actions.
function emptyBlock(title: string, message: string, actions: HTMLElement[] = []): HTMLElement {
  const root = h("div", "pf-v6-c-empty-state pf-m-sm console-notes-app__hint");
  const content = h("div", "pf-v6-c-empty-state__content");
  const header = h("div", "pf-v6-c-empty-state__header");
  const heading = h("div", "pf-v6-c-empty-state__title");
  heading.append(h("h2", "pf-v6-c-empty-state__title-text", title));
  header.append(heading);
  content.append(header, h("div", "pf-v6-c-empty-state__body", message));
  if (actions.length > 0) {
    const footer = h("div", "pf-v6-c-empty-state__footer");
    const row = h("div", "pf-v6-c-empty-state__actions");
    row.append(...actions);
    footer.append(row);
    content.append(footer);
  }
  root.append(content);
  return root;
}

// buildScaffold assembles the app: a filtered list beside a reading pane, over a PF
// EmptyState for the cold case. The panel root keeps its own class rather than the log viewer's
// `.console-render-panel`, and console.css's fill chain names it alongside the other app
// roots.
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "console-notes-app");
  panel.setAttribute("aria-label", "Notes");

  const main = h("div", "console-notes-app__main");
  const pane = h("div", "console-notes-app__pane");

  const bar = h("div", "console-notes-app__bar");
  bar.dataset.controlSize = "default";
  bar.setAttribute("role", "search");
  bar.setAttribute("aria-label", "Notes");
  const group = h("div", "pf-v6-c-text-input-group console-notes-app__search");
  const groupMain = h("div", "pf-v6-c-text-input-group__main");
  const textWrap = h("span", "pf-v6-c-text-input-group__text");
  const search = h("input", "pf-v6-c-text-input-group__text-input");
  search.type = "search";
  // The placeholder teaches the non-obvious half: you can find a note by the code it annotates,
  // not only by its own words. It stops short of promising a text search, because ListNotes
  // leaves the prose empty by contract, so a filter claiming to read it would miss every note
  // the reader has not opened.
  search.placeholder = "Filter notes, or the code they are about";
  search.setAttribute("aria-label", "Filter notes by title, tag or anchor");
  search.spellcheck = false;
  search.autocomplete = "off";
  textWrap.append(search);
  groupMain.append(textWrap);
  const utilities = h("div", "pf-v6-c-text-input-group__utilities");
  const clear = h("button", "pf-v6-c-button pf-m-plain");
  clear.type = "button";
  clear.setAttribute("aria-label", "Clear filter");
  clear.hidden = true;
  const clearIcon = h("span", "pf-v6-c-button__icon");
  clearIcon.append(svgGlyph(CLEAR_ICON, 16));
  clear.append(clearIcon);
  utilities.append(clear);
  group.append(groupMain, utilities);

  const count = h("span", "console-notes-app__count");
  count.setAttribute("role", "status");
  count.setAttribute("aria-live", "polite");
  bar.append(h("span", "console-notes-app__label", "Notes"), group, count);

  const list = h("div", "console-notes-app__list");
  pane.append(list);

  const detail = h("section", "console-notes-app__detail");
  detail.setAttribute("aria-label", "Open note");
  const detailBar = h("div", "console-notes-app__detail-bar");
  detailBar.dataset.controlSize = "default";
  const back = glyphButton("Back to notes", "pf-m-link", BACK_ICON);
  detailBar.append(back);
  const detailBody = h("div", "console-notes-app__detail-body");
  detail.append(detailBar, detailBody);

  main.append(pane, detail);

  const live = h("div", "pf-v6-screen-reader");
  live.setAttribute("role", "status");
  live.setAttribute("aria-live", "polite");

  const empty = h("div", "pf-v6-c-empty-state console-notes-app__empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyHeader = h("div", "pf-v6-c-empty-state__header");
  const emptyTitleBox = h("div", "pf-v6-c-empty-state__title");
  const emptyTitle = h("h2", "pf-v6-c-empty-state__title-text");
  emptyTitleBox.append(emptyTitle);
  emptyHeader.append(emptyTitleBox);
  const emptyMessage = h("div", "pf-v6-c-empty-state__body");
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  emptyActions.dataset.emptyWays = "";
  emptyContent.append(emptyHeader, emptyMessage, emptyActions);
  empty.append(emptyContent);

  panel.append(h("h1", "pf-v6-screen-reader", "Notes"), bar, main, live, empty);
  host.append(panel);
  return {
    bar,
    panel,
    main,
    pane,
    search,
    clear,
    count,
    list,
    detail,
    back,
    detailBody,
    live,
    empty,
    emptySlots: { title: emptyTitle, message: emptyMessage, actions: emptyActions },
    emptyHeader,
  };
}

type Prose =
  | { state: "loading" }
  | { state: "ready"; body: string }
  | { state: "error"; message: string };

// activate builds the app into host, loads once, and returns a teardown. Every async load
// checks `stale` before touching the DOM, so a load that resolves after the tab closed is
// dropped.
//
// Returns the console's app shape (page.ts): a teardown plus setVisible, so the shell can tell
// this pane when it stops being the visible one.
export function activate(host: HTMLElement): AppInstance {
  const refs = buildScaffold(host);
  let stale = false;

  let notes: Note[] = [];
  let stores: StoreStatus[] = [];
  let selected: string | null = null;
  let loadBody: (n: Note) => Promise<string> = () => Promise.resolve("");
  let proseHost: HTMLElement | null = null;
  // Bumped by every body request, so a slow answer for a note the reader has left is dropped.
  let bodyGeneration = 0;

  const announce = (msg: string): void => {
    refs.live.textContent = msg;
  };

  function showEmptyNotes(): void {
    notes = [];
    stores = [];
    selected = null;
    refs.list.replaceChildren();
    closeDetail(false);
    refs.detailBody.replaceChildren();
    refs.main.hidden = true;
    refs.bar.hidden = true;
    refs.empty.hidden = false;
  }

  function resetEmptyIcon(): void {
    refs.empty.classList.remove("pf-m-danger");
    refs.emptyHeader.querySelector(".pf-v6-c-empty-state__icon")?.remove();
  }

  function showConnectPrompt(state: ConnectPromptState): void {
    showEmptyNotes();
    resetEmptyIcon();
    renderConnectPrompt(refs.emptySlots, state, { purpose: PURPOSE, onRetry: load });
  }

  // The transport has already raised the toast for a failed call; this is the text where the
  // reader is looking, with a Retry, since nothing retries behind it.
  function showNotesError(message: string): void {
    showEmptyNotes();
    resetEmptyIcon();
    renderEmptyMessage(refs.emptySlots, "Could not read the notes", message);
    refs.empty.classList.add("pf-m-danger");
    const icon = h("div", "pf-v6-c-empty-state__icon");
    icon.append(statusGlyph("danger"));
    refs.emptyHeader.prepend(icon);
    const retry = glyphButton("Retry", "pf-m-primary");
    retry.addEventListener("click", () => load());
    refs.emptySlots.actions.append(retry);
  }

  // The store is part of a note's resource name ("shared/x", "private/x") rather than a
  // second request field, so a name and a scope can never arrive disagreeing.
  const noteResourceName = (n: Note): string =>
    (n.scope === Scope.PRIVATE ? "private/" : "shared/") + n.name;

  function findNote(name: string): Note | undefined {
    return notes.find((n) => n.name === name);
  }

  // matches searches what ListNotes actually carries. Anchors are included because "which note
  // covers this file" is as common a question as "which note has this word in the title".
  function matches(n: Note, term: string): boolean {
    if (!term) return true;
    const hay = [
      n.title,
      n.name,
      n.tags.join(" "),
      n.anchors.map((a) => (ANCHOR_KIND_NAME[a.kind] ?? "anchor") + ":" + a.target).join(" "),
    ]
      .join(" ")
      .toLowerCase();
    return hay.includes(term);
  }

  // Most recently edited first. The wire order is the store's scan order, which means nothing to
  // a reader; modify time is the one ordering the note files themselves carry. Notes the store
  // could not stat sort last rather than to the top, so a missing timestamp cannot masquerade as
  // the freshest thing here.
  function byRecency(a: Note, b: Note): number {
    const am = tsMillis(a.modifyTime);
    const bm = tsMillis(b.modifyTime);
    if (am === null && bm === null) return a.title.localeCompare(b.title);
    if (am === null) return 1;
    if (bm === null) return -1;
    return bm - am;
  }

  // --- the list: one roving tab stop over store headings and note rows -------------------

  const stops = (): HTMLButtonElement[] =>
    Array.from(refs.list.querySelectorAll<HTMLButtonElement>("[data-roving]")).filter(
      (b) => !b.closest("[hidden]"),
    );

  function settleTabStop(preferKey: string | null): void {
    const all = stops();
    const pick =
      all.find((b) => b.dataset.roving === preferKey) ??
      all.find((b) => b.getAttribute("aria-current") === "true") ??
      all[0];
    for (const b of all) b.tabIndex = b === pick ? 0 : -1;
  }

  refs.list.addEventListener("focusin", (e) => {
    const target = e.target;
    if (!(target instanceof HTMLButtonElement) || target.dataset.roving === undefined) return;
    for (const b of stops()) b.tabIndex = b === target ? 0 : -1;
  });
  refs.list.addEventListener("keydown", (e) => {
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(e.key)) return;
    const all = stops();
    const here = all.indexOf(document.activeElement as HTMLButtonElement);
    if (here < 0) return;
    e.preventDefault();
    const last = all.length - 1;
    const next =
      e.key === "Home"
        ? 0
        : e.key === "End"
          ? last
          : Math.max(0, Math.min(last, here + (e.key === "ArrowDown" ? 1 : -1)));
    all[next]?.focus();
  });

  function buildRow(n: Note): HTMLElement {
    const item = h("li", "pf-v6-c-data-list__item");
    const row = h("button", "console-notes-app__note");
    row.type = "button";
    row.dataset.name = n.name;
    row.dataset.roving = "note:" + n.name;
    if (selected === n.name) {
      row.setAttribute("aria-current", "true");
      item.classList.add("pf-m-selected");
    }

    const top = h("span", "console-notes-app__note-top");
    // Marked in the LIST, not only once a reader opens it. A capture is quoted material that
    // nobody stands behind, and a reader who learns that after reading it has already taken it
    // for a colleague's reasoning.
    if (n.source) {
      top.append(
        pfLabel("Quoted", {
          color: "pf-m-purple",
          hidden: ": a transcript captured from a " + n.source.kind + ", not prose someone wrote",
        }),
      );
    }
    top.append(h("span", "console-notes-app__note-title", n.title || n.name));
    const ms = tsMillis(n.modifyTime);
    if (ms !== null) {
      // The age column means ONE thing on every row: when the file was last edited. Staleness
      // is a different quantity (how far the subject ran ahead of the prose) and sits in the
      // meta line below with the rest of the evidence.
      const ageEl = h("span", "console-notes-app__note-age");
      ageEl.append(
        h("span", "pf-v6-screen-reader", "Last edited "),
        document.createTextNode(age(ms)),
      );
      top.append(ageEl);
    }
    row.append(top);

    const meta = h("span", "console-notes-app__note-meta");
    const lag = behind(n);
    if (lag) meta.append(statusPhrase(lag.status, lag.text));
    const broken = worstAnchor(n);
    if (broken) meta.append(statusPhrase(broken.status, broken.count + " " + broken.label));
    for (const tag of n.tags) meta.append(pfLabel(tag));
    if (meta.childElementCount > 0) row.append(meta);

    row.addEventListener("click", () => openNote(n));
    item.append(row);
    return item;
  }

  const collapsed = (): string[] => collapsedCell.get() ?? [];

  function toggleStore(key: string): void {
    const now = collapsed();
    collapsedCell.set(now.includes(key) ? now.filter((k) => k !== key) : [...now, key]);
    renderList();
  }

  function clearFilter(): void {
    refs.search.value = "";
    renderList();
    refs.search.focus();
  }

  function renderList(): void {
    const term = refs.search.value.trim().toLowerCase();
    const focusedKey = refs.list.contains(document.activeElement)
      ? ((document.activeElement as HTMLElement).dataset.roving ?? null)
      : null;
    refs.list.replaceChildren();
    refs.clear.hidden = refs.search.value === "";
    let shown = 0;

    for (const store of stores) {
      const copy = SCOPE_COPY[store.scope];
      if (!copy) continue;
      if (store.declared) {
        shown += notes.filter((n) => n.scope === store.scope && matches(n, term)).length;
      }
    }

    if (term && shown === 0 && notes.length > 0) {
      const clear = glyphButton("Clear filter", "pf-m-link");
      clear.addEventListener("click", clearFilter);
      refs.list.append(emptyBlock("No notes match", "Nothing matches that filter.", [clear]));
      reconcile(term, shown, focusedKey);
      return;
    }

    for (const store of stores) {
      const copy = SCOPE_COPY[store.scope];
      if (!copy) continue;

      const mine = notes.filter((n) => n.scope === store.scope && matches(n, term)).sort(byRecency);
      // A filter that silently hid its own matches inside a collapsed store would be the
      // search lying, so an active term expands everything for as long as it is set.
      const folded = term === "" && collapsed().includes(copy.key);

      const group = h("div", "console-notes-app__group");
      const regionId = "console-notes-list-" + copy.key;
      const heading = h("h2", "console-notes-app__store-heading");
      const head = h("button", "console-notes-app__store");
      head.type = "button";
      head.dataset.scope = copy.key;
      head.dataset.roving = "store:" + copy.key;
      head.setAttribute("aria-expanded", String(!folded));
      head.setAttribute("aria-controls", regionId);
      const twist = h("span", "console-notes-app__store-twist");
      twist.append(svgGlyph(CHEVRON_ICON, 16));
      head.append(twist);
      // Parenthesized, because the number is a count of what is under this heading and not
      // part of the store's name - "Shared 4" reads for a moment as a fourth Shared.
      head.append(
        h("span", "console-notes-app__store-title", copy.title + " (" + mine.length + ")"),
      );
      // A store that is declared and empty and a store that is not declared at all are
      // different facts, and a blank area would say the first when it means the second.
      head.append(
        h(
          "span",
          "console-notes-app__store-consequence",
          store.declared ? copy.consequence : "not declared",
        ),
      );
      head.addEventListener("click", () => toggleStore(copy.key));
      heading.append(head);

      const region = h("div", "console-notes-app__region");
      region.id = regionId;
      region.hidden = folded;
      group.append(heading, region);
      refs.list.append(group);

      if (!store.declared) {
        const hint = h("p", "console-notes-app__note-none");
        hint.append(
          document.createTextNode("Set "),
          h("code", undefined, "knowledge.notes." + copy.key),
          document.createTextNode(" in magus.yaml to enable this store."),
        );
        region.append(hint);
        continue;
      }

      if (store.issues.length > 0) {
        const issues = h("div", "console-notes-app__issues");
        for (const issue of store.issues) {
          issues.append(inlineAlert({ variant: "warning", title: issue }));
        }
        region.append(issues);
      }

      if (mine.length === 0) {
        const hint = h("p", "console-notes-app__note-none");
        if (term) {
          hint.textContent = "No note here matches that filter.";
        } else {
          hint.append(
            document.createTextNode("Nothing here yet. Run "),
            h("code", undefined, "magus notes edit <name>"),
            document.createTextNode(" to open your editor and write the first one."),
          );
        }
        region.append(hint);
        continue;
      }
      const ul = h("ul", "pf-v6-c-data-list pf-m-compact pf-m-grid-none");
      ul.setAttribute("role", "list");
      ul.setAttribute("aria-label", copy.title + " notes");
      for (const n of mine) ul.append(buildRow(n));
      region.append(ul);
    }
    reconcile(term, shown, focusedKey);
  }

  // reconcile brings everything that depends on the rendered list back in step with it: the tab
  // stop, the focus a re-render would otherwise drop, the count, and the open note.
  function reconcile(term: string, shown: number, focusedKey: string | null): void {
    settleTabStop(focusedKey);
    if (focusedKey)
      stops()
        .find((b) => b.dataset.roving === focusedKey)
        ?.focus();
    const total = notes.length;
    refs.count.textContent = term
      ? shown + " of " + total + (total === 1 ? " note" : " notes")
      : total + (total === 1 ? " note" : " notes");

    // The reading pane keeps whatever is open, but a filter that hides the open note leaves the
    // list and the pane disagreeing about what is selected.
    if (selected && !refs.list.querySelector('[data-name="' + CSS.escape(selected) + '"]')) {
      showBlank();
    }
  }

  // --- the reading pane --------------------------------------------------------------------

  // The detail is an overlay in a narrow pane and a second column in a wide one; the stylesheet's
  // container query decides which, and this asks it. Over a pane that is showing the detail as an
  // overlay the list and filter beneath are inert, so neither Tab nor a screen reader can reach
  // what the overlay hides.
  const isOverlay = (): boolean => getComputedStyle(refs.detail).position === "absolute";

  function syncOverlay(): void {
    const covering = refs.detail.hasAttribute("data-open") && isOverlay();
    for (const el of [refs.pane, refs.bar]) {
      if (covering) el.setAttribute("inert", "");
      else el.removeAttribute("inert");
    }
  }

  function closeDetail(restoreFocus: boolean): void {
    refs.detail.removeAttribute("data-open");
    syncOverlay();
    if (!restoreFocus || !selected) return;
    refs.list.querySelector<HTMLElement>('[data-name="' + CSS.escape(selected) + '"]')?.focus();
  }

  refs.back.addEventListener("click", () => closeDetail(true));
  refs.detail.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || !refs.detail.hasAttribute("data-open") || !isOverlay()) return;
    e.stopPropagation();
    closeDetail(true);
  });
  const resizeObserver =
    typeof ResizeObserver === "undefined" ? null : new ResizeObserver(() => syncOverlay());
  resizeObserver?.observe(refs.panel);

  function showBlank(): void {
    selected = null;
    proseHost = null;
    bodyGeneration++;
    closeDetail(false);
    refs.detailBody.removeAttribute("aria-busy");
    refs.detailBody.replaceChildren(
      emptyBlock("No note selected", "Choose a note from the list to read it."),
    );
  }

  // buildTranscript renders a captured conversation as a thread.
  //
  // Two groupings, and they are what stop it reading as one person talking to themselves.
  // The FILE is a divider, printed once where it changes, the way a channel names what is
  // being discussed rather than tagging every line. The SPEAKER is printed once per run, so
  // three consecutive messages from the same person are three messages and one name - a
  // magus review has exactly one human in it, and repeating the label on every message made
  // a normal review look like a monologue.
  //
  // A message still gets its own box. Run together as prose a transcript reads as though one
  // person wrote all of it, which is precisely the reading a capture exists to prevent.
  function buildTranscript(t: Transcript): HTMLElement {
    const wrap = h("div", "console-notes-app__thread");
    let subject = "";
    let author = "";
    for (const e of t.entries) {
      const where = [e.subject, e.locator].filter(Boolean).join(" ");
      if (where !== subject) {
        subject = where;
        author = ""; // a new file restates who is speaking, even for the same person
        wrap.append(h("h3", "console-notes-app__thread-file", where));
      }

      const box = h("article", "console-notes-app__entry");
      if (e.resolved) box.dataset.resolved = "";
      // Agent and person are marked apart because a tool's output reading as a colleague's
      // opinion is the one misreading a transcript must not allow.
      box.dataset.voice = e.author.endsWith("(agent)") || e.author === "agent" ? "agent" : "person";

      if (e.author !== author) {
        author = e.author;
        box.append(h("div", "console-notes-app__entry-author", e.author));
      } else {
        box.dataset.continued = "";
      }
      if (e.resolved) {
        box.append(pfLabel("Resolved", { color: "pf-m-green", icon: statusIcon("success") }));
      }

      // textContent, as everywhere else a note's prose is rendered: this is quoted material
      // out of a file on disk and must never become a way to run markup someone pasted in.
      box.append(h("p", "console-notes-app__entry-body", e.body));
      wrap.append(box);
    }
    return wrap;
  }

  function buildAnchor(a: Anchor): HTMLElement {
    const copy = ANCHOR_COPY[a.status] ?? ANCHOR_COPY[AnchorStatus.UNVERIFIED];
    const row = h("li", "console-notes-app__anchor");
    row.append(statusPhrase(copy?.status ?? "neutral", copy?.label ?? "unverified"));

    const target = h("span", "console-notes-app__anchor-target");
    target.append(document.createTextNode((ANCHOR_KIND_NAME[a.kind] ?? "anchor") + " " + a.target));
    if (a.detail) target.append(h("span", "console-notes-app__anchor-detail", a.detail));
    // The node id is the handle a reader carries to the Graph Explorer by hand. It is text and
    // not a link because cross-app navigation carries a pageId and nothing else today.
    if (a.nodeId) target.append(h("span", "console-notes-app__anchor-detail", a.nodeId));
    row.append(target);
    return row;
  }

  // setProse swaps the note's body region between loading, ready and failed without touching the
  // heading or the facts, so a body arriving neither moves focus nor shifts the layout around it.
  function setProse(n: Note, prose: Prose): void {
    const slot = proseHost;
    if (!slot) return;
    refs.detailBody.setAttribute("aria-busy", String(prose.state === "loading"));
    if (prose.state === "loading") {
      const skeleton = h("div", "console-notes-app__loading");
      skeleton.setAttribute("role", "status");
      skeleton.append(h("span", "pf-v6-screen-reader", "Loading the note"));
      for (const width of ["pf-m-width-75", "", "", "pf-m-width-50", "", "pf-m-width-66"]) {
        skeleton.append(h("div", "pf-v6-c-skeleton pf-m-text-md " + width));
      }
      slot.replaceChildren(skeleton);
      return;
    }
    if (prose.state === "error") {
      const retry = glyphButton("Retry", "pf-m-link pf-m-inline");
      retry.addEventListener("click", () => requestBody(n));
      slot.replaceChildren(
        inlineAlert({
          variant: "danger",
          title: "Could not load this note",
          body: prose.message,
          actions: [retry],
        }),
      );
      return;
    }
    // A capture renders as its entries where the body reads back as one, and verbatim where it
    // does not. Losing the boxes is cosmetic; losing the transcript would not be.
    const transcript = n.source ? parseTranscript(n.source.kind, prose.body) : null;
    if (transcript) {
      slot.replaceChildren(
        h("p", "console-notes-app__prose", transcript.preamble),
        buildTranscript(transcript),
      );
      return;
    }
    // A note IS a markdown file, so it is rendered as one. renderMarkdown builds nodes rather
    // than markup - no innerHTML, no HTML string anywhere - which keeps the untrusted-body
    // guarantee structural while letting a hard-wrapped paragraph reflow to the pane.
    const body = h("div", "console-notes-app__prose");
    body.append(renderMarkdown(prose.body));
    slot.replaceChildren(body);
  }

  function renderNote(n: Note): HTMLElement {
    const copy = SCOPE_COPY[n.scope];
    const read = h("div", "console-notes-app__read");
    const title = h("h2", "console-notes-app__title", n.title || n.name);
    title.tabIndex = -1;
    read.append(title);

    const sub = h("div", "console-notes-app__subtitle");
    if (copy) {
      sub.append(
        pfLabel(copy.title, { color: copy.color }),
        h("span", undefined, copy.consequence),
      );
    }
    if (n.source) {
      sub.append(
        pfLabel("Quoted", { color: "pf-m-purple" }),
        h(
          "span",
          undefined,
          "a transcript captured from a " + n.source.kind + ", not prose someone wrote",
        ),
      );
    }
    const ms = tsMillis(n.modifyTime);
    if (ms !== null) sub.append(h("span", undefined, edited(ms)));
    const lag = behind(n);
    if (lag) {
      sub.append(
        statusPhrase(
          lag.status,
          lag.text +
            (n.staleness === Staleness.PETRIFIED ? ". Re-read it before relying on it" : ""),
        ),
      );
    }
    read.append(sub);
    if (n.tags.length > 0) read.append(tagList(n.tags));

    proseHost = h("div", "console-notes-app__prose-host");
    read.append(proseHost);

    const facts = h("div", "console-notes-app__facts");
    if (n.anchors.length > 0) {
      facts.append(h("h3", "console-notes-app__facts-head", "Anchored to"));
      const anchors = h("ul", "console-notes-app__anchors");
      anchors.setAttribute("role", "list");
      for (const a of n.anchors) anchors.append(buildAnchor(a));
      facts.append(anchors);
    }
    facts.append(h("h3", "console-notes-app__facts-head", "File"));
    facts.append(copyRow(n.path, "path", announce));
    // The path and the command, rather than an edit box. This is where a reader goes to change
    // a note, and naming the command is the whole affordance: the write path is a person in an
    // editor, and the console showing a text box would be the thing this store exists to prevent.
    facts.append(h("h3", "console-notes-app__facts-head", "Edit from a terminal"));
    facts.append(copyRow("magus notes edit " + n.name, "edit command", announce));

    refs.detailBody.replaceChildren(read, facts);
    refs.detailBody.scrollTop = 0;
    return title;
  }

  function requestBody(n: Note): void {
    const generation = ++bodyGeneration;
    setProse(n, { state: "loading" });
    const current = (): boolean => !stale && selected === n.name && generation === bodyGeneration;
    loadBody(n).then(
      (body) => {
        // A second click before the first body lands must not overwrite the note now open.
        if (current()) setProse(findNote(n.name) ?? n, { state: "ready", body });
      },
      (e: unknown) => {
        // reported: by the server transport's failure interceptor
        if (current()) {
          setProse(n, { state: "error", message: e instanceof Error ? e.message : String(e) });
        }
      },
    );
  }

  function openNote(n: Note): void {
    selected = n.name;
    for (const row of refs.list.querySelectorAll<HTMLElement>(".console-notes-app__note")) {
      const on = row.dataset.name === n.name;
      if (on) row.setAttribute("aria-current", "true");
      else row.removeAttribute("aria-current");
      row.closest("li")?.classList.toggle("pf-m-selected", on);
    }
    refs.detail.dataset.open = "";
    const title = renderNote(n);
    syncOverlay();
    // Focus follows the reader into the overlay. Beside the list it stays on the row, where the
    // arrow keys still move through the notes.
    if (isOverlay()) title.focus();
    requestBody(n);
  }

  function show(
    next: Note[],
    nextStores: StoreStatus[],
    fetch: (n: Note) => Promise<string>,
  ): void {
    notes = next;
    stores = nextStores;
    loadBody = fetch;
    refs.empty.hidden = true;
    refs.main.hidden = false;
    refs.bar.hidden = false;
    renderList();
    showBlank();
  }

  // Bumped by every load, so the answer from an address the reader has since moved off is dropped
  // instead of painted over the current one.
  let loadGeneration = 0;

  async function loadLive(serverHost: string): Promise<void> {
    const generation = ++loadGeneration;
    const superseded = (): boolean => stale || generation !== loadGeneration;
    const client = createClient(NotesService, createServerTransport(serverHost));
    try {
      const resp = await client.listNotes({});
      if (superseded()) return;
      show(resp.notes, resp.stores, async (n) => {
        const one = await client.getNote({ name: noteResourceName(n) });
        return one.body ?? "";
      });
    } catch (e) {
      if (superseded()) return;
      const msg = e instanceof Error ? e.message : String(e);
      if (!isUnreachable(e)) {
        showNotesError("The server at " + serverHost + " answered with an error (" + msg + ").");
        return;
      }
      showConnectPrompt({ connection: "disconnected", host: serverHost, reason: msg });
    }
  }

  // loadDemo renders invented notes. The disclosure that they ARE invented is not optional -
  // authorship is the entire claim a note makes, and sample prose passing as something a colleague
  // wrote is the one lie this store cannot survive - but this app no longer carries it. Demo
  // mode is entered through the Workspace menu, which sets #demo, and the shell's connection dot
  // reads "demo" off that fragment for as long as it is set, on this tab and every other.
  function loadDemo(): void {
    const demo = demoNotes();
    show(demo.notes, demo.stores, (n) => Promise.resolve(demo.body(n.name)));
  }

  // load resolves what to read: an explicit #demo, then resolveServerHostOrRemembered (a #port
  // link, the server-origin console, the Settings address, or the last server the dashboard reached).
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
    // adoptServerOrigin, not just consumeLiveToken. Each app is its own esbuild bundle, so
    // lib/server's "did we adopt this origin" flag is PER-BUNDLE state: the shell setting it
    // does not make it true in here, and serverAttach then returns null on a console served by
    // that very server. Without this the app works only after the dashboard has persisted a
    // host to localStorage, which is the shape of bug that looks fine on the developer's machine.
    adoptServerOrigin();
    if (wantsDemo(params)) {
      loadDemo();
      return;
    }
    const serverHost = resolveServerHostOrRemembered(params);
    if (serverHost) {
      showConnectPrompt({ connection: "connecting", host: serverHost });
      void loadLive(serverHost);
      return;
    }
    loadGeneration++; // any load still out belongs to an address that no longer resolves
    showConnectPrompt({ connection: "none" });
  }

  refs.search.addEventListener("input", () => renderList());
  refs.search.addEventListener("keydown", (e) => {
    if (e.key !== "Escape" || !refs.search.value) return;
    e.stopPropagation();
    clearFilter();
  });
  refs.clear.addEventListener("click", clearFilter);
  refs.main.hidden = true;
  refs.bar.hidden = true;
  load();
  // A new address is followed only while no notes are on screen: an open note keeps the server it
  // came from until the tab is reopened.
  const unsubscribeHost = subscribeDefaultHost(() => {
    if (refs.main.hidden) load();
  });

  return {
    // Nothing to suppress: the store is read on mount and filtered by the reader, and it writes no
    // part of the shared status bar. The hook is here so the answer is already in place the day it
    // grows one.
    setVisible(): void {},
    deactivate(): void {
      stale = true;
      resizeObserver?.disconnect();
      unsubscribeHost();
    },
  };
}
