// The graph's question builder: compose a filter term by term, or run a view.
//
// Every term writes into the same input you could have typed, which is the point - it replaced
// chips that ran the right query and never showed it. Two tabs because a filter is a string in the
// query grammar and a view is a graph algorithm; no filter expresses "what breaks if I change this".

import { h } from "../../desktop/view";
import { canDetach, detachPanel, type DetachHandle } from "../../lib/detach";
import { reportFailure } from "../../lib/notifications";
import { inlineAlert } from "../../ui/alert";

const SOURCE = "Graph";
const SVG_NS = "http://www.w3.org/2000/svg";

// icon is a PF button icon slot holding one stroked glyph, so a control never falls back on a text
// character ("×") that a font may draw at any size or weight.
function icon(...paths: string[]): HTMLElement {
  const slot = h("span", "pf-v6-c-button__icon");
  const svg = document.createElementNS(SVG_NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("width", "1em");
  svg.setAttribute("height", "1em");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "2");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  for (const d of paths) {
    const path = document.createElementNS(SVG_NS, "path");
    path.setAttribute("d", d);
    svg.append(path);
  }
  slot.append(svg);
  return slot;
}

const CLOSE_GLYPH = "M6 6l12 12M18 6L6 18";
const DETACH_GLYPH = ["M15 3h6v6", "M10 14 21 3", "M21 14v7H3V3h7"];

// menuItem is one row of a PF Menu used as a static list of actions: a label, then a description line
// per entry in `lines`. A row that cannot be used stays focusable (aria-disabled, not disabled) so the
// reason it carries is reachable from the keyboard.
function menuItem(
  label: string,
  lines: Array<string | HTMLElement>,
  disabledReason?: string,
): {
  li: HTMLElement;
  button: HTMLButtonElement;
} {
  const li = h("li", "pf-v6-c-menu__list-item");
  const button = h("button", "pf-v6-c-menu__item");
  button.type = "button";
  const main = h("span", "pf-v6-c-menu__item-main");
  main.append(h("span", "pf-v6-c-menu__item-text", label));
  button.append(main);
  for (const line of lines) {
    const row = h("span", "pf-v6-c-menu__item-description");
    row.append(line);
    button.append(row);
  }
  if (disabledReason) {
    li.classList.add("pf-m-aria-disabled");
    button.setAttribute("aria-disabled", "true");
  }
  li.append(button);
  return { li, button };
}

// menuList wraps rows in the PF Menu chrome.
function menuList(rows: HTMLElement[]): HTMLElement {
  const menu = h("div", "pf-v6-c-menu pf-m-plain");
  const content = h("div", "pf-v6-c-menu__content");
  const list = h("ul", "pf-v6-c-menu__list");
  list.append(...rows);
  content.append(list);
  menu.append(content);
  return menu;
}

export interface QueryBuilderDeps {
  // From the LOADED graph, so a picker never offers a value that matches nothing here.
  kinds: () => string[];
  relations: () => string[];
  projects: () => string[];
  currentQuery: () => string;
  applyQuery: (q: string) => void;
  matchCount: () => { matched: number; total: number } | null;
  runView: (view: string) => void;
  radialPick: () => void;
  onClose?: () => void;
  // Facts only. Which views they rule out is declared per view (ViewSpec.requires), so adding a view
  // forces a decision about its requirement instead of leaving it to a lambda somewhere else.
  capabilities: () => GraphCapabilities;
}

export interface GraphCapabilities {
  flavor: "targets" | "knowledge";
  hasDurations: boolean;
  hasAffectedSet: boolean;
  live: boolean;
  // Why the last FindAffected could not answer, empty when it did. An empty affected set has two
  // causes and only one of them is "your tree is clean".
  affectedFallback: string;
}

export interface QueryBuilder {
  el: HTMLElement;
  open(): void;
  close(): void;
  isOpen(): boolean;
}

// One list, three uses: the type, the picker's order, and the parser's accept set. Spelling the
// union separately let the picker and the type drift, which is a term the parser accepts and the
// picker cannot offer.
const FIELDS = ["", "kind", "project", "relation", "id"] as const;
type Field = (typeof FIELDS)[number];

