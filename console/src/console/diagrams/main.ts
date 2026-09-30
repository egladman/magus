// main.ts - the Diagrams surface: the figures the server can draw, one at a time, through a
// declared lens. The server's SVG is on the page the moment it arrives and is the figure;
// interact.ts upgrades it in place, and the Buzz runtime (wasm.ts), loaded only on request, takes
// over lens changes so they lay out in the page without a round trip.

import { reportFailure } from "../../lib/notifications";
import { adoptServerOrigin, parseHash, resolveServerHost } from "../../lib/server";
import { subscribeDefaultHost } from "../../lib/settings";
import type { SurfaceInstance } from "../standalone";
import { h } from "../view";
import {
  listDiagrams,
  renderDiagram,
  type DiagramEntry,
  type DiagramFailure,
  type RenderedDiagram,
} from "./api";
import { attachFigure, edgesOf, neighbours, type FigureController } from "./interact";
import {
  EMPTY_LENS,
  fragmentForView,
  lensFields,
  lensIsEmpty,
  parseLensFields,
  viewFromHash,
  type Declaration,
  type Lens,
} from "./lens";
import { markListFocus, notice, prepareSvg, renderNodeList, type NoticeTone } from "./view";
import {
  anchorTemplate,
  drawnNodes,
  ensureBuzz,
  relayout,
  IMPORTS,
  type BuzzRuntime,
  type FigureMeta,
} from "./wasm";

const SOURCE = "Diagrams";

// Per mount, so each mount's control reflects its own request. The wasm itself is one Go
// instance per page: ensureBuzz loads it once and a second mount's load resolves at once.
type RuntimeState =
  | { readonly kind: "off" }
  | { readonly kind: "loading" }
  | { readonly kind: "ready"; readonly runtime: BuzzRuntime }
  | { readonly kind: "failed"; readonly detail: string };

type FigureState =
  | { readonly kind: "empty" }
  | { readonly kind: "loading"; readonly id: string; readonly lens: Lens }
  | {
      readonly kind: "shown";
      readonly id: string;
      readonly lens: Lens;
      readonly rendered: RenderedDiagram;
      readonly laidOut: "server" | "runtime";
    }
  | { readonly kind: "refused"; readonly id: string; readonly lens: Lens };

// The whole figure as the server declared it: what the runtime re-cuts for a new lens.
interface Base {
  readonly id: string;
  readonly decl: Declaration;
  readonly meta: FigureMeta;
  readonly rendered: RenderedDiagram;
}

export interface DiagramsRefs {
  readonly picker: HTMLSelectElement;
  readonly lensForm: HTMLFormElement;
  readonly scope: HTMLInputElement;
  readonly focus: HTMLInputElement;
  readonly depth: HTMLInputElement;
  readonly notices: HTMLElement;
  readonly controls: HTMLElement;
  readonly fit: HTMLButtonElement;
  readonly zoomIn: HTMLButtonElement;
  readonly zoomOut: HTMLButtonElement;
  readonly focusNode: HTMLButtonElement;
  readonly clearFocus: HTMLButtonElement;
  readonly runtime: HTMLButtonElement;
  readonly runtimeStatus: HTMLElement;
  readonly caption: HTMLElement;
  readonly frame: HTMLElement;
  readonly nodes: HTMLElement;
}

export function reducedMotion(): boolean {
  const media =
    typeof matchMedia === "function" && matchMedia("(prefers-reduced-motion: reduce)").matches;
  return media || document.documentElement.dataset.motion === "reduced";
}

function button(label: string, modifiers: string): HTMLButtonElement {
  const b = h("button", "pf-v6-c-button " + modifiers);
  b.type = "button";
  b.append(h("span", "pf-v6-c-button__text", label));
  return b;
}

function field(labelText: string, input: HTMLInputElement | HTMLSelectElement): HTMLElement {
  const wrap = h("label", "console-diagrams__field");
  wrap.append(h("span", "console-diagrams__field-label", labelText));
  const control = h("span", "pf-v6-c-form-control");
  control.append(input);
  wrap.append(control);
  return wrap;
}

function textInput(name: string, placeholder: string): HTMLInputElement {
  const i = h("input");
  i.type = "text";
  i.name = name;
  i.placeholder = placeholder;
  i.autocomplete = "off";
  i.spellcheck = false;
  return i;
}

