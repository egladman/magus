// memory.ts - the Settings "Agent memory" section: an observable, EDITABLE view over the
// durable magus_memory files (status, progress, decisions) that agents write across sessions,
// spoken to over magus.memory.v1.MemoryService.
//
// Why editable: the memory files are append-heavy and never rotated by default, so they grow
// unbounded and an agent can silently bloat the store. A human read/edit/delete surface is the
// safety valve - the operator can audit what agents recorded and prune bad or oversized entries.
//
// The content is AGENT-WRITTEN and UNTRUSTED. It is rendered through text nodes ONLY (never
// innerHTML): the read view is a minimal, injection-proof markdown pass that builds real DOM
// elements from text, and the edit view is a plain <textarea>. No memory string can carry markup
// into the page.

import { createClient, type Client } from "@connectrpc/connect";
import { MemoryService, MemoryFile, type MemoryDoc } from "../../gen/magus/memory/v1/memory_pb";
import { createDaemonTransport, getLiveToken, isCapabilityDenied } from "../../lib/daemon";
import { showToast } from "../../lib/refresh-toast";
import { h } from "../view";

// FILE_ORDER fixes the display order and the explicit per-file labels the section shows. Each
// label names the file's role so the control's purpose is obvious on its own.
const FILE_ORDER: { file: MemoryFile; title: string; blurb: string }[] = [
  {
    file: MemoryFile.STATUS,
    title: "Status snapshot",
    blurb: "The current state agents overwrite each session.",
  },
  {
    file: MemoryFile.PROGRESS,
    title: "Progress journal",
    blurb: "A dated log of work done, appended over time.",
  },
  {
    file: MemoryFile.DECISIONS,
    title: "Decision log",
    blurb: "Dated decisions with the reasoning behind them.",
  },
];

// sizeLabel renders a byte count compactly (B / KB).
function sizeLabel(bytes: bigint): string {
  const n = Number(bytes);
  if (n < 1024) return n + " B";
  return (n / 1024).toFixed(1) + " KB";
}

// modifiedLabel renders a doc's last-modified time, or a "not written yet" note when absent.
function modifiedLabel(doc: MemoryDoc): string {
  const ts = doc.modified;
  if (!ts) return "Not written yet";
  const ms = Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6);
  return "Edited " + new Date(ms).toLocaleString() + " - " + sizeLabel(doc.sizeBytes);
}

// renderMarkdown is a MINIMAL, SAFE markdown pass: it builds real DOM elements from the raw text
// using textContent only, so agent-written content can never inject markup. It recognizes ATX
// headings (## / ###) and dash/star bullet lists; every other line is a paragraph. Inline syntax
// (bold, code) is left as literal text - correctness and safety over completeness.
function renderMarkdown(text: string): HTMLElement {
  const doc = h("div", "console-settings-memory__doc");
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  let list: HTMLElement | null = null;
  const closeList = (): void => {
    if (list) {
      doc.append(list);
      list = null;
    }
  };
  for (const raw of lines) {
    const line = raw.trimEnd();
    const bullet = /^\s*[-*]\s+(.*)$/.exec(line);
    if (bullet) {
      if (!list) list = h("ul", "console-settings-memory__list");
      list.append(h("li", "", bullet[1]));
      continue;
    }
    closeList();
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      const level = Math.min(6, Math.max(3, heading[1].length + 2)); // # -> h3, ## -> h4, ...
      doc.append(h(("h" + level) as "h3", "console-settings-memory__heading", heading[2]));
      continue;
    }
    if (line.trim() === "") continue;
    doc.append(h("p", "console-settings-memory__para", line));
  }
  closeList();
  return doc;
}

