// The graph's question builder: compose a filter term by term, or run a view.
//
// Every term writes into the same input you could have typed, which is the point - it replaced
// chips that ran the right query and never showed it. Two tabs because a filter is a string in the
// query grammar and a view is a graph algorithm; no filter expresses "what breaks if I change this".

import { h } from "../view";
import { canDetach, detachPanel, type DetachHandle } from "../../lib/detach";

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
}

export interface QueryBuilder {
  el: HTMLElement;
  open(): void;
  close(): void;
  isOpen(): boolean;
}

type Field = "" | "kind" | "project" | "relation" | "id";

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
    if (fm && ["kind", "project", "relation", "id"].includes(fm[1].toLowerCase())) {
      field = fm[1].toLowerCase() as Field;
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
    note: "Two terms AND together - both must match.",
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
    note: "Subtracting a RELATION finds absence - spells nothing documents.",
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
  detachBtn.innerHTML =
    '<span class="pf-v6-c-button__icon"><svg viewBox="0 0 24 24" width="14" height="14" fill="none" ' +
    'stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ' +
    'aria-hidden="true"><path d="M15 3h6v6"/><path d="M10 14 21 3"/>' +
    '<path d="M21 14v7H3V3h7"/></svg></span>';
  detachBtn.hidden = !canDetach();
  detachBtn.addEventListener("click", () => {
    if (detached?.isOpen()) {
      detached.close();
      return;
    }
    // Not awaited before the call: both window APIs spend the click's user activation, and an await
    // in between loses it.
    void detachPanel(box, {
      title: "Ask the graph",
      width: 460,
      height: 700,
      onReturn: () => {
        detached = null;
      },
    }).then((handle) => {
      detached = handle;
      // A browser can refuse both a PiP window and a popup. Saying so beats a button that looks
      // broken - which is what this did until the refusal was observed.
      if (!handle) {
        detachBtn.title = "Your browser would not open a second window for this panel";
        detachBtn.setAttribute("data-detach-failed", "");
      }
    });
  });

  const closeBtn = h("button", "pf-v6-c-button pf-m-plain console-graph-qb__close");
  closeBtn.type = "button";
  closeBtn.setAttribute("aria-label", "Close the builder");
  closeBtn.append(h("span", "pf-v6-c-button__icon", "×"));
  closeBtn.addEventListener("click", () => close());
  head.append(detachBtn, closeBtn);

  // Tabs
  const tabs = h("div", "pf-v6-c-tabs console-graph-qb__tabs");
  tabs.setAttribute("role", "tablist");
  const tabList = h("ul", "pf-v6-c-tabs__list");
  const tabButtons = new Map<"filter" | "view", HTMLButtonElement>();
  for (const [id, label] of [
    ["filter", "Build a filter"],
    ["view", "Run a view"],
  ] as ["filter" | "view", string][]) {
    const li = h("li", "pf-v6-c-tabs__item");
    const btn = h("button", "pf-v6-c-tabs__link") as HTMLButtonElement;
    btn.type = "button";
    btn.setAttribute("role", "tab");
    btn.append(h("span", "pf-v6-c-tabs__item-text", label));
    btn.addEventListener("click", () => {
      tab = id;
      paint();
    });
    li.append(btn);
    tabList.append(li);
    tabButtons.set(id, btn);
  }
  tabs.append(tabList);

  const body = h("div", "console-graph-qb__body");
  const filterPane = h("div", "console-graph-qb__pane");
  const viewPane = h("div", "console-graph-qb__pane");
  body.append(filterPane, viewPane);

  // --- filter pane -----------------------------------------------------------------------------
  const rows = h("div", "console-graph-qb__rows");
  const addBtn = h(
    "button",
    "pf-v6-c-button pf-m-secondary console-graph-qb__add",
  ) as HTMLButtonElement;
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
    const b = h("button", "pf-v6-c-button pf-m-link pf-m-inline") as HTMLButtonElement;
    b.type = "button";
    b.append(h("span", "pf-v6-c-button__text", label));
    b.addEventListener("click", () => {
      const text = get();
      if (!renderQuery(terms)) return;
      void navigator.clipboard?.writeText(text).then(() => {
        const span = b.querySelector(".pf-v6-c-button__text");
        if (!span) return;
        span.textContent = "Copied";
        setTimeout(() => {
          span.textContent = label;
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
  const exWrap = h("div", "console-graph-qb__examples");
  for (const ex of EXAMPLES) {
    const b = h("button", "console-graph-qb__example");
    b.type = "button";
    b.append(h("span", "console-graph-qb__exlabel", ex.label));
    b.append(h("code", "console-graph-qb__exquery", ex.query));
    b.append(h("span", "console-graph-qb__exnote", ex.note));
    b.addEventListener("click", () => {
      terms = parseTerms(ex.query);
      paintRows();
      commit(); // a discrete pick, same as any other - it applies and the canvas answers
    });
    exWrap.append(b);
  }

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

  function valueListFor(field: Field): string[] {
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

      const neg = h(
        "button",
        "pf-v6-c-button pf-m-control console-graph-qb__neg",
      ) as HTMLButtonElement;
      neg.type = "button";
      neg.setAttribute("aria-pressed", t.negated ? "true" : "false");
      neg.title = t.negated
        ? "Excluding these - click to include"
        : "Including these - click to exclude";
      neg.append(h("span", "pf-v6-c-button__text", t.negated ? "not" : "is"));
      neg.addEventListener("click", () => {
        t.negated = !t.negated;
        paintRows();
        commit();
      });

      const sel = h("span", "pf-v6-c-form-control console-graph-qb__field");
      const select = h("select") as HTMLSelectElement;
      select.setAttribute("aria-label", "Field");
      for (const f of ["", "kind", "project", "relation", "id"] as Field[]) {
        const o = h("option") as HTMLOptionElement;
        o.value = f;
        o.textContent = FIELD_LABEL[f];
        o.selected = f === t.field;
        select.append(o);
      }
      select.addEventListener("change", () => {
        t.field = select.value as Field;
        t.value = "";
        paintRows();
        commit();
      });
      sel.append(select);

      const valWrap = h("span", "pf-v6-c-form-control console-graph-qb__value");
      const input = h("input") as HTMLInputElement;
      input.type = "text";
      input.value = t.value;
      input.setAttribute("aria-label", "Value");
      input.spellcheck = false;
      input.autocomplete = "off";
      const list = valueListFor(t.field);
      if (list.length) {
        const dl = h("datalist") as HTMLDataListElement;
        dl.id = "qb-values-" + idx;
        for (const v of list) {
          const o = h("option") as HTMLOptionElement;
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
        if ((ev as KeyboardEvent).key === "Enter") {
          ev.preventDefault();
          commit();
        }
      });
      valWrap.append(input);

      const del = h(
        "button",
        "pf-v6-c-button pf-m-plain console-graph-qb__del",
      ) as HTMLButtonElement;
      del.type = "button";
      del.setAttribute("aria-label", "Remove this term");
      del.append(h("span", "pf-v6-c-button__icon", "×"));
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
    for (const v of VIEWS) {
      const why = v.requires?.(caps) ?? null;
      const card = h("button", "console-graph-qb__view") as HTMLButtonElement;
      card.type = "button";
      card.disabled = !!why;
      card.append(h("span", "console-graph-qb__viewlabel", v.label));
      card.append(h("span", "console-graph-qb__viewblurb", why ?? v.blurb));
      if (v.cli && !why) card.append(h("code", "console-graph-qb__viewcli", v.cli));
      if (v.picks && !why) {
        card.append(h("span", "console-graph-qb__viewpick", "then click a node"));
      }
      card.addEventListener("click", () => {
        close();
        if (v.id === "radial") deps.radialPick();
        else deps.runView(v.id);
      });
      viewPane.append(card);
    }
  }

  // --- footer ----------------------------------------------------------------------------------
  // No Apply: the canvas behind this panel already shows the answer. What the footer owes instead
  // is an undo, because experimenting is only safe if the filter you arrived with comes back.
  const foot = h("div", "console-graph-qb__foot");
  const resetBtn = h("button", "pf-v6-c-button pf-m-link") as HTMLButtonElement;
  resetBtn.type = "button";
  resetBtn.append(h("span", "pf-v6-c-button__text", "Reset"));
  resetBtn.addEventListener("click", () => {
    terms = parseTerms(openedWith);
    paintRows();
    commit();
  });
  const clearBtn = h("button", "pf-v6-c-button pf-m-link") as HTMLButtonElement;
  clearBtn.type = "button";
  clearBtn.append(h("span", "pf-v6-c-button__text", "Clear all"));
  clearBtn.addEventListener("click", () => {
    terms = [];
    paintRows();
    commit();
  });
  foot.append(resetBtn, clearBtn);

  box.append(head, tabs, body, foot);
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
      btn.classList.toggle("pf-m-current", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
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
    (rows.querySelector("select") as HTMLSelectElement | null)?.focus();
  }

  function close(): void {
    // Bring it home first. Hiding the host while the panel lives in another window would leave that
    // window open with no way back to it.
    detached?.close();
    overlay.hidden = true;
    deps.onClose?.();
  }

  document.addEventListener("keydown", (e) => {
    // Only when focus is inside the panel: this is no longer a modal, so Escape belongs to whatever
    // the reader is actually working in - stealing it would break Escape on the canvas.
    if (e.key === "Escape" && !overlay.hidden && overlay.contains(document.activeElement)) {
      e.preventDefault();
      close();
    }
  });

  return { el: overlay, open, close, isOpen: () => !overlay.hidden };
}