function asField(s: string): Field | null {
  return (FIELDS as readonly string[]).includes(s) ? (s as Field) : null;
}

interface Term {
  field: Field;
  value: string;
  negated: boolean;
}

const FIELD_LABEL: Record<Field, string> = {
  "": "matches text",
  kind: "is a",
  project: "is in project",
  relation: "touches a",
  id: "has id containing",
};

// Quoting rule, matching parseQuery: a value with whitespace has to survive the round trip.
function renderTerm(t: Term): string {
  const v = /\s/.test(t.value) ? '"' + t.value + '"' : t.value;
  return (t.negated ? "-" : "") + (t.field ? t.field + ":" : "") + v;
}

export function renderQuery(terms: Term[]): string {
  return terms
    .filter((t) => t.value.trim() !== "")
    .map(renderTerm)
    .join(" ");
}

// Mirrors main.ts's parseQuery closely enough to round-trip what the builder itself writes; an
// unrecognized field falls back to free text, which is what the filter does with it too.
export function parseTerms(str: string): Term[] {
  const out: Term[] = [];
  let i = 0;
  while (i < str.length) {
    while (i < str.length && /\s/.test(str[i])) i++;
    if (i >= str.length) break;
    let negated = false;
    if (str[i] === "-") {
      negated = true;
      i++;
    }
    let field: Field = "";
    const fm = /^([a-zA-Z]+):/.exec(str.slice(i));
    // An unknown prefix is not a field: it stays part of the value, so a filter magus understands
    // and this builder does not still round-trips instead of being silently rewritten.
    const named = fm && fm[1].toLowerCase() !== "" ? asField(fm[1].toLowerCase()) : null;
    if (fm && named) {
      field = named;
      i += fm[0].length;
    }
    let value: string;
    if (str[i] === '"') {
      const end = str.indexOf('"', i + 1);
      value = end < 0 ? str.slice(i + 1) : str.slice(i + 1, end);
      i = end < 0 ? str.length : end + 1;
    } else {
      let j = i;
      while (j < str.length && !/\s/.test(str[j])) j++;
      value = str.slice(i, j);
      i = j;
    }
    if (value !== "") out.push({ field, value, negated });
  }
  return out;
}

// Worked examples, easy to hard. Each loads into the term rows, so the next click is an EDIT - the
// last three teach what nobody guesses: terms AND, a leading hyphen subtracts, and they compose.
interface Example {
  label: string;
  query: string;
  note: string;
}

const EXAMPLES: Example[] = [
  { label: "Every target", query: "kind:target", note: "One kind, nothing else." },
  {
    label: "One project's code",
    query: "project:console kind:function",
    note: "Two terms AND together. Both must match.",
  },
  {
    label: "Documentation",
    query: "kind:doc",
    note: "Prose nodes: markdown, module docs, rationale.",
  },
  {
    label: "Anything a spell touches",
    query: "relation:uses",
    note: "Matches by the EDGES a node carries, not the node.",
  },
  {
    label: "Targets that are not generators",
    query: "kind:target -id:generate",
    note: "A leading hyphen subtracts. This is the one people never guess.",
  },
  {
    label: "Code outside the console",
    query: "kind:function -project:console",
    note: "Negation works on any field, not just id.",
  },
  {
    label: "Undocumented spells",
    query: "kind:spell -relation:documents",
    note: "Subtracting a RELATION finds absence: spells nothing documents.",
  },
];

interface ViewSpec {
  id: string;
  label: string;
  blurb: string;
  cli: string | null;
  picks: boolean; // arms a click on the canvas rather than answering immediately
  // What this view needs from the loaded graph, and what to say when it is missing. Absent means
  // the view always works. Returning a sentence is what disables it - so the requirement and the
  // explanation cannot drift apart, because they are the same expression.
  requires?: (c: GraphCapabilities) => string | null;
}