// buildMemorySection builds the section body and drives it live against the daemon at host. A null
// host short-circuits to a clear "connect first" empty state. Returns the body and a destroy() the
// surface calls on teardown so a late RPC never renders into a detached node. opts.onDenied fires
// when the daemon declines the memory service to this client (a phone-share session): the caller
// hides the whole section, so the SERVER decides whether the memory view is offered.
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
    return {
      el: body,
      destroy() {
        stale = true;
      },
    };
  }

  const client: Client<typeof MemoryService> = createClient(
    MemoryService,
    createDaemonTransport(host, getLiveToken()),
  );

  // load lists the files (for the on-disk dir + metadata), then fetches each file's content, and
  // rebuilds the section. Called on mount and after every save/delete so the view stays current.
  async function load(): Promise<void> {
    try {
      const list = await client.listMemory({});
      const docs = await Promise.all(
        FILE_ORDER.map((f) => client.getMemory({ file: f.file }).then((r) => r.doc)),
      );
      if (stale) return;
      body.replaceChildren();
      const dir = h("p", "console-settings-memory__dir");
      dir.append(document.createTextNode("Files on disk: "));
      dir.append(h("code", "", list.dir || "(unknown)"));
      body.append(dir);
      for (let i = 0; i < FILE_ORDER.length; i++) {
        const meta = FILE_ORDER[i];
        const doc = docs[i];
        body.append(buildFileCard(meta, doc ?? undefined));
      }
    } catch (e) {
      if (stale) return;
      // The daemon declined the service to this client (a read-only phone share): hide the section
      // entirely rather than show a failure - the server has decided the memory view is not offered.
      if (isCapabilityDenied(e)) {
        opts.onDenied?.();
        return;
      }
      const msg = e instanceof Error ? e.message : String(e);
      body.replaceChildren(
        buildEmpty(
          "Could not load memory",
          "The daemon at " +
            host +
            " did not answer the memory service (" +
            msg +
            "). Start it with: magus server start.",
        ),
      );
    }
  }

  // buildFileCard renders one memory file: a header (label, blurb, metadata) with Edit and Delete
  // actions over a body that is either the rendered markdown, an empty note, or the edit form.
  function buildFileCard(
    meta: { file: MemoryFile; title: string; blurb: string },
    doc: MemoryDoc | undefined,
  ): HTMLElement {
    const card = h("div", "console-settings-memory__card");

    const head = h("div", "console-settings-memory__head");
    const heading = h("div", "console-settings-memory__headtext");
    heading.append(
      h("h3", "console-settings-memory__title", meta.title),
      h("p", "console-settings-memory__blurb", meta.blurb),
      h("p", "console-settings-memory__meta", doc ? modifiedLabel(doc) : "Not written yet"),
    );
    const actions = h("div", "console-settings-memory__actions");
    const editBtn = h(
      "button",
      "pf-v6-c-button pf-m-secondary pf-m-small",
      "Edit",
    ) as HTMLButtonElement;
    editBtn.type = "button";
    editBtn.title = "Edit the " + meta.title.toLowerCase();
    editBtn.setAttribute("aria-label", "Edit the " + meta.title.toLowerCase());
    const deleteBtn = h(
      "button",
      "pf-v6-c-button pf-m-link pf-m-danger pf-m-small",
      "Delete",
    ) as HTMLButtonElement;
    deleteBtn.type = "button";
    deleteBtn.title = "Delete the " + meta.title.toLowerCase() + " file";
    deleteBtn.setAttribute("aria-label", "Delete the " + meta.title.toLowerCase() + " file");
    deleteBtn.disabled = !doc || !doc.exists;
    actions.append(editBtn, deleteBtn);
    head.append(heading, actions);
    card.append(head);

    const bodyBox = h("div", "console-settings-memory__cardbody");
    const content = doc?.content ?? "";
    if (doc && doc.exists && content.trim() !== "") {
      bodyBox.append(renderMarkdown(content));
    } else {
      bodyBox.append(
        h(
          "p",
          "console-settings-memory__empty",
          "This file is empty. Agents write to it as they work, or you can add content with Edit.",
        ),
      );
    }
    card.append(bodyBox);

    // enterEdit swaps the card body for a textarea seeded with the raw markdown, plus Save/Cancel.
    // Saving replaces the whole file (PutMemory is an overwrite, not an append).
    const enterEdit = (): void => {
      editBtn.disabled = true;
      bodyBox.replaceChildren();
      const form = h("div", "console-settings-memory__edit");
      const control = h("span", "pf-v6-c-form-control");
      const area = h("textarea") as HTMLTextAreaElement;
      area.value = content;
      area.rows = 12;
      area.spellcheck = false;
      area.setAttribute("aria-label", "Edit the " + meta.title.toLowerCase());
      control.append(area);
      const editActions = h("div", "console-settings-memory__editactions");
      const saveBtn = h(
        "button",
        "pf-v6-c-button pf-m-primary pf-m-small",
        "Save",
      ) as HTMLButtonElement;
      saveBtn.type = "button";
      const cancelBtn = h(
        "button",
        "pf-v6-c-button pf-m-link pf-m-small",
        "Cancel",
      ) as HTMLButtonElement;
      cancelBtn.type = "button";
      editActions.append(saveBtn, cancelBtn);
      form.append(control, editActions);
      bodyBox.append(form);
      area.focus();

      cancelBtn.addEventListener("click", () => void load()); // discard: rebuild from the saved state
      saveBtn.addEventListener("click", () => {
        saveBtn.disabled = true;
        cancelBtn.disabled = true;
        void client.putMemory({ file: meta.file, content: area.value }).then(
          () => {
            if (stale) return;
            showToast("Agent memory", "Saved " + meta.title.toLowerCase() + ".");
            void load();
          },
          (e) => {
            if (stale) return;
            saveBtn.disabled = false;
            cancelBtn.disabled = false;
            const msg = e instanceof Error ? e.message : String(e);
            showToast(
              "Agent memory",
              "Could not save " + meta.title.toLowerCase() + ": " + msg,
              "error",
            );
          },
        );
      });
    };
    editBtn.addEventListener("click", enterEdit);

    deleteBtn.addEventListener("click", () => {
      if (
        !confirm(
          "Delete the " +
            meta.title.toLowerCase() +
            "? This removes the file from disk and cannot be undone.",
        )
      )
        return;
      deleteBtn.disabled = true;
      void client.deleteMemory({ file: meta.file }).then(
        () => {
          if (stale) return;
          showToast("Agent memory", "Deleted " + meta.title.toLowerCase() + ".");
          void load();
        },
        (e) => {
          if (stale) return;
          deleteBtn.disabled = false;
          const msg = e instanceof Error ? e.message : String(e);
          showToast(
            "Agent memory",
            "Could not delete " + meta.title.toLowerCase() + ": " + msg,
            "error",
          );
        },
      );
    });

    return card;
  }

  void load();
  return {
    el: body,
    destroy() {
      stale = true;
    },
  };
}

// buildEmpty renders the shared console empty state: a PF EmptyState with a title and body line.
function buildEmpty(title: string, sub: string): HTMLElement {
  const wrap = h("div", "pf-v6-c-empty-state");
  const content = h("div", "pf-v6-c-empty-state__content");
  const bodyEl = h("div", "pf-v6-c-empty-state__body");
  bodyEl.append(h("p", "", sub));
  content.append(h("h2", "pf-v6-c-empty-state__title-text", title), bodyEl);
  wrap.append(content);
  return wrap;
}