// build lays the surface out: the bar (figure and lens), the figure's own controls in a row
// above it (never over it), then the figure beside its node list.
export function build(host: HTMLElement): DiagramsRefs {
  const page = h("section", "console-diagrams");
  page.setAttribute("aria-label", "Diagrams");

  const bar = h("header", "console-diagrams__bar");
  bar.dataset.controlSize = "default";
  const picker = h("select");
  picker.name = "diagram";
  const lensForm = h("form", "console-diagrams__lens");
  lensForm.setAttribute("aria-label", "Lens");
  const scope = textInput("scope", "internal/server, types");
  const focus = textInput("focus", "a node id or path");
  const depth = textInput("depth", "1");
  depth.inputMode = "numeric";
  depth.size = 3;
  const apply = button("Apply", "pf-m-secondary");
  apply.type = "submit";
  lensForm.append(field("Scope", scope), field("Focus", focus), field("Depth", depth), apply);
  bar.append(field("Figure", picker), lensForm);

  const notices = h("div", "console-diagrams__notices");
  notices.setAttribute("aria-live", "polite");

  const body = h("div", "console-diagrams__body");
  const main = h("div", "console-diagrams__main");
  const controls = h("div", "console-diagrams__controls");
  controls.dataset.controlSize = "compact";
  controls.setAttribute("role", "toolbar");
  controls.setAttribute("aria-label", "Figure controls");
  const fit = button("Fit", "pf-m-secondary");
  fit.title = "Fit the figure (f)";
  const zoomOut = button("Zoom out", "pf-m-secondary");
  zoomOut.title = "Zoom out (-)";
  const zoomIn = button("Zoom in", "pf-m-secondary");
  zoomIn.title = "Zoom in (+)";
  const focusNode = button("Focus", "pf-m-secondary");
  focusNode.title =
    "Focus the node you are on: its edges and one-hop neighbours stay, the rest dims";
  const clearFocus = button("Clear focus", "pf-m-secondary");
  clearFocus.title = "Clear focus (Esc)";
  clearFocus.disabled = true;
  const runtime = button("Load interactive runtime", "pf-m-tertiary");
  runtime.title = "Load the 4.2MB Buzz runtime so lens changes lay out in the page";
  const runtimeStatus = h("span", "console-diagrams__runtime-status");
  runtimeStatus.setAttribute("role", "status");
  controls.append(fit, zoomOut, zoomIn, focusNode, clearFocus, runtime, runtimeStatus);

  const figure = h("figure", "console-diagrams__figure");
  const frame = h("div", "console-diagrams__frame");
  frame.tabIndex = 0;
  frame.setAttribute(
    "aria-label",
    "Figure: f fits, + and - zoom, 0 is actual size, Esc clears focus",
  );
  const caption = h("figcaption", "console-diagrams__caption");
  figure.append(frame, caption);
  main.append(controls, figure);

  const aside = h("aside", "console-diagrams__aside");
  aside.setAttribute("aria-label", "Nodes");
  aside.append(h("h2", "console-diagrams__aside-title", "Nodes"));
  const nodes = h("ul", "console-diagrams__nodes");
  aside.append(nodes);
  body.append(main, aside);

  page.append(bar, notices, body);
  host.append(page);
  return {
    picker,
    lensForm,
    scope,
    focus,
    depth,
    notices,
    controls,
    fit,
    zoomIn,
    zoomOut,
    focusNode,
    clearFocus,
    runtime,
    runtimeStatus,
    caption,
    frame,
    nodes,
  };
}

// claimFor reads the kind from the figure id, as the handler's parseLens does.
function claimFor(id: string): string {
  return id.split(":")[0] === IMPORTS ? IMPORTS : "flow";
}

const READ_NOTICE: Record<string, { tone: NoticeTone; title: string }> = {
  refused: { tone: "warning", title: "This figure is too big to draw through this lens" },
  unindexed: { tone: "warning", title: "No symbol index to draw imports from" },
  "bad-lens": { tone: "warning", title: "The lens names something the figure does not have" },
  absent: { tone: "danger", title: "Not served here" },
  unreadable: { tone: "danger", title: "Could not read the figure" },
};