const VIEWS: ViewSpec[] = [
  {
    id: "blast",
    label: "What rebuilds if I change this?",
    blurb: "Everything that transitively depends on the target you pick.",
    cli: "magus explain <target>",
    picks: true,
  },
  {
    id: "trace",
    label: "Why does A depend on B?",
    blurb: "The shortest dependency path between two targets you pick.",
    cli: "magus path <a> <b>",
    picks: true,
  },
  {
    id: "radial",
    label: "What is next to this?",
    blurb: "One node's neighborhood, as rings by distance. Containment is not followed.",
    cli: null,
    picks: true,
  },
  {
    id: "hubs",
    label: "What does everything depend on?",
    blurb: "The most depended-on nodes, ranked. Containment does not count toward the rank.",
    cli: null,
    picks: false,
  },
  {
    id: "orphans",
    label: "What is dead?",
    blurb: "Nodes with no dependency either way, among the kinds that normally carry one.",
    cli: null,
    picks: false,
  },
  {
    id: "cycles",
    label: "Is anything circular?",
    blurb: "Targets caught in a dependency cycle, which magus cannot order.",
    cli: "magus describe graph",
    picks: false,
    // Only the target adapter marks a cycle; types.KnowledgeEdge has no such field, so on the
    // knowledge graph this view found nothing every time and reported the graph acyclic - a result
    // it had never checked.
    requires: (c) =>
      c.flavor === "targets"
        ? null
        : "Switch to the target graph. The knowledge graph does not record cycles, so this would report none without looking.",
  },
  {
    id: "critical",
    label: "What is slow?",
    blurb: "The longest duration-weighted chain of targets.",
    cli: "magus graph deps -o json",
    picks: false,
    requires: (c) =>
      c.hasDurations
        ? null
        : "This graph carries no timing. Run a build, then export again and the chain appears.",
  },
  {
    id: "affected",
    label: "What does my diff touch?",
    blurb: "The projects your working-tree changes reach.",
    cli: "magus affected ls",
    picks: false,
    // Live-with-a-clean-tree is a different answer from not-live, and saying "needs a live
    // workspace" to someone who HAS one is how a correct tool reads as broken.
    requires: (c) =>
      c.hasAffectedSet
        ? null
        : c.affectedFallback
          ? "The workspace could not compute a diff: " + c.affectedFallback
          : c.live
            ? "Nothing to show: your working tree matches the base it is compared against."
            : "Needs a live workspace. Open one with magus graph export --open --follow.",
  },
];

