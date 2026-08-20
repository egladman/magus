// main.ts - the console's Notes surface: the workspace's human-authored notes, both the
// shared store in the checkout and the private one on this machine, separated by scope so a
// reader never has to guess who else can see a note.
//
// READ ONLY, and that is the feature rather than a limitation. A note is the one node class
// the knowledge graph does not derive from the workspace: nothing in the repository
// corroborates it later, so its only provenance is the person who wrote it. A browser edit
// would put an unattributable author on that store, so the way in stays `magus notes edit` -
// an editor, and a commit under the author's name. NotesService has no Update and no Delete to
// call even if this surface wanted one.
//
// Read-only makes the surface's whole job TRIAGE: which note do I need, is it still true, and
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
} from "../../gen/magus/notes/v1alpha1/notes_pb";
import {
  parseHash,
  wantsDemo,
  daemonAttach,
  adoptDaemonOrigin,
  validateLoopbackHost,
  consumeLiveToken,
  createDaemonTransport,
} from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";
import type { SurfaceInstance } from "../standalone";
import { demoNotes } from "./demo";
import { parseTranscript, type Transcript } from "./transcript";

// The SAME key the dashboard remembers its last daemon under, so opening Notes after
// connecting the dashboard resumes the same loopback host. Read-only here.
const daemonCell = persisted<string | null>("dashboard-daemon", null);

// SCOPE_COPY names each store by its CONSEQUENCE rather than by its config key. "shared" and
// "private" are the words in magus.yaml, but what a reader needs at a glance is who ends up
// able to read the note, which is the only difference between the two.
//
// `consequence` is short because it renders INSIDE the store heading rather than in a banner
// beneath it. A banner is a thing to scroll past; a sticky heading is on screen for as long as
// the notes it introduces are. `where` and the path stay in the detail pane, where a reader who
// wants the location is already looking.
const SCOPE_COPY: Record<number, { title: string; key: string; consequence: string }> = {
  [Scope.SHARED]: {
    title: "Shared",
    key: "shared",
    consequence: "committed, review sees it",
  },
  [Scope.PRIVATE]: {
    title: "Private",
    key: "private",
    consequence: "this machine only",
  },
};

// ANCHOR_COPY maps a status to its label. UNVERIFIED is deliberately not treated as healthy: it
// means nothing was checked, which is a different answer from "fine", and presenting it as a
// pass would tell a reader their notes were verified when no verification ran.
const ANCHOR_COPY: Record<number, { label: string; slug: string }> = {
  [AnchorStatus.RESOLVES]: { label: "resolves", slug: "resolves" },
  [AnchorStatus.DANGLING]: { label: "dangling", slug: "dangling" },
  [AnchorStatus.DRIFTED]: { label: "drifted", slug: "drifted" },
  [AnchorStatus.UNVERIFIED]: { label: "unverified", slug: "unverified" },
};

const ANCHOR_KIND_NAME: Record<number, string> = {
  1: "symbol",
  2: "file",
  3: "project",
  4: "target",
  5: "note",
};

type ScopeFilter = "all" | "shared" | "private";

interface Refs {
  panel: HTMLElement;
  demoSlot: HTMLElement;
  main: HTMLElement;
  search: HTMLInputElement;
  scopeGroup: HTMLElement;
  list: HTMLElement;
  detail: HTMLElement;
  detailScope: HTMLElement;
  detailBody: HTMLElement;
  empty: HTMLElement;
  emptyTitle: HTMLElement;
  emptySub: HTMLElement;
}

// tsMillis converts a protobuf Timestamp to epoch milliseconds, or null when absent. A note
// whose store could not stat it has no modify time, and inventing one would put a freshness
// claim on the surface that nothing measured.
function tsMillis(t: Timestamp | undefined): number | null {
  if (!t) return null;
  return Number(t.seconds) * 1000 + Math.floor(t.nanos / 1e6);
}

