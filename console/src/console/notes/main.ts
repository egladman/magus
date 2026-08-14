// main.ts - the console's Notes surface: the workspace's human-authored notes, both the
// shared store in the checkout and the private one on this machine, separated by scope so a
// reader never has to guess who else can see a note.
//
// READ ONLY, and that is the feature rather than a limitation. A note is the one node class
// the knowledge graph does not derive from the workspace: nothing in the repository
// corroborates it later, so its only provenance is the person who wrote it. A browser edit
// would put an unattributable author on that store, so the way in stays `magus notes edit` -
// an editor, and a commit under the author's name. This surface shows the path and the exact
// command instead of offering a text box.
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
import {
  NotesService,
  Scope,
  AnchorStatus,
  Staleness,
  type Note,
  type Anchor,
  type StoreStatus,
} from "../../gen/magus/notes/v1/notes_pb";
import {
  parseHash,
  daemonAttach,
  validateLoopbackHost,
  consumeLiveToken,
  createDaemonTransport,
} from "../../lib/daemon";
import { persisted } from "../../lib/persist";
import { h } from "../view";

// The SAME key the dashboard remembers its last daemon under, so opening Notes after
// connecting the dashboard resumes the same loopback host. Read-only here.
const daemonCell = persisted<string | null>("dashboard-daemon", null);

interface Refs {
  scroll: HTMLElement;
  body: HTMLElement;
  empty: HTMLElement;
  emptyTitle: HTMLElement;
  emptySub: HTMLElement;
}

// SCOPE_COPY names each store by its CONSEQUENCE rather than by its config key. "shared" and
// "private" are the words in magus.yaml, but what a reader needs at a glance is who ends up
// able to read the note, which is the only difference between the two.
const SCOPE_COPY: Record<number, { title: string; sub: string }> = {
  [Scope.SHARED]: {
    title: "Shared",
    sub: "Committed to this repository. Your team has these, and git records who wrote each one.",
  },
  [Scope.PRIVATE]: {
    title: "Private",
    sub: "This machine only. Nothing attributes these and no clone has them.",
  },
};

// ANCHOR_COPY maps a status to its label and PF Label colour. UNVERIFIED is deliberately not
// green: it means nothing was checked, which is a different answer from "fine", and colouring
// it like a pass would tell a reader their notes were verified when no verification ran.
const ANCHOR_COPY: Record<number, { label: string; modifier: string }> = {
  [AnchorStatus.RESOLVES]: { label: "resolves", modifier: "pf-m-green" },
  [AnchorStatus.DANGLING]: { label: "dangling", modifier: "pf-m-red" },
  [AnchorStatus.DRIFTED]: { label: "drifted", modifier: "pf-m-orange" },
  [AnchorStatus.UNVERIFIED]: { label: "unverified", modifier: "pf-m-grey" },
};

const ANCHOR_KIND_NAME: Record<number, string> = {
  1: "symbol",
  2: "file",
  3: "project",
  4: "target",
  5: "note",
};

// label builds a PF Label. Text goes through textContent by construction (h sets text, never
// innerHTML), which is what keeps note prose and note titles from being trusted markup.
function label(text: string, modifier?: string, title?: string): HTMLElement {
  const el = h("span", "pf-v6-c-label" + (modifier ? " " + modifier : ""));
  const content = h("span", "pf-v6-c-label__content", text);
  el.append(content);
  if (title) el.title = title;
  return el;
}

// stalenessLabel renders the divergence, with the raw day count as the evidence. A note is
// only marked when magus measured it: UNMEASURED renders nothing at all rather than a
// reassuring badge, because absence of evidence is not evidence of freshness.
function stalenessLabel(n: Note): HTMLElement | null {
  switch (n.staleness) {
    case Staleness.OUTRUN:
      return label(
        n.outrunDays + " days behind",
        "pf-m-orange",
        "The code this note describes changed " + n.outrunDays + " days after the note last did.",
      );
    case Staleness.PETRIFIED:
      return label(
        n.outrunDays + " days behind",
        "pf-m-red",
        "The code this note describes has been moving for " +
          n.outrunDays +
          " days without it. Re-read it before trusting it.",
      );
    default:
      return null;
  }
}