export function createQueryBuilder(deps: QueryBuilderDeps): QueryBuilder {
  let terms: Term[] = [];
  let tab: "filter" | "view" = "filter";
  // The filter as it stood when the panel opened, so Reset can put it back after experimenting.
  let openedWith = "";
  let detached: DetachHandle | null = null;
  let opening = false;

  const overlay = h("div", "console-graph-qb");
  overlay.hidden = true;
  overlay.setAttribute("role", "region");
  overlay.setAttribute("aria-label", "Ask the graph");

  const box = h("div", "console-graph-qb__box");
  const head = h("div", "console-graph-qb__head");
  head.append(h("span", "console-graph-qb__title", "Ask the graph"));
  // Detach puts the builder in its own window so it can sit beside the graph on another display -
  // the panel is MOVED, so it keeps running and keeps driving this graph.
  const detachBtn = h("button", "pf-v6-c-button pf-m-plain console-graph-qb__detach");
  detachBtn.type = "button";
  detachBtn.title = "Open in its own window";
  detachBtn.setAttribute("aria-label", "Open the builder in its own window");
  detachBtn.append(icon(...DETACH_GLYPH));
  detachBtn.hidden = !canDetach();
  detachBtn.addEventListener("click", () => {
    if (detached?.isOpen()) {
      detached.close();
      return;
    }
    // requestWindow is async, so a second click before it settles would move the panel into a second
    // window and strand the first one holding a placeholder that never comes back.
    if (opening) return;
    opening = true;
    // Not awaited before the call: both window APIs spend the click's user activation, and an await
    // in between loses it.
    void detachPanel(box, {
      title: "Ask the graph",
      width: 460,
      height: 700,
      onReturn: () => {
        detached = null;
      },
    }).then((res) => {
      opening = false;
      if (res.ok) {
        detached = res.handle;
        detachBtn.removeAttribute("data-detach-failed");
        detachBtn.title = "Put it back";
        clearNotice();
        return;
      }
      // A browser can refuse a second window. The reason is said where the reader is looking, in the
      // panel, and as a toast; a tooltip alone reaches nobody on touch and nobody who is not hovering.
      const message = "Could not open the builder in its own window: " + res.reason;
      detachBtn.title = message;
      detachBtn.setAttribute("data-detach-failed", "");
      showFailure(message, "graph:detach");
    });
  });

  const closeBtn = h("button", "pf-v6-c-button pf-m-plain console-graph-qb__close");
  closeBtn.type = "button";
  closeBtn.setAttribute("aria-label", "Close the builder");
  closeBtn.append(icon(CLOSE_GLYPH));
  closeBtn.addEventListener("click", () => close());
  head.append(detachBtn, closeBtn);

  // A failure here is a toast and this notice: the panel is where the reader is looking.
  const notice = h("div", "console-graph-qb__notice");
  notice.hidden = true;
  function clearNotice(): void {
    notice.replaceChildren();
    notice.hidden = true;
  }
  function showFailure(message: string, key: string): void {
    notice.replaceChildren(inlineAlert({ variant: "danger", title: message }));
    notice.hidden = false;
    reportFailure(SOURCE, message, key);
  }

  // Tabs: the WAI-ARIA tablist pattern on PF's tabs markup. The list is the tablist and each item
  // is presentational, so a screen reader counts two tabs and not two list items holding two tabs.
  // Roving tabindex plus arrow keys, Home and End; each tab owns the panel it controls.
  const tabs = h("div", "pf-v6-c-tabs console-graph-qb__tabs");
  const tabList = h("ul", "pf-v6-c-tabs__list");
  tabList.setAttribute("role", "tablist");
  tabList.setAttribute("aria-label", "Ask the graph");
  const TAB_IDS = ["filter", "view"] as const;
  const tabButtons = new Map<"filter" | "view", HTMLButtonElement>();
  const tabItems = new Map<"filter" | "view", HTMLElement>();
  for (const [id, label] of [
    ["filter", "Build a filter"],
    ["view", "Run a view"],
  ] as ["filter" | "view", string][]) {
    const li = h("li", "pf-v6-c-tabs__item");
    li.setAttribute("role", "presentation");
    const btn = h("button", "pf-v6-c-tabs__link");
    btn.type = "button";
    btn.id = "console-graph-qb-tab-" + id;
    btn.setAttribute("role", "tab");
    btn.setAttribute("aria-controls", "console-graph-qb-panel-" + id);
    btn.append(h("span", "pf-v6-c-tabs__item-text", label));
    btn.addEventListener("click", () => {
      tab = id;
      paint();
    });
    li.append(btn);
    tabList.append(li);
    tabButtons.set(id, btn);
    tabItems.set(id, li);
  }
  tabList.addEventListener("keydown", (e) => {
    const at = TAB_IDS.indexOf(tab);
    let next = -1;
    if (e.key === "ArrowRight") next = (at + 1) % TAB_IDS.length;
    else if (e.key === "ArrowLeft") next = (at - 1 + TAB_IDS.length) % TAB_IDS.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = TAB_IDS.length - 1;
    if (next < 0) return;
    e.preventDefault();
    tab = TAB_IDS[next];
    paint();
    tabButtons.get(tab)?.focus();
  });
  tabs.append(tabList);

  const body = h("div", "console-graph-qb__body");
  const filterPane = h("div", "console-graph-qb__pane");
  const viewPane = h("div", "console-graph-qb__pane");
  for (const [id, pane] of [
    ["filter", filterPane],
    ["view", viewPane],
  ] as const) {
    pane.id = "console-graph-qb-panel-" + id;
    pane.setAttribute("role", "tabpanel");
    pane.setAttribute("aria-labelledby", "console-graph-qb-tab-" + id);
    pane.tabIndex = 0;
  }
  body.append(filterPane, viewPane);

  // --- filter pane -----------------------------------------------------------------------------
  const rows = h("div", "console-graph-qb__rows");
  const addBtn = h("button", "pf-v6-c-button pf-m-secondary console-graph-qb__add");
  addBtn.type = "button";
  addBtn.append(h("span", "pf-v6-c-button__text", "Add a term"));
  addBtn.addEventListener("click", () => {
    terms.push({ field: "kind", value: "", negated: false });
    paintRows();
  });

  const previewLabel = h("p", "console-graph-qb__previewlabel", "Your filter");
  const preview = h("code", "console-graph-qb__preview");
  const cliLine = h("code", "console-graph-qb__cli");
  const countLine = h("p", "console-graph-qb__count");
  countLine.setAttribute("aria-live", "polite");
  // One copy control for the two things worth copying: the filter itself, and the command that runs
  // it in a terminal. The sidebar's own copy button does the command only.
  const copyRow = h("div", "console-graph-qb__copyrow");
  for (const [label, get] of [
    ["Copy filter", () => renderQuery(terms)],
    ["Copy command", () => 'magus query "' + renderQuery(terms) + '"'],
  ] as [string, () => string][]) {
    const b = h("button", "pf-v6-c-button pf-m-link pf-m-inline");
    b.type = "button";
    b.append(h("span", "pf-v6-c-button__text", label));
    b.addEventListener("click", () => {
      const text = get();
      if (!renderQuery(terms)) return;
      const span = b.querySelector(".pf-v6-c-button__text");
      // writeText rejects when the document is not focused or the permission is denied, and an
      // unhandled rejection left the button reading "Copied" with an empty clipboard.
      if (!navigator.clipboard) {
        showFailure("Could not copy: the clipboard is not available here.", "graph:qb-copy");
        return;
      }
      void navigator.clipboard
        .writeText(text)
        .then(() => {
          clearNotice();
          if (span) span.textContent = "Copied";
        })
        .catch((err: unknown) => {
          showFailure(
            "Could not copy the " +
              label.replace("Copy ", "") +
              ": " +
              (err instanceof Error ? err.message : String(err)) +
              ". Select it in the panel and copy it by hand.",
            "graph:qb-copy",
          );
        })
        .finally(() => {
          setTimeout(() => {
            if (span) span.textContent = label;
          }, 1200);
        });
    });
    copyRow.append(b);
  }
  const hint = h(
    "p",
    "console-graph-qb__hint",
    "Terms combine with AND. Apply puts this in the filter box, where you can keep editing it by hand.",
  );

  const exLabel = h("p", "console-graph-qb__previewlabel", "Start from an example");
  const exRows: HTMLElement[] = [];
  for (const ex of EXAMPLES) {
    const { li, button } = menuItem(ex.label, [
      h("code", "console-graph-qb__exquery", ex.query),
      ex.note,
    ]);
    button.addEventListener("click", () => {
      terms = parseTerms(ex.query);
      paintRows();
      commit(); // a discrete pick, same as any other - it applies and the canvas answers
    });
    exRows.push(li);
  }
  const exWrap = menuList(exRows);
  exWrap.classList.add("console-graph-qb__examples");

  filterPane.append(
    rows,
    addBtn,
    previewLabel,
    preview,
    countLine,
    cliLine,
    copyRow,
    hint,
    exLabel,
    exWrap,
  );

  function fieldValues(field: Field): string[] {
    if (field === "kind") return deps.kinds();
    if (field === "relation") return deps.relations();
    if (field === "project") return deps.projects();
    return [];
  }

  function paintRows(): void {
    rows.textContent = "";
    if (terms.length === 0) {
      rows.append(
        h(
          "p",
          "console-graph-qb__empty",
          "No terms yet. Add one to narrow the graph, or run a view from the other tab.",
        ),
      );
    }
    terms.forEach((t, idx) => {
      const row = h("div", "console-graph-qb__row");

      const neg = h("button", "pf-v6-c-button pf-m-control console-graph-qb__neg");
      neg.type = "button";
      // The word stays "not" and the pressed state carries the rest: a label that flipped between
      // "is" and "not" while aria-pressed flipped too would say the state twice and the name never
      // the same twice.
      neg.setAttribute("aria-pressed", t.negated ? "true" : "false");
      neg.setAttribute("aria-label", "not (term " + (idx + 1) + ")");
      neg.title = t.negated
        ? "Excluding these. Click to include"
        : "Including these. Click to exclude";
      neg.append(h("span", "pf-v6-c-button__text", "not"));
      neg.addEventListener("click", () => {
        t.negated = !t.negated;
        paintRows();
        commit();
      });

      const sel = h("span", "pf-v6-c-form-control console-graph-qb__field");
      const select = h("select");
      select.setAttribute("aria-label", "Field of term " + (idx + 1));
      for (const f of FIELDS) {
        const o = h("option");
        o.value = f;
        o.textContent = FIELD_LABEL[f];
        o.selected = f === t.field;
        select.append(o);
      }
      select.addEventListener("change", () => {
        t.field = asField(select.value) ?? "";
        t.value = "";
        paintRows();
        commit();
      });
      sel.append(select);

      const valWrap = h("span", "pf-v6-c-form-control console-graph-qb__value");
      const input = h("input");
      input.type = "text";
      input.value = t.value;
      input.setAttribute("aria-label", "Value of term " + (idx + 1));
      input.spellcheck = false;
      input.autocomplete = "off";
      const list = fieldValues(t.field);
      if (list.length) {
        const dl = h("datalist");
        dl.id = "qb-values-" + idx;
        for (const v of list) {
          const o = h("option");
          o.value = v;
          dl.append(o);
        }
        valWrap.append(dl);
        input.setAttribute("list", dl.id);
        input.placeholder = list.slice(0, 3).join(", ") + (list.length > 3 ? ", ..." : "");
      } else {
        input.placeholder = t.field === "id" ? "part of a node id" : "any text in a name or doc";
      }
      // Typing updates the preview but does NOT run the query - Enter or leaving the field does.
      // Datadog's split, and it is the right one: a discrete pick is cheap and unambiguous, while
      // a half-typed value would run a query for `spe` on the way to `spell`.
      input.addEventListener("input", () => {
        t.value = input.value;
        paintPreview();
      });
      input.addEventListener("change", () => commit());
      input.addEventListener("keydown", (ev) => {
        if (ev.key === "Enter") {
          ev.preventDefault();
          commit();
        }
      });
      valWrap.append(input);

      const del = h("button", "pf-v6-c-button pf-m-plain console-graph-qb__del");
      del.type = "button";
      del.setAttribute("aria-label", "Remove term " + (idx + 1));
      del.append(icon(CLOSE_GLYPH));
      del.addEventListener("click", () => {
        terms.splice(idx, 1);
        paintRows();
        commit();
      });

      row.append(neg, sel, valWrap, del);
      rows.append(row);
    });
    paintPreview();
  }

  function paintPreview(): void {
    const q = renderQuery(terms);
    preview.textContent = q || "(everything)";
    // The CLI echo is the point of the exercise as much as the filter is: the same string runs on
    // the command line, and seeing that is what makes the grammar worth learning.
    cliLine.textContent = q ? 'magus query "' + q + '"' : "";
    cliLine.hidden = !q;
  }

  // --- view pane -------------------------------------------------------------------------------
  function paintViews(): void {
    viewPane.textContent = "";
    const caps = deps.capabilities();
    // Say what this graph is BEFORE the list, so the greyed-out cards are explained in advance
    // rather than each one having to be clicked to find out.
    const blocked = VIEWS.filter((v) => v.requires?.(caps)).length;
    const summary = h(
      "p",
      "console-graph-qb__caps",
      (caps.flavor === "targets" ? "Target graph" : "Knowledge graph") +
        (caps.hasDurations ? ", with timing" : ", no timing") +
        (caps.live ? ", live" : ", snapshot") +
        (blocked ? " - " + blocked + " of " + VIEWS.length + " views need something else." : "."),
    );
    viewPane.append(summary);
    const rows: HTMLElement[] = [];
    for (const v of VIEWS) {
      const why = v.requires?.(caps) ?? null;
      const lines: Array<string | HTMLElement> = [why ?? v.blurb];
      if (v.cli && !why) lines.push(h("code", "console-graph-qb__viewcli", v.cli));
      if (v.picks && !why) lines.push(h("span", "console-graph-qb__viewpick", "then click a node"));
      const { li, button } = menuItem(v.label, lines, why ?? undefined);
      button.addEventListener("click", () => {
        // A view the graph cannot answer stays reachable, for the reason on its row, but runs nothing.
        if (why) return;
        close();
        if (v.id === "radial") deps.radialPick();
        else deps.runView(v.id);
      });
      rows.push(li);
    }
    viewPane.append(menuList(rows));
  }

  // --- footer ----------------------------------------------------------------------------------
  // No Apply: the canvas behind this panel already shows the answer. What the footer owes instead
  // is an undo, because experimenting is only safe if the filter you arrived with comes back.
  const foot = h("div", "console-graph-qb__foot");
  const resetBtn = h("button", "pf-v6-c-button pf-m-link");
  resetBtn.type = "button";
  resetBtn.append(h("span", "pf-v6-c-button__text", "Reset"));
  resetBtn.addEventListener("click", () => {
    terms = parseTerms(openedWith);
    paintRows();
    commit();
  });
  const clearBtn = h("button", "pf-v6-c-button pf-m-link");
  clearBtn.type = "button";
  clearBtn.append(h("span", "pf-v6-c-button__text", "Clear all"));
  clearBtn.addEventListener("click", () => {
    terms = [];
    paintRows();
    commit();
  });
  foot.append(resetBtn, clearBtn);

  box.append(head, notice, tabs, body, foot);
  overlay.append(box);

  // commit runs the query NOW. Called by every discrete control and by Enter/blur in a value, never
  // per keystroke.
  function commit(): void {
    deps.applyQuery(renderQuery(terms));
    paintCount();
  }

  function paintCount(): void {
    const m = deps.matchCount();
    countLine.textContent = m == null ? "" : m.matched + " of " + m.total + " nodes match";
    countLine.hidden = m == null;
  }

  function paint(): void {
    for (const [id, btn] of tabButtons) {
      const on = id === tab;
      tabItems.get(id)?.classList.toggle("pf-m-current", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
      btn.tabIndex = on ? 0 : -1;
    }
    filterPane.hidden = tab !== "filter";
    viewPane.hidden = tab !== "view";
    // The footer undoes a filter; a view runs on click and has nothing to undo here.
    foot.hidden = tab !== "filter";
    if (tab === "view") paintViews();
  }

  function open(): void {
    // Seed from whatever is in the filter box, so opening the builder over a hand-typed query
    // continues it rather than discarding it. openedWith is what Reset goes back to.
    openedWith = deps.currentQuery();
    terms = parseTerms(openedWith);
    tab = "filter";
    overlay.hidden = false;
    paint();
    paintRows();
    paintCount();
    rows.querySelector("select")?.focus();
  }

  function close(): void {
    // Bring it home first. Hiding the host while the panel lives in another window would leave that
    // window open with no way back to it.
    detached?.close();
    overlay.hidden = true;
    deps.onClose?.();
  }

  // On the box, not on document. Two reasons: this is not a modal, so Escape belongs to whatever the
  // reader is actually working in and only reaches here by bubbling out of the panel; and the box is
  // what detach MOVES, so the binding travels to the detached window instead of listening on a
  // document the panel has left. A document-level listener also outlived every activation, since the
  // builder is rebuilt per activation and nothing ever removed it.
  box.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !overlay.hidden) {
      e.preventDefault();
      close();
    }
  });

  return { el: overlay, open, close, isOpen: () => !overlay.hidden };
}