// activate builds the surface into host. Everything is per mount; only the runtime is shared.
export function activate(host: HTMLElement): SurfaceInstance {
  adoptServerOrigin();
  let serverHost = resolveServerHost(parseHash()) ?? "";
  const refs = build(host);
  let entries: DiagramEntry[] = [];
  let figure: FigureState = { kind: "empty" };
  let runtimeState: RuntimeState = { kind: "off" };
  let base: Base | null = null;
  let controller: FigureController | null = null;
  let current: AbortController | null = null;
  let lastNode: string | null = null;
  let stale = false;

  const showNotice = (tone: NoticeTone, title: string, body: string): void => {
    refs.notices.replaceChildren(notice(tone, title, body));
  };
  const clearNotices = (): void => refs.notices.replaceChildren();

  const showRead = (read: DiagramFailure): void => {
    const n = READ_NOTICE[read.kind];
    showNotice(n.tone, n.title, read.detail);
  };

  const writeHash = (id: string | null, lens: Lens): void => {
    const next = fragmentForView(parseHash(), { id, lens });
    if (next === location.hash || (next === "" && location.hash === "")) return;
    history.replaceState(null, "", next || location.pathname + location.search);
  };

  const syncRuntime = (): void => {
    const s = runtimeState;
    refs.runtime.disabled = s.kind === "loading" || s.kind === "ready";
    refs.runtimeStatus.textContent =
      s.kind === "loading"
        ? "Loading the runtime..."
        : s.kind === "ready"
          ? "Runtime loaded: lens changes lay out here."
          : s.kind === "failed"
            ? "Runtime failed to load."
            : "";
    refs.controls.dataset.runtime = s.kind;
  };

  const onFocusChange = (id: string | null): void => {
    refs.clearFocus.disabled = id === null;
    const svg = refs.frame.querySelector("svg");
    const lit = id !== null && svg ? neighbours(edgesOf(svg), id) : new Set<string>();
    markListFocus(refs.nodes, id, lit);
  };

  // mount puts an SVG on the page. A first figure fits; a re-layout of the same figure swaps in
  // place keeping the reader's zoom, fading unless motion is reduced.
  const mount = (rendered: RenderedDiagram, keepView: boolean): void => {
    const svg = prepareSvg(rendered.svg, { nodes: rendered.nodes, sourceUrl: rendered.sourceUrl });
    const old = refs.frame.querySelector("svg");
    if (keepView && controller && old) {
      if (!reducedMotion()) svg.dataset.entering = "";
      old.replaceWith(svg);
      controller.replace(svg);
      if (svg.dataset.entering !== undefined)
        requestAnimationFrame(() => {
          delete svg.dataset.entering;
        });
    } else {
      controller?.destroy();
      refs.frame.replaceChildren(svg);
      controller = attachFigure({ frame: refs.frame, svg, reducedMotion, onFocusChange });
    }
    refs.caption.textContent = rendered.title;
    renderNodeList(refs.nodes, {
      nodes: rendered.nodes,
      sourceUrl: rendered.sourceUrl,
      onFocus: (id) => controller?.focusNode(id),
    });
    onFocusChange(controller?.focused() ?? null);
  };

  const clearFigure = (): void => {
    controller?.destroy();
    controller = null;
    refs.frame.replaceChildren();
    refs.caption.textContent = "";
    refs.nodes.replaceChildren();
    refs.clearFocus.disabled = true;
  };

  const renderFromServer = async (id: string, lens: Lens): Promise<void> => {
    current?.abort();
    const ac = new AbortController();
    current = ac;
    figure = { kind: "loading", id, lens };
    refs.frame.setAttribute("aria-busy", "true");
    const read = await renderDiagram({ host: serverHost, signal: ac.signal }, id, lens);
    if (stale || current !== ac) return;
    refs.frame.removeAttribute("aria-busy");
    if (read.kind === "aborted") return;
    if (read.kind !== "ok") {
      clearFigure();
      figure = { kind: "refused", id, lens };
      showRead(read);
      return;
    }
    clearNotices();
    const sameFigure = base?.id === id && controller !== null;
    const claim = claimFor(id);
    const rendered: RenderedDiagram = {
      ...read.value,
      nodes: drawnNodes(read.value.nodes, claim),
    };
    try {
      mount(rendered, sameFigure);
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e);
      reportFailure(SOURCE, "Could not show diagram " + id + ": " + detail, "diagrams:mount:" + id);
      showNotice("danger", "Could not show the figure", detail);
      return;
    }
    figure = { kind: "shown", id, lens, rendered, laidOut: "server" };
    if (lensIsEmpty(lens)) {
      const svg = refs.frame.querySelector("svg");
      base = {
        id,
        rendered,
        decl: { nodes: rendered.nodes, edges: svg ? edgesOf(svg) : [] },
        meta: {
          id,
          title: rendered.title,
          claim,
          anchorHref: anchorTemplate(rendered.sourceUrl),
        },
      };
    } else if (base?.id !== id) base = null;
  };

  // renderInPage lays the figure out with the runtime. False when it cannot (no runtime, no
  // whole-figure declaration to cut), so the caller asks the server instead.
  const renderInPage = (id: string, lens: Lens): boolean => {
    const rt = runtimeState;
    if (rt.kind !== "ready" || !base || base.id !== id) return false;
    const out = relayout({
      runtime: rt.runtime,
      decl: base.decl,
      meta: base.meta,
      lens,
    });
    if (out.kind === "failed") {
      reportFailure(
        SOURCE,
        "The runtime could not lay out " + id + ": " + out.detail,
        "diagrams:relayout:" + id,
      );
      return false;
    }
    current?.abort();
    if (out.kind !== "ok") {
      reportFailure(SOURCE, out.detail, "diagrams:" + out.kind + ":" + id);
      clearFigure();
      figure = { kind: "refused", id, lens };
      showRead(out);
      return true;
    }
    const rendered: RenderedDiagram = {
      ...base.rendered,
      svg: out.svg,
      nodes: cutNodes(base, out.svg),
    };
    clearNotices();
    try {
      mount(rendered, controller !== null);
    } catch (e) {
      reportFailure(
        SOURCE,
        "The runtime's figure would not display: " + String(e),
        "diagrams:relayout-mount:" + id,
      );
      return false;
    }
    figure = { kind: "shown", id, lens, rendered, laidOut: "runtime" };
    return true;
  };

  const show = (id: string, lens: Lens): void => {
    refs.picker.value = id;
    const f = lensFields(lens);
    refs.scope.value = f.scope;
    refs.focus.value = f.focus;
    refs.depth.value = f.depth;
    writeHash(id, lens);
    if (!renderInPage(id, lens)) void renderFromServer(id, lens);
  };

  const loadList = async (): Promise<void> => {
    if (!serverHost) {
      showNotice(
        "info",
        "No server",
        "Diagrams are drawn by a running server. Start one with `magus server start`.",
      );
      return;
    }
    const read = await listDiagrams({ host: serverHost });
    if (stale) return;
    if (read.kind === "aborted") return;
    if (read.kind !== "ok") {
      showRead(read);
      return;
    }
    entries = read.value;
    refs.picker.replaceChildren(
      ...entries.map((e) => {
        const o = h(
          "option",
          undefined,
          e.title + (e.indexed === false ? " (needs an index)" : ""),
        );
        o.value = e.id;
        return o;
      }),
    );
    const wanted = viewFromHash(parseHash());
    const start = entries.find((e) => e.id === wanted.id) ?? entries[0];
    if (!start) {
      showNotice("info", "Nothing to draw", "This workspace has no figures the server can draw.");
      return;
    }
    show(start.id, start.id === wanted.id ? wanted.lens : EMPTY_LENS);
  };

  refs.picker.addEventListener("change", () => {
    base = null;
    show(refs.picker.value, EMPTY_LENS);
  });
  refs.lensForm.addEventListener("submit", (e) => {
    e.preventDefault();
    const parsed = parseLensFields({
      scope: refs.scope.value,
      focus: refs.focus.value,
      depth: refs.depth.value,
    });
    if (!parsed.ok) {
      showNotice("warning", "The lens is not complete", parsed.error);
      return;
    }
    if (refs.picker.value) show(refs.picker.value, parsed.lens);
  });
  refs.fit.addEventListener("click", () => controller?.fit());
  refs.zoomIn.addEventListener("click", () => controller?.zoom(1.25));
  refs.zoomOut.addEventListener("click", () => controller?.zoom(1 / 1.25));
  refs.clearFocus.addEventListener("click", () => controller?.focusNode(null));
  refs.frame.addEventListener("focusin", (e) => {
    const n = e.target instanceof Element ? e.target.closest("[data-node]") : null;
    if (n) lastNode = n.getAttribute("data-node");
  });
  refs.focusNode.addEventListener("click", () => {
    const id =
      lastNode ?? refs.frame.querySelector("[data-node]")?.getAttribute("data-node") ?? null;
    if (id) controller?.focusNode(id);
  });
  refs.runtime.addEventListener("click", () => {
    if (runtimeState.kind === "loading" || runtimeState.kind === "ready") return;
    runtimeState = { kind: "loading" };
    syncRuntime();
    void (async () => {
      try {
        runtimeState = { kind: "ready", runtime: await ensureBuzz() };
      } catch (e) {
        const detail = e instanceof Error ? e.message : String(e);
        reportFailure(SOURCE, "The Buzz runtime did not load: " + detail, "diagrams:runtime");
        runtimeState = { kind: "failed", detail };
      }
      if (stale) return;
      syncRuntime();
      if (runtimeState.kind === "failed")
        showNotice(
          "danger",
          "The interactive runtime did not load",
          runtimeState.detail + ". The figure above is still the server's.",
        );
    })();
  });

  syncRuntime();
  void loadList();

  const unsubscribeHost = subscribeDefaultHost(() => {
    if (figure.kind === "shown") return; // a figure being read keeps the server it came from
    serverHost = resolveServerHost(parseHash()) ?? "";
    void loadList();
  });

  return {
    setVisible: () => {},
    deactivate: () => {
      stale = true;
      current?.abort();
      controller?.destroy();
      unsubscribeHost();
    },
  };
}

// cutNodes is the base's node rows for the boxes a re-layout actually drew, in figure order.
function cutNodes(base: Base, svg: string): RenderedDiagram["nodes"] {
  const drawn = new Set([...svg.matchAll(/data-node="([^"]*)"/g)].map((m) => m[1]));
  return base.decl.nodes.filter((n) => drawn.has(n.id));
}
