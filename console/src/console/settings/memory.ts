// memory.ts - the Settings "Agent memory" section: a dense, console-admin view over the
// durable agent-memory RECORDS the magus_memory MCP tool writes, spoken to over
// magus.memory.v1.MemoryService.
//
// Memory is a set of discrete records, each a typed POINTER into the magus domain (the refs
// ARE the payload); only a decision/plan carries a prose caption. The view is a list built
// for scanning and pruning many rows at once - per-row edit/delete plus checkbox
// multi-select with a single bulk delete - not roomy cards. A cursor snapshot ("where you
// left off") is pinned on top as the one genuine free-text blob.
//
// The content is AGENT-WRITTEN and UNTRUSTED. It is rendered through h() (textContent) and
// plain form controls ONLY, never innerHTML: no record string can carry markup into the page.

import { createClient, type Client } from "@connectrpc/connect";
import {
  MemoryService,
  MemoryType,
  MemoryRefKind,
  type Memory,
} from "../../gen/magus/memory/v1/memory_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../../lib/daemon";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../view";

// TYPE_LABELS / REFKIND_LABELS render an enum value as its lowercase wire word; the *_OPTIONS
// lists drive the edit-form selects and the type filter (UNSPECIFIED never offered).
const TYPE_LABELS: Record<number, string> = {
  [MemoryType.UNSPECIFIED]: "unspecified",
  [MemoryType.POINTER]: "pointer",
  [MemoryType.DECISION]: "decision",
  [MemoryType.PLAN]: "plan",
};
const TYPE_OPTIONS = [MemoryType.POINTER, MemoryType.DECISION, MemoryType.PLAN];

const REFKIND_LABELS: Record<number, string> = {
  [MemoryRefKind.UNSPECIFIED]: "?",
  [MemoryRefKind.QUERY]: "query",
  [MemoryRefKind.NODE]: "node",
  [MemoryRefKind.OUTPUT]: "output",
  [MemoryRefKind.COMMAND]: "command",
  [MemoryRefKind.DOC]: "doc",
};
const REFKIND_OPTIONS = [
  MemoryRefKind.QUERY,
  MemoryRefKind.NODE,
  MemoryRefKind.OUTPUT,
  MemoryRefKind.COMMAND,
  MemoryRefKind.DOC,
];

// hasCaption reports whether a type carries a prose body (decision/plan). A pointer never does.
function hasCaption(t: MemoryType): boolean {
  return t === MemoryType.DECISION || t === MemoryType.PLAN;
}

// draftRef is one editable ref row in the form (strings before they become a wire MemoryRef).
interface draftRef {
  kind: MemoryRefKind;
  target: string;
}