// age renders an elapsed span at one significant unit: a reader scanning the column wants the
// order of magnitude, and "412 days" costs three characters to say what "1y" says.
function age(ms: number): string {
  const days = Math.floor((Date.now() - ms) / 86400000);
  if (days <= 0) return "today";
  if (days < 31) return days + "d";
  if (days < 365) return Math.floor(days / 30) + "mo";
  return Math.floor(days / 365) + "y";
}

// edited phrases the same span as a sentence. Separate from age because the column wants a
// token and the sentence wants grammar: "today" is already a complete answer, and running it
// through the "edited X ago" frame produces "edited today ago".
function edited(ms: number): string {
  const span = age(ms);
  return span === "today" ? "edited today" : "edited " + span + " ago";
}

// worstAnchor reports the anchor verdict a row should be marked by. Dangling outranks drifted
// because the subject is gone rather than merely changed, and both outrank an unverified
// anchor, which is an absence of evidence rather than a finding.
function worstAnchor(n: Note): { slug: string; count: number } | null {
  for (const status of [AnchorStatus.DANGLING, AnchorStatus.DRIFTED, AnchorStatus.UNVERIFIED]) {
    const hits = n.anchors.filter((a) => a.status === status);
    if (hits.length > 0) {
      const copy = ANCHOR_COPY[status];
      if (copy) return { slug: copy.slug, count: hits.length };
    }
  }
  return null;
}

// stalenessSlug names the divergence tier, or null when magus measured none. UNMEASURED renders
// nothing at all rather than a reassuring badge: absence of evidence is not evidence of
// freshness.
function stalenessSlug(n: Note): string | null {
  if (n.staleness === Staleness.OUTRUN) return "outrun";
  if (n.staleness === Staleness.PETRIFIED) return "petrified";
  return null;
}

// button builds a PF Button. Text goes through textContent by construction (h sets text, never
// innerHTML), which is what keeps note prose and note titles from being trusted markup.
function button(label: string, modifiers: string): HTMLButtonElement {
  const b = h("button", "pf-v6-c-button " + modifiers) as HTMLButtonElement;
  b.type = "button";
  b.append(h("span", "pf-v6-c-button__text", label));
  return b;
}

// alert builds a PF Alert, icon included. The icon is not decoration: PF lays the component out
// as a grid with a slot for it, and an alert built without one collapses into something a reader
// scrolls past like body text.
function alert(variant: string, title: string, body?: string): HTMLElement {
  const el = h("div", "pf-v6-c-alert " + variant);
  const icon = h("div", "pf-v6-c-alert__icon");
  icon.setAttribute("aria-hidden", "true");
  icon.textContent = variant.includes("warning") ? "!" : "i";
  el.append(icon);
  el.append(h("p", "pf-v6-c-alert__title", title));
  if (body) {
    const desc = h("div", "pf-v6-c-alert__description");
    desc.append(h("p", undefined, body));
    el.append(desc);
  }
  return el;
}

// copyRow renders a value beside a button that copies it. The console cannot run a command for
// the reader - and on a phone nothing can - so copying it is the whole of the affordance.
function copyRow(value: string, what: string): HTMLElement {
  const row = h("div", "console-notes-app__copy");
  row.append(h("code", undefined, value));
  const btn = button("copy", "pf-m-secondary");
  btn.setAttribute("aria-label", "Copy " + what);
  btn.addEventListener("click", () => {
    const text = btn.querySelector(".pf-v6-c-button__text");
    if (!text) return;
    const settle = (word: string): void => {
      text.textContent = word;
      setTimeout(() => {
        text.textContent = "copy";
      }, 1200);
    };
    if (!navigator.clipboard?.writeText) {
      settle("failed");
      return;
    }
    navigator.clipboard.writeText(value).then(
      () => settle("copied"),
      () => settle("failed"),
    );
  });
  row.append(btn);
  return row;
}

