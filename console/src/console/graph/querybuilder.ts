// The graph's question builder: compose a filter term by term, or run one of the graph views.
//
// It replaces two columns of prebuilt chips. Those ran the right queries and taught nothing - a
// chip that silently types `kind:target -id:generate` into a box you never look at leaves you no
// better at asking the next question. Every term here writes into the SAME input you could have
// typed, so the builder is a way to learn the grammar rather than a way to avoid it.
//
// Two tabs because filters and views are different mechanics, and blurring them is what made the
// chips confusing. A filter is a string in the query grammar. A view runs a graph algorithm -
// there is no filter that expresses "what breaks if I change this", which is why several views
// have no CLI equivalent to show.

import { h } from "../view";

export interface QueryBuilderDeps {
  // Values present in the LOADED graph, so the pickers offer what will actually match rather than
  // every kind the schema allows.
  kinds: () => string[];
  relations: () => string[];
  projects: () => string[];
  currentQuery: () => string;
  applyQuery: (q: string) => void;
  runView: (view: string) => void;
  radialPick: () => void;
  // Views are conditional: cycles needs a target graph, critical needs timing, affected needs a
  // live workspace. Unavailable ones are shown with the reason rather than hidden, because a
  // question you cannot ask yet is still worth knowing about.
  viewUnavailable: (view: string) => string | null;
}

export interface QueryBuilder {
  el: HTMLElement;
  open(): void;
  close(): void;
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
// unrecognised field falls back to free text, which is what the filter does with it too.
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

// Worked examples. Each one loads into the term rows rather than applying straight off, so the
// next click is an EDIT: you see which term does what, change one, and the query is yours. A
// gallery that applied on click would be the chips again with more steps.
//
// Ordered easy to hard on purpose. The last three are the ones nobody discovers unaided - that
// terms AND together, that a leading hyphen subtracts, and that the two compose - and they are
// where the grammar stops being a search box and starts being a query language.
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
    blurb: "One node's neighbourhood, as rings by distance. Containment is not followed.",
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
  },
  {
    id: "critical",
    label: "What is slow?",
    blurb: "The longest duration-weighted chain. Needs a graph carrying timing.",
    cli: "magus graph deps -o json",
    picks: false,
  },
  {
    id: "affected",
    label: "What does my diff touch?",
    blurb: "The projects your working tree changes reach. Needs a live workspace.",
    cli: "magus affected ls",
    picks: false,
  },
];

export function createQueryBuilder(deps: QueryBuilderDeps): QueryBuilder {
  let terms: Term[] = [];
  let tab: "filter" | "view" = "filter";

  const overlay = h("div", "pf-v6-c-backdrop console-graph-qb");
  overlay.hidden = true;
  overlay.setAttribute("role", "dialog");
  overlay.setAttribute("aria-modal", "true");
  overlay.setAttribute("aria-label", "Ask the graph");

  const bullseye = h("div", "pf-v6-l-bullseye");
  const box = h("div", "pf-v6-c-modal-box pf-m-md console-graph-qb__box");
  const head = h("div", "pf-v6-c-modal-box__header");
  const titleWrap = h("div", "pf-v6-c-modal-box__title");
  titleWrap.append(h("span", "pf-v6-c-modal-box__title-text", "Ask the graph"));
  head.append(titleWrap);
  const closeBtn = h("button", "pf-v6-c-button pf-m-plain pf-v6-c-modal-box__close");
  closeBtn.type = "button";
  closeBtn.setAttribute("aria-label", "Close");
  closeBtn.append(h("span", "pf-v6-c-button__icon", "×"));
  closeBtn.addEventListener("click", () => close());

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

  const body = h("div", "pf-v6-c-modal-box__body console-graph-qb__body");
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
    });
    exWrap.append(b);
  }

  filterPane.append(rows, addBtn, previewLabel, preview, cliLine, hint, exLabel, exWrap);

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
      input.addEventListener("input", () => {
        t.value = input.value;
        paintPreview();
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
    for (const v of VIEWS) {
      const why = deps.viewUnavailable(v.id);
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
  const foot = h("div", "pf-v6-c-modal-box__footer console-graph-qb__foot");
  const applyBtn = h("button", "pf-v6-c-button pf-m-primary") as HTMLButtonElement;
  applyBtn.type = "button";
  applyBtn.append(h("span", "pf-v6-c-button__text", "Apply filter"));
  applyBtn.addEventListener("click", () => {
    deps.applyQuery(renderQuery(terms));
    close();
  });
  const cancelBtn = h("button", "pf-v6-c-button pf-m-link") as HTMLButtonElement;
  cancelBtn.type = "button";
  cancelBtn.append(h("span", "pf-v6-c-button__text", "Cancel"));
  cancelBtn.addEventListener("click", () => close());
  foot.append(applyBtn, cancelBtn);

  box.append(head, closeBtn, tabs, body, foot);
  bullseye.append(box);
  overlay.append(bullseye);

  overlay.addEventListener("click", (ev) => {
    if (!box.contains(ev.target as Node)) close();
  });

  function paint(): void {
    for (const [id, btn] of tabButtons) {
      const on = id === tab;
      btn.classList.toggle("pf-m-current", on);
      btn.setAttribute("aria-selected", on ? "true" : "false");
    }
    filterPane.hidden = tab !== "filter";
    viewPane.hidden = tab !== "view";
    // Apply belongs to the filter tab; a view runs on click and has nothing to commit.
    foot.hidden = tab !== "filter";
    if (tab === "view") paintViews();
  }

  function open(): void {
    // Seed from whatever is in the filter box, so opening the builder over a hand-typed query
    // continues it rather than discarding it.
    terms = parseTerms(deps.currentQuery());
    tab = "filter";
    overlay.hidden = false;
    paint();
    paintRows();
    (rows.querySelector("select") as HTMLSelectElement | null)?.focus();
  }

  function close(): void {
    overlay.hidden = true;
  }

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && !overlay.hidden) {
      e.preventDefault();
      close();
    }
  });

  return { el: overlay, open, close };
}