// buildMemorySection builds the section body and drives it live against the daemon at host. A
// null host short-circuits to a "connect first" empty state. Returns the body and a destroy()
// the surface calls on teardown so a late RPC never renders into a detached node. opts.onDenied
// fires when the daemon declines the service (a phone-share session): the caller hides the
// whole section, so the SERVER decides whether the memory view is offered.
export function buildMemorySection(
  host: string | null,
  opts: { onDenied?: () => void } = {},
): { el: HTMLElement; destroy(): void } {
  const body = h("div", "console-settings-memory");
  let stale = false;

  if (!host) {
    body.append(
      buildEmpty(
        "Not connected to a daemon",
        "Connect the console to a running daemon to view and edit agent memory. Open the console from a magus link, or set the daemon host on the General tab.",
      ),
    );
    return { el: body, destroy() { stale = true; } };
  }

  const client: Client<typeof MemoryService> = createClient(
    MemoryService,
    createDaemonTransport(host, getLiveToken()),
  );

  // Client-side view state: the selection (record names checked for bulk delete), a text
  // filter, a type filter, and the name of the record currently open in the inline editor
  // ("" for a new record, null for none). The dataset is small, so filtering is in JS over
  // the full ListMemories result rather than a server filter.
  const selected = new Set<string>();
  let filterText = "";
  let filterType: MemoryType | null = null;
  let editing: string | null = null;

  // load fetches the cursor and the records, then rebuilds the section. Called on mount and
  // after every save/delete so the view stays current.
  async function load(): Promise<void> {
    try {
      const [cursor, list] = await Promise.all([client.getCursor({}), client.listMemories({})]);
      if (stale) return;
      render(cursor.content, list.memories);
    } catch (e) {
      if (stale) return;
      if (isCapabilityDenied(e)) {
        opts.onDenied?.();
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      body.replaceChildren(
        buildEmpty(
          "Could not load memory",
          "The daemon at " + host + " did not answer the memory service (" + msg + "). Start it with: magus server start.",
        ),
      );
    }
  }

  function render(cursor: string, records: Memory[]): void {
    body.replaceChildren();
    body.append(buildCursorCard(cursor));

    const toolbar = h("div", "console-settings-memory__toolbar");
    const addBtn = button("Add memory", "pf-m-secondary pf-m-small");
    addBtn.addEventListener("click", () => { editing = ""; render(cursor, records); });

    const typeSel = h("select", "pf-v6-c-form-control console-settings-memory__filter") as HTMLSelectElement;
    typeSel.append(option("", "All types"));
    for (const t of TYPE_OPTIONS) typeSel.append(option(String(t), TYPE_LABELS[t]));
    typeSel.value = filterType == null ? "" : String(filterType);
    typeSel.addEventListener("change", () => {
      filterType = typeSel.value === "" ? null : (Number(typeSel.value) as MemoryType);
      render(cursor, records);
    });

    const search = h("input", "pf-v6-c-form-control console-settings-memory__search") as HTMLInputElement;
    search.type = "search";
    search.placeholder = "Filter by name, ref, or caption";
    search.value = filterText;
    search.addEventListener("input", () => { filterText = search.value; render(cursor, records); });

    toolbar.append(addBtn, typeSel, search);

    if (selected.size > 0) {
      const bulk = button("Delete selected (" + selected.size + ")", "pf-m-link pf-m-danger pf-m-small");
      bulk.addEventListener("click", () => void bulkDelete());
      toolbar.append(bulk);
    }
    body.append(toolbar);

    if (editing !== null) {
      body.append(buildEditForm(records.find((r) => r.name === editing)));
    }

    const shown = records.filter(matchesFilter);
    if (records.length === 0) {
      body.append(
        buildEmpty(
          "No memories yet",
          "Agents record memories as they work. Each is a typed pointer into the codebase - a saved query, a node, an output ref - so there is nothing to write by hand here; use Add memory only to seed one.",
        ),
      );
      return;
    }
    const list = h("div", "console-settings-memory__list");
    if (shown.length === 0) {
      list.append(h("p", "console-settings-memory__empty", "No records match the current filter."));
    }
    for (const rec of shown) list.append(buildRow(rec));
    body.append(list);
  }

  function matchesFilter(r: Memory): boolean {
    if (filterType != null && r.type !== filterType) return false;
    if (filterText.trim() === "") return true;
    const needle = filterText.toLowerCase();
    if (r.name.toLowerCase().includes(needle)) return true;
    if (r.body.toLowerCase().includes(needle)) return true;
    return r.refs.some((ref) => ref.target.toLowerCase().includes(needle));
  }

  // buildCursorCard renders the singleton snapshot: one textarea, one Save (UpdateCursor).
  // This is the ONE place a free-text blob is correct - it is a snapshot, not a record.
  function buildCursorCard(content: string): HTMLElement {
    const card = h("div", "console-settings-memory__cursor");
    card.append(h("h3", "console-settings-memory__title", "Resume - where you left off"));
    const area = h("textarea") as HTMLTextAreaElement;
    area.className = "pf-v6-c-form-control";
    area.rows = 3;
    area.spellcheck = false;
    area.value = content;
    area.setAttribute("aria-label", "Cursor snapshot");
    const save = button("Save", "pf-m-primary pf-m-small");
    save.addEventListener("click", () => {
      save.disabled = true;
      void client.updateCursor({ content: area.value }).then(
        () => { if (!stale) showToast("Agent memory", "Saved the cursor."); },
        (e: unknown) => { save.disabled = false; toastError("save the cursor", e); },
      );
    });
    const control = h("div", "console-settings-memory__cursorbody");
    control.append(area, save);
    card.append(control);
    return card;
  }

  // buildRow renders one record: a select checkbox, a type badge, its refs as chips, the
  // caption (decision/plan only), status, and per-row Edit and Delete actions.
  function buildRow(rec: Memory): HTMLElement {
    const row = h("div", "console-settings-memory__row");
    row.setAttribute("data-type", TYPE_LABELS[rec.type]);

    const check = h("input", "console-settings-memory__check") as HTMLInputElement;
    check.type = "checkbox";
    check.checked = selected.has(rec.name);
    check.setAttribute("aria-label", "Select " + rec.name);
    check.addEventListener("change", () => {
      if (check.checked) selected.add(rec.name);
      else selected.delete(rec.name);
      // Re-render only the toolbar's bulk button by reloading is overkill; toggle in place.
      const bulk = body.querySelector<HTMLElement>(".console-settings-memory__toolbar .pf-m-danger");
      if (selected.size === 0) bulk?.remove();
      else if (!bulk) void load();
      else bulk.textContent = "Delete selected (" + selected.size + ")";
    });

    const head = h("div", "console-settings-memory__rowhead");
    head.append(h("span", "console-settings-memory__badge", TYPE_LABELS[rec.type]));
    head.append(h("span", "console-settings-memory__name", rec.name));
    if (rec.status) head.append(h("span", "console-settings-memory__status", rec.status));

    const chips = h("div", "console-settings-memory__chips");
    for (const ref of rec.refs) {
      const chip = h("span", "console-settings-memory__chip");
      chip.append(h("span", "console-settings-memory__chipkind", REFKIND_LABELS[ref.kind]));
      chip.append(document.createTextNode(" " + ref.target));
      chips.append(chip);
    }

    const main = h("div", "console-settings-memory__rowmain");
    main.append(head, chips);
    if (rec.body) main.append(h("p", "console-settings-memory__caption", rec.body));

    const actions = h("div", "console-settings-memory__rowactions");
    const edit = button("Edit", "pf-m-secondary pf-m-small");
    edit.addEventListener("click", () => { editing = rec.name; void load(); });
    const del = button("Delete", "pf-m-link pf-m-danger pf-m-small");
    del.addEventListener("click", () => void deleteOne(rec.name));
    actions.append(edit, del);

    row.append(check, main, actions);
    return row;
  }

  // buildEditForm renders the inline create/update form. rec is undefined for a new record.
  // Save is an upsert (UpdateMemory with allow_missing), so create and edit share one path.
  function buildEditForm(rec: Memory | undefined): HTMLElement {
    const form = h("div", "console-settings-memory__edit");
    const nameInput = labeledInput("Name (kebab-slug)", rec?.name ?? "");
    nameInput.input.disabled = rec !== undefined; // name is identity; edit never renames

    const typeSel = h("select", "pf-v6-c-form-control") as HTMLSelectElement;
    for (const t of TYPE_OPTIONS) typeSel.append(option(String(t), TYPE_LABELS[t]));
    typeSel.value = String(rec?.type ?? MemoryType.POINTER);

    const statusInput = labeledInput("Status (optional)", rec?.status ?? "");
    const refsBox = h("div", "console-settings-memory__refs");
    const drafts: draftRef[] = rec ? rec.refs.map((r) => ({ kind: r.kind, target: r.target })) : [];
    const renderRefs = (): void => {
      refsBox.replaceChildren();
      refsBox.append(h("label", "console-settings-memory__label", "Refs (the payload - at least one)"));
      drafts.forEach((d, i) => refsBox.append(refRow(d, () => { drafts.splice(i, 1); renderRefs(); })));
      const add = button("Add ref", "pf-m-link pf-m-small");
      add.addEventListener("click", () => { drafts.push({ kind: MemoryRefKind.QUERY, target: "" }); renderRefs(); });
      refsBox.append(add);
    };
    if (drafts.length === 0) drafts.push({ kind: MemoryRefKind.QUERY, target: "" });
    renderRefs();

    const bodyWrap = h("div", "console-settings-memory__bodywrap");
    const bodyArea = h("textarea") as HTMLTextAreaElement;
    bodyArea.className = "pf-v6-c-form-control";
    bodyArea.rows = 2;
    bodyArea.placeholder = "Caption - the why (decision/plan only)";
    bodyArea.value = rec?.body ?? "";
    bodyWrap.append(h("label", "console-settings-memory__label", "Caption"), bodyArea);
    const syncBodyVisibility = (): void => {
      bodyWrap.hidden = !hasCaption(Number(typeSel.value) as MemoryType);
    };
    typeSel.addEventListener("change", syncBodyVisibility);
    syncBodyVisibility();

    const refsInput = labeledInput("References (comma-separated names, optional)", (rec?.references ?? []).join(", "));

    const save = button("Save", "pf-m-primary pf-m-small");
    const cancel = button("Cancel", "pf-m-link pf-m-small");
    cancel.addEventListener("click", () => { editing = null; void load(); });
    save.addEventListener("click", () => {
      const t = Number(typeSel.value) as MemoryType;
      const record = {
        name: nameInput.input.value.trim(),
        type: t,
        status: statusInput.input.value.trim(),
        body: hasCaption(t) ? bodyArea.value : "",
        refs: drafts
          .filter((d) => d.target.trim() !== "")
          .map((d) => ({ kind: d.kind, target: d.target.trim() })),
        references: refsInput.input.value
          .split(",")
          .map((s) => s.trim())
          .filter((s) => s !== ""),
      };
      save.disabled = true;
      cancel.disabled = true;
      void client.updateMemory({ memory: record, allowMissing: true }).then(
        () => { if (!stale) { showToast("Agent memory", "Saved " + record.name + "."); editing = null; void load(); } },
        (e: unknown) => { save.disabled = false; cancel.disabled = false; toastError("save " + record.name, e); },
      );
    });

    const typeWrap = h("div", "console-settings-memory__bodywrap");
    typeWrap.append(h("label", "console-settings-memory__label", "Type"), typeSel);
    const actions = h("div", "console-settings-memory__editactions");
    actions.append(save, cancel);
    form.append(nameInput.wrap, typeWrap, statusInput.wrap, refsBox, bodyWrap, refsInput.wrap, actions);
    return form;
  }

  function refRow(d: draftRef, onRemove: () => void): HTMLElement {
    const rowEl = h("div", "console-settings-memory__refrow");
    const kindSel = h("select", "pf-v6-c-form-control") as HTMLSelectElement;
    for (const k of REFKIND_OPTIONS) kindSel.append(option(String(k), REFKIND_LABELS[k]));
    kindSel.value = String(d.kind);
    kindSel.addEventListener("change", () => { d.kind = Number(kindSel.value) as MemoryRefKind; });
    const target = h("input", "pf-v6-c-form-control") as HTMLInputElement;
    target.value = d.target;
    target.placeholder = "target (node id, query, output ref, command, or doc)";
    target.addEventListener("input", () => { d.target = target.value; });
    const rm = button("Remove", "pf-m-link pf-m-small");
    rm.addEventListener("click", onRemove);
    rowEl.append(kindSel, target, rm);
    return rowEl;
  }

  async function deleteOne(name: string): Promise<void> {
    if (!confirm("Delete the memory " + name + "? This cannot be undone.")) return;
    try {
      await client.deleteMemory({ name, allowMissing: true });
      if (stale) return;
      selected.delete(name);
      showToast("Agent memory", "Deleted " + name + ".");
      void load();
    } catch (e) {
      toastError("delete " + name, e);
    }
  }

  async function bulkDelete(): Promise<void> {
    const names = [...selected];
    if (names.length === 0) return;
    if (!confirm("Delete " + names.length + " memories? This cannot be undone.")) return;
    try {
      await Promise.all(names.map((name) => client.deleteMemory({ name, allowMissing: true })));
      if (stale) return;
      selected.clear();
      showToast("Agent memory", "Deleted " + names.length + " memories.");
      void load();
    } catch (e) {
      toastError("delete the selected memories", e);
    }
  }

  function toastError(action: string, e: unknown): void {
    if (stale) return;
    const msg = e instanceof Error ? e.message : String(e);
    showToast("Agent memory", "Could not " + action + ": " + msg, "error");
  }

  void load();
  return { el: body, destroy() { stale = true; } };
}

// button builds a PatternFly button of the given modifier classes.
function button(label: string, modifiers: string): HTMLButtonElement {
  const b = h("button", "pf-v6-c-button " + modifiers, label) as HTMLButtonElement;
  b.type = "button";
  return b;
}

// option builds a <select> option.
function option(value: string, label: string): HTMLOptionElement {
  const o = h("option", "", label) as HTMLOptionElement;
  o.value = value;
  return o;
}

// labeledInput builds a labeled text input, returning the wrapper and the input.
function labeledInput(label: string, value: string): { wrap: HTMLElement; input: HTMLInputElement } {
  const wrap = h("div", "console-settings-memory__bodywrap");
  wrap.append(h("label", "console-settings-memory__label", label));
  const input = h("input", "pf-v6-c-form-control") as HTMLInputElement;
  input.value = value;
  wrap.append(input);
  return { wrap, input };
}

// buildEmpty renders the shared console empty state: a PF EmptyState with a title and body.
function buildEmpty(title: string, sub: string): HTMLElement {
  const wrap = h("div", "pf-v6-c-empty-state");
  const content = h("div", "pf-v6-c-empty-state__content");
  const bodyEl = h("div", "pf-v6-c-empty-state__body");
  bodyEl.append(h("p", "", sub));
  content.append(h("h2", "pf-v6-c-empty-state__title-text", title), bodyEl);
  wrap.append(content);
  return wrap;
}