// buildScaffold assembles the surface on PatternFly: a scrolling panel of store sections over
// a PF EmptyState for the cold case. It authors no new CSS - every class here is either PF's
// or one the shared render model already owns.
function buildScaffold(host: HTMLElement): Refs {
  const panel = h("section", "console-render-panel");
  const scroll = h("div", "console-render-scroll");
  const body = h("div", "console-render-body");

  const empty = h("div", "pf-v6-c-empty-state console-render-empty");
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
  emptyActions.append(wayLive);

  emptyBody.append(emptySub, emptyActions);
  emptyContent.append(emptyTitle, emptyBody);
  empty.append(emptyContent);

  scroll.append(body, empty);
  panel.append(scroll);
  host.append(panel);
  return { scroll, body, empty, emptyTitle, emptySub };
}

// buildAnchors renders what a note is ABOUT, with each anchor's verdict beside it. An anchor
// that resolves carries its node id in the title so a reader can carry it to the Graph
// Explorer; a dangling or drifted one carries the explanation instead of a repair button,
// because nothing here re-points an anchor at a guess.
function buildAnchors(anchors: Anchor[]): HTMLElement {
  const list = h("div", "pf-v6-c-label-group");
  const main = h("div", "pf-v6-c-label-group__main");
  for (const a of anchors) {
    const copy = ANCHOR_COPY[a.status] ?? ANCHOR_COPY[AnchorStatus.UNVERIFIED];
    const kind = ANCHOR_KIND_NAME[a.kind] ?? "anchor";
    const item = h("div", "pf-v6-c-label-group__list-item");
    item.append(label(kind + ":" + a.target, undefined, a.nodeId || undefined));
    item.append(label(copy.label, copy.modifier, a.detail || undefined));
    main.append(item);
  }
  list.append(main);
  return list;
}

// buildNoteCard renders one note as a PF Card. The body is fetched lazily on expand: prose is
// the largest field and a reader is scanning titles first.
function buildNoteCard(n: Note, loadBody: (n: Note) => Promise<string>): HTMLElement {
  const card = h("article", "pf-v6-c-card pf-m-compact");

  const header = h("div", "pf-v6-c-card__header");
  const titleWrap = h("div", "pf-v6-c-card__header-main");
  const title = h("h3", "pf-v6-c-card__title-text", n.title || n.name);
  titleWrap.append(title);
  header.append(titleWrap);

  const badges = h("div", "pf-v6-c-card__actions");
  const stale = stalenessLabel(n);
  if (stale) badges.append(stale);
  for (const tag of n.tags) badges.append(label(tag, "pf-m-blue"));
  header.append(badges);
  card.append(header);

  const bodyBox = h("div", "pf-v6-c-card__body");
  bodyBox.append(buildAnchors(n.anchors));

  // The path and the command, rather than an edit box. This is where a reader goes to change
  // a note, and naming the command is the whole affordance: the write path is a person in an
  // editor, and the console showing one would be the thing this store exists to prevent.
  const where = h("p", "pf-v6-c-card__body");
  where.append(h("code", undefined, n.path));
  bodyBox.append(where);

  const prose = h("div");
  prose.hidden = true;
  bodyBox.append(prose);

  const footer = h("div", "pf-v6-c-card__footer");
  const readBtn = h("button", "pf-v6-c-button pf-m-link pf-m-inline") as HTMLButtonElement;
  readBtn.type = "button";
  readBtn.append(h("span", "pf-v6-c-button__text", "Read"));
  let loaded = false;
  readBtn.addEventListener("click", () => {
    if (!loaded) {
      loaded = true;
      void loadBody(n).then((text) => {
        // textContent, never innerHTML: this is prose out of a file on disk, and the surface
        // that renders it must not become a way to run markup someone pasted into a note.
        const pre = h("pre");
        pre.textContent = text;
        prose.replaceChildren(pre);
      });
    }
    prose.hidden = !prose.hidden;
    readBtn.replaceChildren(h("span", "pf-v6-c-button__text", prose.hidden ? "Read" : "Hide"));
  });
  footer.append(readBtn);

  const editHint = h("code", undefined, "magus notes edit " + n.name);
  editHint.title = "Notes are written by a person, in an editor, and committed under their name.";
  footer.append(editHint);

  card.append(bodyBox, footer);
  return card;
}