// buildScaffold assembles the surface: a filtered list beside a reading pane, over a PF
// EmptyState for the cold case. The panel root keeps its own class rather than the log viewer's
// `.console-render-panel`, and console.css's fill chain names it alongside the other surface
// roots.
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "console-notes-app");

  const demoSlot = h("div", "console-notes-app__demo");
  demoSlot.hidden = true;

  const main = h("div", "console-notes-app__main");
  const pane = h("div", "console-notes-app__pane");

  const bar = h("div", "console-notes-app__bar");
  const search = h(
    "input",
    "pf-v6-c-form-control__text console-notes-app__search",
  ) as HTMLInputElement;
  search.type = "search";
  // Title, tag and anchor, NOT body: ListNotes leaves the prose empty by contract, so a filter
  // claiming to search it would silently miss every note the reader has not opened.
  search.placeholder = "Filter title, tag or anchor";
  search.setAttribute("aria-label", "Filter notes");
  const scopeGroup = h("div", "pf-v6-c-toggle-group console-notes-app__scope");
  scopeGroup.setAttribute("role", "group");
  scopeGroup.setAttribute("aria-label", "Scope");
  bar.append(search, scopeGroup);

  const list = h("div", "console-notes-app__list");
  list.setAttribute("role", "list");
  pane.append(bar, list);

  const detail = h("div", "console-notes-app__detail");
  const detailBar = h("div", "console-notes-app__detail-bar");
  const back = button("Back to notes", "pf-m-link pf-m-inline console-notes-app__detail-back");
  const detailScope = h("span", "console-notes-app__detail-scope");
  detailBar.append(back, detailScope);
  const detailBody = h("div", "console-notes-app__detail-body");
  detail.append(detailBar, detailBody);
  back.addEventListener("click", () => {
    detail.removeAttribute("data-open");
  });

  main.append(pane, detail);

  const empty = h("div", "pf-v6-c-empty-state console-notes-app__empty");
  const emptyContent = h("div", "pf-v6-c-empty-state__content");
  const emptyTitle = h("h1", "pf-v6-c-empty-state__title-text", "No daemon connected");
  const emptyBody = h("div", "pf-v6-c-empty-state__body");
  const emptySub = h("p");
  emptySub.textContent =
    "Notes are prose a person wrote about this workspace, anchored to what it is about.";
  const emptyActions = h("div", "pf-v6-c-empty-state__actions");
  emptyActions.dataset.emptyWays = "";

  const wayLive = h("div");
  wayLive.dataset.emptyWay = "";
  const liveLabel = h("span", undefined, "Connect a daemon");
  liveLabel.dataset.emptyWayLabel = "";
  const liveCmd = h("pre");
  liveCmd.dataset.emptyCmd = "";
  liveCmd.append(h("code", undefined, "magus server start"));
  const liveHint = h("span", undefined, "Then open the live link it prints.");
  liveHint.dataset.emptyHint = "";
  wayLive.append(liveLabel, liveCmd, liveHint);

  // The hint says "sample" here, and the banner says it again above the list. Twice on purpose:
  // this is the surface where mistaking invented prose for something a colleague wrote is the
  // costly error, and one notice is one thing to miss.
  //
  // No button, matching every other surface - the Workspace menu is the one way in.
  const wayDemo = h("div");
  wayDemo.dataset.emptyWay = "";
  const demoLabel = h("span", undefined, "Try the demo");
  demoLabel.dataset.emptyWayLabel = "";
  const demoHint = h(
    "span",
    undefined,
    "Pick Demo data from the Workspace menu. Sample notes, no daemon needed.",
  );
  demoHint.dataset.emptyHint = "";
  wayDemo.append(demoLabel, demoHint);

  emptyActions.append(wayLive, wayDemo);
  emptyBody.append(emptySub, emptyActions);
  emptyContent.append(emptyTitle, emptyBody);
  empty.append(emptyContent);

  panel.append(demoSlot, main, empty);
  host.append(panel);
  return {
    panel,
    demoSlot,
    main,
    search,
    scopeGroup,
    list,
    detail,
    detailScope,
    detailBody,
    empty,
    emptyTitle,
    emptySub,
  };
}