// buildStoreSection renders one store, including the case where it holds nothing. A store that
// is declared and empty and a store that is not declared at all are different facts, and a
// blank area would say the first when it means the second.
function buildStoreSection(
  store: StoreStatus,
  notes: Note[],
  loadBody: (n: Note) => Promise<string>,
): HTMLElement {
  const copy = SCOPE_COPY[store.scope] ?? { title: "Notes", sub: "" };
  const section = h("section", "console-render-section");

  const head = h("div", "console-render-section__head");
  head.append(h("h2", undefined, copy.title));
  head.append(label(notes.length + (notes.length === 1 ? " note" : " notes"), "pf-m-grey"));
  section.append(head);

  const sub = h("p");
  sub.textContent = copy.sub;
  section.append(sub);

  if (!store.declared) {
    const none = h("p");
    none.textContent =
      "This workspace declares no " +
      (store.scope === Scope.PRIVATE ? "private" : "shared") +
      " notes store. Set knowledge.notes." +
      (store.scope === Scope.PRIVATE ? "private" : "shared") +
      " in magus.yaml to enable one.";
    section.append(none);
    return section;
  }

  for (const issue of store.issues) {
    const warn = h("div", "pf-v6-c-alert pf-m-warning pf-m-inline");
    warn.append(h("p", "pf-v6-c-alert__title", issue));
    section.append(warn);
  }

  if (notes.length === 0) {
    const none = h("p");
    none.textContent =
      "Nothing here yet. `magus notes edit <name>` opens your editor and writes the first one.";
    section.append(none);
    return section;
  }

  const gallery = h("div", "pf-v6-l-gallery pf-m-gutter");
  for (const n of notes) gallery.append(buildNoteCard(n, loadBody));
  section.append(gallery);
  return section;
}

// activate builds the surface into host, loads once, and returns a teardown. Every async load
// checks `stale` before touching the DOM, so a load that resolves after the tab closed is
// dropped.
export function activate(host: HTMLElement): () => void {
  const refs = buildScaffold(host);
  let stale = false;

  function showEmpty(title: string, sub: string): void {
    refs.body.replaceChildren();
    refs.empty.hidden = false;
    refs.emptyTitle.textContent = title;
    refs.emptySub.textContent = sub;
  }

  async function loadLive(daemonHost: string): Promise<void> {
    const client = createClient(NotesService, createDaemonTransport(daemonHost));
    try {
      const resp = await client.listNotes({});
      if (stale) return;
      const loadBody = async (n: Note): Promise<string> => {
        const one = await client.getNote({ name: n.name, scope: n.scope });
        return one.note?.body ?? "";
      };
      refs.body.replaceChildren();
      for (const store of resp.stores) {
        const mine = resp.notes.filter((n) => n.scope === store.scope);
        refs.body.append(buildStoreSection(store, mine, loadBody));
      }
      refs.empty.hidden = true;
    } catch (e) {
      if (stale) return;
      const msg = e instanceof Error ? e.message : String(e);
      showEmpty(
        "Could not reach the daemon",
        "The daemon at " + daemonHost + " did not answer (" + msg + ").",
      );
    }
  }

  // load resolves which daemon to read: an explicit attach (a #port link or the
  // daemon-origin console), then the last daemon the dashboard remembered. There is no demo
  // arm, and its absence is deliberate: a synthesized note would be prose no person wrote,
  // rendered in the one surface whose entire claim is that a person wrote everything on it.
  function load(): void {
    const params = parseHash();
    consumeLiveToken(params);
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

  load();

  return () => {
    stale = true;
  };
}