// activate builds the surface into host, loads once, and returns a teardown. Every async load
// checks `stale` before touching the DOM, so a load that resolves after the tab closed is
// dropped.
//
// Returns the console's surface shape (page.ts): a teardown plus setVisible, so the shell can tell
// this pane when it stops being the visible one. Every surface hands back this shape rather than a
// bare teardown - a surface with nowhere to put the hook is how the log viewer came to write a
// backgrounded tab's status bar.
export function activate(host: HTMLElement): SurfaceInstance {
  const refs = buildScaffold(host);
  let stale = false;

  let notes: Note[] = [];
  let stores: StoreStatus[] = [];
  let scopeFilter: ScopeFilter = "all";
  let selected: string | null = null;
  let loadBody: (n: Note) => Promise<string> = () => Promise.resolve("");

  function showEmpty(title: string, sub: string): void {
    notes = [];
    stores = [];
    selected = null;
    refs.list.replaceChildren();
    refs.detail.removeAttribute("data-open");
    refs.detailBody.replaceChildren();
    refs.main.hidden = true;
    refs.demoSlot.hidden = true;
    refs.empty.hidden = false;
    refs.emptyTitle.textContent = title;
    refs.emptySub.textContent = sub;
  }

  // The store is part of a note's resource name ("shared/x", "private/x") rather than a
  // second request field, so a name and a scope can never arrive disagreeing.
  const noteResourceName = (n: Note): string =>
    (n.scope === Scope.PRIVATE ? "private/" : "shared/") + n.name;

  function noteFor(name: string): Note | undefined {
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

  // Most recently edited first. The wire order is the store's scan order, which is an
  // implementation detail of the filesystem walk and means nothing to a reader; modify time is
  // the one ordering the note files themselves carry. Notes the store could not stat sort last
  // rather than to the top, so a missing timestamp cannot masquerade as the freshest thing here.
  function byRecency(a: Note, b: Note): number {
    const am = tsMillis(a.modifyTime);
    const bm = tsMillis(b.modifyTime);
    if (am === null && bm === null) return a.title.localeCompare(b.title);
    if (am === null) return 1;
    if (bm === null) return -1;
    return bm - am;
  }

  function renderScopeGroup(): void {
    refs.scopeGroup.replaceChildren();
    const total = stores.reduce((sum, s) => sum + s.noteCount, 0);
    const options: { id: ScopeFilter; label: string; count: number }[] = [
      { id: "all", label: "All", count: total },
    ];
    for (const store of stores) {
      const copy = SCOPE_COPY[store.scope];
      if (!copy || !store.declared) continue;
      options.push({
        id: copy.key as ScopeFilter,
        label: copy.title,
        count: store.noteCount,
      });
    }
    // One store makes the filter a control with nothing to choose between.
    if (options.length < 3) return;
    for (const opt of options) {
      const item = h("div", "pf-v6-c-toggle-group__item");
      const btn = h(
        "button",
        "pf-v6-c-toggle-group__button" + (scopeFilter === opt.id ? " pf-m-selected" : ""),
      ) as HTMLButtonElement;
      btn.type = "button";
      btn.setAttribute("aria-pressed", String(scopeFilter === opt.id));
      btn.append(h("span", "pf-v6-c-toggle-group__text", opt.label + " " + opt.count));
      btn.addEventListener("click", () => {
        scopeFilter = opt.id;
        renderScopeGroup();
        renderList();
      });
      item.append(btn);
      refs.scopeGroup.append(item);
    }
  }

  function buildRow(n: Note): HTMLElement {
    const row = h("button", "console-notes-app__note") as HTMLButtonElement;
    row.type = "button";
    row.dataset.name = n.name;
    const broken = worstAnchor(n);
    if (broken) row.dataset.health = broken.slug;
    if (selected === n.name) row.setAttribute("aria-current", "true");

    const top = h("div", "console-notes-app__note-top");
    // Marked in the LIST, not only once a reader opens it. A capture is quoted material that
    // nobody stands behind, and a reader who learns that after reading it has already taken it
    // for a colleague's reasoning - which is the single thing this mark exists to prevent.
    if (n.source) {
      const mark = h("span", "console-notes-app__quoted", "quoted");
      mark.title = "A transcript captured from a " + n.source.kind + ", not prose someone wrote";
      top.append(mark);
    }
    top.append(h("span", "console-notes-app__note-title", n.title || n.name));
    const ms = tsMillis(n.modifyTime);
    if (ms !== null) {
      const ageEl = h("span", "console-notes-app__note-age", age(ms));
      // The age column means ONE thing on every row: when the file was last edited. Staleness
      // is a different quantity (how far the subject ran ahead of the prose) and shares the
      // meta line below with the rest of the evidence rather than this column, so a reader
      // never has to work out which of two spans a number is.
      ageEl.title = "Last edited";
      top.append(ageEl);
    }
    row.append(top);

    const meta = h("div", "console-notes-app__note-meta");
    const slug = stalenessSlug(n);
    if (slug) {
      const el = h("span", undefined, n.outrunDays + "d behind");
      el.dataset.staleness = slug;
      meta.append(el);
    }
    if (broken) {
      const el = h("span", undefined, broken.count + " " + broken.slug);
      el.dataset.health = broken.slug;
      meta.append(el);
    }
    if (n.tags.length > 0) meta.append(h("span", undefined, n.tags.join(" ")));
    if (meta.childElementCount > 0) row.append(meta);

    row.addEventListener("click", () => openNote(n));
    row.addEventListener("keydown", (e) => {
      if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
      e.preventDefault();
      const rows = Array.from(
        refs.list.querySelectorAll<HTMLButtonElement>(".console-notes-app__note"),
      );
      const next = rows[rows.indexOf(row) + (e.key === "ArrowDown" ? 1 : -1)];
      next?.focus();
    });
    return row;
  }

  function renderList(): void {
    const term = refs.search.value.trim().toLowerCase();
    refs.list.replaceChildren();
    let shown = 0;

    for (const store of stores) {
      const copy = SCOPE_COPY[store.scope];
      if (!copy) continue;
      if (scopeFilter !== "all" && scopeFilter !== copy.key) continue;

      const head = h("div", "console-notes-app__store");
      head.dataset.scope = copy.key;
      const mine = notes.filter((n) => n.scope === store.scope && matches(n, term)).sort(byRecency);
      head.append(h("span", undefined, copy.title + " " + mine.length));
      // A store that is declared and empty and a store that is not declared at all are
      // different facts, and a blank area would say the first when it means the second.
      head.append(
        h(
          "span",
          "console-notes-app__store-consequence",
          store.declared ? copy.consequence : "not declared",
        ),
      );
      refs.list.append(head);

      if (!store.declared) {
        refs.list.append(
          h(
            "p",
            "console-notes-app__note-none",
            "Set knowledge.notes." + copy.key + " in magus.yaml to enable this store.",
          ),
        );
        continue;
      }

      for (const issue of store.issues) {
        const warn = h("div", "console-notes-app__issue");
        warn.append(h("span", undefined, "!"), h("span", undefined, issue));
        refs.list.append(warn);
      }

      if (mine.length === 0) {
        refs.list.append(
          h(
            "p",
            "console-notes-app__note-none",
            term
              ? "No note here matches that filter."
              : "Nothing here yet. `magus notes edit <name>` opens your editor and writes the first one.",
          ),
        );
        continue;
      }
      for (const n of mine) refs.list.append(buildRow(n));
      shown += mine.length;
    }

    // The reading pane keeps whatever is open, but a filter that hides the open note leaves the
    // list and the pane disagreeing about what is selected.
    if (selected && !refs.list.querySelector('[data-name="' + CSS.escape(selected) + '"]')) {
      showBlank();
    }
    if (shown === 0 && term) refs.detail.removeAttribute("data-open");
  }

  function showBlank(): void {
    selected = null;
    refs.detail.removeAttribute("data-open");
    refs.detailScope.textContent = "";
    refs.detailScope.removeAttribute("data-scope");
    refs.detailBody.replaceChildren(
      h("div", "console-notes-app__blank", "Select a note to read it."),
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
        wrap.append(h("div", "console-notes-app__thread-file", where));
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
      if (e.resolved) box.append(h("span", "console-notes-app__entry-resolved", "resolved"));

      // textContent, as everywhere else a note's prose is rendered: this is quoted material
      // out of a file on disk and must never become a way to run markup someone pasted in.
      box.append(h("p", "console-notes-app__entry-body", e.body));
      wrap.append(box);
    }
    return wrap;
  }

  function buildAnchor(a: Anchor): HTMLElement {
    const copy = ANCHOR_COPY[a.status] ?? ANCHOR_COPY[AnchorStatus.UNVERIFIED];
    const row = h("div", "console-notes-app__anchor");
    const dot = h("span", "console-notes-app__anchor-status");
    if (copy) dot.dataset.status = copy.slug;
    dot.setAttribute("role", "img");
    dot.setAttribute("aria-label", copy?.label ?? "unverified");
    row.append(dot);

    const target = h("span", "console-notes-app__anchor-target");
    target.append(document.createTextNode((ANCHOR_KIND_NAME[a.kind] ?? "anchor") + " " + a.target));
    if (a.detail) target.append(h("small", "console-notes-app__anchor-detail", a.detail));
    // The node id is the handle a reader carries to the Graph Explorer by hand. It is text and
    // not a link because cross-surface navigation carries a pageId and nothing else today, so a
    // link would have to invent a contract this change has no business inventing.
    if (a.nodeId) target.append(h("small", "console-notes-app__anchor-detail", a.nodeId));
    row.append(target);
    return row;
  }

  function renderNote(n: Note, body: string): void {
    const copy = SCOPE_COPY[n.scope];
    refs.detailScope.textContent = copy ? copy.title + " - " + copy.consequence : "";
    if (copy) refs.detailScope.dataset.scope = copy.key;

    const read = h("div", "console-notes-app__read");
    read.append(h("h2", "console-notes-app__title", n.title || n.name));

    const sub = h("div", "console-notes-app__subtitle");
    const ms = tsMillis(n.modifyTime);
    if (ms !== null) sub.append(h("span", undefined, edited(ms)));
    const slug = stalenessSlug(n);
    if (slug) {
      const el = h(
        "span",
        undefined,
        n.outrunDays + " days behind its subject" + (slug === "petrified" ? ", re-read it" : ""),
      );
      el.dataset.staleness = slug;
      sub.append(el);
    }
    if (n.tags.length > 0) sub.append(h("span", undefined, n.tags.join(" ")));
    if (sub.childElementCount > 0) read.append(sub);

    // A capture renders as its entries where the body reads back as one, and verbatim where it
    // does not. Losing the boxes is cosmetic; losing the transcript would not be.
    const transcript = n.source ? parseTranscript(n.source.kind, body) : null;
    if (transcript) {
      read.append(h("p", "console-notes-app__prose", transcript.preamble));
      read.append(buildTranscript(transcript));
    } else {
      // textContent, never innerHTML: this is prose out of a file on disk, and the surface that
      // renders it must not become a way to run markup someone pasted into a note.
      read.append(h("p", "console-notes-app__prose", body));
    }

    const facts = h("div", "console-notes-app__facts");
    if (n.anchors.length > 0) {
      facts.append(h("div", "console-notes-app__facts-head", "Anchored to"));
      for (const a of n.anchors) facts.append(buildAnchor(a));
    }
    facts.append(h("div", "console-notes-app__facts-head", "File"));
    facts.append(copyRow(n.path, "path"));
    // The path and the command, rather than an edit box. This is where a reader goes to change
    // a note, and naming the command is the whole affordance: the write path is a person in an
    // editor, and the console showing a text box would be the thing this store exists to prevent.
    facts.append(h("div", "console-notes-app__facts-head", "Edit it"));
    facts.append(copyRow("magus notes edit " + n.name, "edit command"));

    refs.detailBody.replaceChildren(read, facts);
    refs.detailBody.scrollTop = 0;
  }

  function openNote(n: Note): void {
    selected = n.name;
    for (const row of refs.list.querySelectorAll<HTMLElement>(".console-notes-app__note")) {
      if (row.dataset.name === n.name) row.setAttribute("aria-current", "true");
      else row.removeAttribute("aria-current");
    }
    refs.detail.dataset.open = "";
    renderNote(n, "");
    void loadBody(n).then((body) => {
      // A second click before the first body lands must not overwrite the note now open.
      if (stale || selected !== n.name) return;
      const current = noteFor(n.name) ?? n;
      renderNote(current, body);
    });
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
    renderScopeGroup();
    renderList();
    showBlank();
  }

  async function loadLive(daemonHost: string): Promise<void> {
    const client = createClient(NotesService, createDaemonTransport(daemonHost));
    try {
      const resp = await client.listNotes({});
      if (stale) return;
      refs.demoSlot.hidden = true;
      refs.demoSlot.replaceChildren();
      show(resp.notes, resp.stores, async (n) => {
        const one = await client.getNote({ name: noteResourceName(n) });
        return one.body ?? "";
      });
    } catch (e) {
      if (stale) return;
      const msg = e instanceof Error ? e.message : String(e);
      showEmpty(
        "Could not reach the daemon",
        "The daemon at " + daemonHost + " did not answer (" + msg + ").",
      );
    }
  }

  // loadDemo renders invented notes, and the banner above them is not decoration: it is the only
  // thing between a demo and this surface asserting that a person wrote five things nobody wrote.
  // Every other surface can show sample data silently; this one cannot, because authorship is the
  // entire claim a note makes. Warning rather than info, because the reader who skims past an
  // info stripe and then quotes one of these to a colleague is the failure being prevented.
  function loadDemo(): void {
    const demo = demoNotes();
    refs.demoSlot.replaceChildren(
      alert(
        "pf-m-warning",
        "Sample notes. Nobody wrote these.",
        "Connect a daemon to read the ones people actually wrote here.",
      ),
    );
    refs.demoSlot.hidden = false;
    show(demo.notes, demo.stores, (n) => Promise.resolve(demo.body(n.name)));
  }

  // load resolves what to read: an explicit #demo, then an explicit daemon attach (a #port link
  // or the daemon-origin console), then the last daemon the dashboard remembered.
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
    // adoptDaemonOrigin, not just consumeLiveToken. Each surface is its own esbuild bundle, so
    // lib/daemon's "did we adopt this origin" flag is PER-BUNDLE state: the shell setting it
    // does not make it true in here, and daemonAttach then returns null on a console served by
    // that very daemon. Without this the surface works only after the dashboard has persisted a
    // host to localStorage, which is the shape of bug that looks fine on the developer's machine.
    adoptDaemonOrigin();
    if (wantsDemo(params)) {
      loadDemo();
      return;
    }
    const linked = daemonAttach(params);
    const remembered = daemonCell.get();
    const daemonHost = linked ?? (remembered ? validateLoopbackHost(remembered) : null);
    if (daemonHost) {
      void loadLive(daemonHost);
      return;
    }
    showEmpty(
      "No daemon connected",
      "Notes are prose a person wrote about this workspace, anchored to what it is about.",
    );
  }

  refs.search.addEventListener("input", () => renderList());
  refs.main.hidden = true;
  load();

  return {
    // Nothing to suppress: the store is read on mount and filtered by the reader, and it writes no
    // part of the shared status bar. The hook is here so the answer is already in place the day it
    // grows one.
    setVisible(): void {},
    deactivate(): void {
      stale = true;
    },
  };
}
