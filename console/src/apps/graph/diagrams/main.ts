// The Graph's Figures mode: one server-drawn figure at a time, through a declared lens. The
// server's SVG is the figure as soon as it arrives. The Buzz runtime (wasm.ts), loaded only on
// request, lays out later lens changes in the page without a round trip.

import { reportFailure } from "../../../lib/notifications";
import { adoptServerOrigin, parseHash, resolveServerHost } from "../../../lib/server";
import { subscribeDefaultHost } from "../../../lib/settings";
import type { AppInstance } from "../../../desktop/standalone";
import { h } from "../../../desktop/view";
import { emptyStateShell } from "../../../ui/empty-state";
import {
  listDiagrams,
  renderDiagram,
  type DiagramEntry,
  type DiagramFailure,
  type RenderedDiagram,
} from "./api";
import { decodeFigureLink } from "./figure-link";
import { attachFigure, readEdges, neighbours, type FigureController } from "./interact";
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
  figureForLink,
  linkedRows,
  relayout,
  toRelayout,
  IMPORTS,
  type BuzzRuntime,
  type FigureMeta,
} from "./wasm";

const SOURCE = "Figures";
// The id a shared link's figure goes by; no server figure has it.
const LINK_ID = "link";

// Per mount, so each mount's control reflects its own request. The wasm is one Go instance per
// page: ensureBuzz loads it once.
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
  readonly bar: HTMLElement;
  readonly picker: HTMLSelectElement;
  readonly lensForm: HTMLFormElement;
  readonly scope: HTMLInputElement;
  readonly focus: HTMLInputElement;
  readonly depth: HTMLInputElement;
  readonly apply: HTMLButtonElement;
  readonly notices: HTMLElement;
  readonly state: HTMLElement;
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

// build lays the app out: the bar (figure and lens), the figure's controls in a row above it
// (never over it), then the figure beside its node list.
export function build(host: HTMLElement): DiagramsRefs {
  const page = h("section", "console-diagrams");
  page.setAttribute("aria-label", "Figures");

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
  // "Center on", not "Focus": the Focus button below dims everything but a node, a different act from
  // choosing which node the server centers the drawing on. The field is still `focus` in the lens
  // and in the fragment.
  lensForm.append(field("Scope", scope), field("Center on", focus), field("Depth", depth), apply);
  bar.append(field("Figure", picker), lensForm);

  // Each notice carries its own live role (ui/alert.ts), so this host announces nothing itself: a
  // live container around role=alert children read every notice twice.
  const notices = h("div", "console-diagrams__notices");

  const body = h("div", "console-diagrams__body");
  const main = h("div", "console-diagrams__main");
  const controls = h("div", "console-diagrams__controls");
  controls.dataset.controlSize = "compact";
  // A group, not a toolbar: role=toolbar promises arrow-key movement between its buttons, and these
  // are ordinary Tab stops.
  controls.setAttribute("role", "group");
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
  // What is on screen when there is no figure to show: a spinner while it draws, a PF empty state
  // when there is nothing to draw, no server, or a refusal. A sibling of the frame and not inside
  // it, because the frame's only SVG is the figure.
  const state = h("div", "console-diagrams__state");
  state.hidden = true;
  const frame = h("div", "console-diagrams__frame");
  frame.tabIndex = 0;
  // The name is the thing; the keys are a shortcut list and a description, so a screen reader says
  // "Figure" and then, on request, how to drive it.
  frame.setAttribute("aria-label", "Figure");
  frame.setAttribute("aria-keyshortcuts", "f + - 0 Escape");
  frame.setAttribute("aria-describedby", KEYS_ID);
  const keys = h(
    "p",
    "pf-v6-screen-reader",
    "f fits the figure, plus and minus zoom, 0 is actual size, Escape clears focus.",
  );
  keys.id = KEYS_ID;
  const caption = h("figcaption", "console-diagrams__caption");
  figure.append(state, frame, keys, caption);
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
    bar,
    picker,
    lensForm,
    scope,
    focus,
    depth,
    apply,
    notices,
    state,
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

// parseClaim reads the kind from the figure id, as the handler's parseLens does.
function parseClaim(id: string): string {
  return id.split(":")[0] === IMPORTS ? IMPORTS : "flow";
}

const KEYS_ID = "console-diagrams-keys";
const SVG_NS = "http://www.w3.org/2000/svg";

// stateView is the frame's stand-in: a spinner or a PF empty state. Built from nodes, not markup,
// so a body that wants a <code> element can hand one over.
export function stateView(
  kind: "loading" | "empty",
  title: string,
  body?: string | Node,
): HTMLElement {
  if (kind === "loading") {
    const wrap = h("div", "console-diagrams__loading");
    const spinner = document.createElementNS(SVG_NS, "svg");
    spinner.setAttribute("class", "pf-v6-c-spinner pf-m-xl");
    spinner.setAttribute("role", "status");
    spinner.setAttribute("viewBox", "0 0 100 100");
    spinner.setAttribute("aria-label", title);
    const circle = document.createElementNS(SVG_NS, "circle");
    circle.setAttribute("class", "pf-v6-c-spinner__path");
    circle.setAttribute("cx", "50");
    circle.setAttribute("cy", "50");
    circle.setAttribute("r", "45");
    circle.setAttribute("fill", "none");
    spinner.append(circle);
    wrap.append(spinner, h("p", undefined, title));
    return wrap;
  }
  const state = emptyStateShell({ heading: "h2", title, classes: "pf-m-sm" });
  if (body === undefined) state.body.remove();
  else state.body.append(typeof body === "string" ? h("p", undefined, body) : body);
  state.footer.remove();
  return state.root;
}

// command is a sentence holding a shell command as a <code> element, never as backticks a screen
// reader would read out.
function command(before: string, cmd: string, after = "."): HTMLElement {
  const p = h("p");
  p.append(before, h("code", undefined, cmd), after);
  return p;
}

const READ_NOTICE: Record<string, { tone: NoticeTone; title: string }> = {
  refused: { tone: "warning", title: "This figure is too big to draw through this lens" },
  unindexed: { tone: "warning", title: "No symbol index to draw imports from" },
  "bad-lens": { tone: "warning", title: "The lens names something the figure does not have" },
  absent: { tone: "danger", title: "Not served here" },
  unreadable: { tone: "danger", title: "Could not read the figure" },
};

// activate builds the app into host. Everything is per mount; only the runtime is shared.
export function activate(host: HTMLElement): AppInstance {
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
  let linkPayload: string | null = null;

  const showNotice = (tone: NoticeTone, title: string, body: string | Node): void => {
    refs.notices.replaceChildren(notice(tone, title, body));
  };
  const clearNotices = (): void => refs.notices.replaceChildren();

  // showState puts the frame's stand-in on screen, or takes it away for a figure. The frame is
  // hidden while a stand-in shows, so its stale contents and focus stop cannot be reached.
  const showState = (view: HTMLElement | null): void => {
    if (view) refs.state.replaceChildren(view);
    else refs.state.replaceChildren();
    refs.state.hidden = view === null;
    refs.frame.hidden = view !== null;
  };

  // syncControls enables a control only when it has something to act on: the figure's own controls
  // need a figure, the picker, lens and runtime need a server's list to act on, and Clear focus needs
  // a focus. A dead button that answers a click with nothing reads as broken.
  const syncControls = (): void => {
    const has = controller !== null;
    for (const b of [refs.fit, refs.zoomIn, refs.zoomOut, refs.focusNode]) b.disabled = !has;
    refs.clearFocus.disabled = !has || controller?.focused() == null;
    const served = linkPayload === null && entries.length > 0;
    refs.picker.disabled = !served;
    refs.scope.disabled = !served;
    refs.focus.disabled = !served;
    refs.depth.disabled = !served;
    refs.apply.disabled = !served;
    refs.runtime.disabled =
      !served || runtimeState.kind === "loading" || runtimeState.kind === "ready";
  };

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
    syncControls();
    refs.runtimeStatus.textContent =
      s.kind === "loading"
        ? "Loading the runtime..."
        : s.kind === "ready"
          ? linkPayload === null
            ? "Runtime loaded: lens changes lay out here."
            : ""
          : s.kind === "failed"
            ? "Runtime failed to load."
            : "";
    refs.controls.dataset.runtime = s.kind;
  };

  const onFocusChange = (id: string | null): void => {
    refs.clearFocus.disabled = controller === null || id === null;
    const svg = refs.frame.querySelector("svg");
    const lit = id !== null && svg ? neighbours(readEdges(svg), id) : new Set<string>();
    markListFocus(refs.nodes, id, lit);
  };

  // A first figure fits; a re-layout of the same figure swaps in place keeping the reader's zoom.
  const mount = (rendered: RenderedDiagram, keepView: boolean): void => {
    const svg = prepareSvg(rendered.svg, { nodes: rendered.nodes, sourceUrl: rendered.sourceUrl });
    showState(null);
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
    syncControls();
  };

  const clearFigure = (): void => {
    controller?.destroy();
    controller = null;
    refs.frame.replaceChildren();
    refs.caption.textContent = "";
    refs.nodes.replaceChildren();
    syncControls();
  };

  // refusedState is the frame's stand-in once a figure could not be shown: the notice above says why,
  // so this only says that the frame is empty on purpose.
  const refusedState = (): void =>
    showState(stateView("empty", "No figure shown", "The notice above says why."));

  const renderFromServer = async (id: string, lens: Lens): Promise<void> => {
    current?.abort();
    const ac = new AbortController();
    current = ac;
    figure = { kind: "loading", id, lens };
    refs.frame.setAttribute("aria-busy", "true");
    // A figure already on screen stays (dimmed, busy) while the next one draws; only a frame with
    // nothing in it gets the spinner.
    if (controller === null) showState(stateView("loading", "Drawing the figure"));
    const read = await renderDiagram({ host: serverHost, signal: ac.signal }, id, lens);
    if (stale || current !== ac) return;
    refs.frame.removeAttribute("aria-busy");
    if (read.kind === "aborted") return;
    if (read.kind !== "ok") {
      clearFigure();
      figure = { kind: "refused", id, lens };
      showRead(read);
      refusedState();
      return;
    }
    clearNotices();
    const sameFigure = base?.id === id && controller !== null;
    const claim = parseClaim(id);
    const rendered: RenderedDiagram = {
      ...read.value,
      nodes: drawnNodes(read.value.nodes, claim),
    };
    try {
      mount(rendered, sameFigure);
    } catch (e) {
      const detail = e instanceof Error ? e.message : String(e);
      reportFailure(SOURCE, "Could not show figure " + id + ": " + detail, "diagrams:mount:" + id);
      showNotice("danger", "Could not show the figure", detail);
      if (controller === null) refusedState();
      return;
    }
    figure = { kind: "shown", id, lens, rendered, laidOut: "server" };
    if (lensIsEmpty(lens)) {
      const svg = refs.frame.querySelector("svg");
      base = {
        id,
        rendered,
        decl: { nodes: rendered.nodes, edges: svg ? readEdges(svg) : [] },
        meta: {
          id,
          title: rendered.title,
          claim,
          anchorHref: anchorTemplate(rendered.sourceUrl),
        },
      };
    } else if (base?.id !== id) base = null;
  };

  // False when the runtime cannot lay the figure out (not loaded, no declaration to cut), so the
  // caller asks the server instead.
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
      refusedState();
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
      showState(
        stateView(
          "empty",
          "No server",
          command("Figures are drawn by a running server. Start one with ", "magus server start"),
        ),
      );
      syncControls();
      return;
    }
    showState(stateView("loading", "Loading the figures"));
    const read = await listDiagrams({ host: serverHost });
    if (stale) return;
    if (read.kind === "aborted") return;
    if (read.kind !== "ok") {
      showRead(read);
      refusedState();
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
      showState(
        stateView("empty", "Nothing to draw", "This workspace has no figures the server can draw."),
      );
      syncControls();
      return;
    }
    syncControls();
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

  // A shared link is drawn from its fragment alone. The runtime loads at once because nothing
  // else can draw it, and the picker and lens have no server figure to act on.
  const openLink = async (payload: string): Promise<void> => {
    linkPayload = payload;
    current?.abort();
    const ac = new AbortController();
    current = ac;
    refs.bar.hidden = true;
    refs.runtime.hidden = true;
    clearFigure();
    clearNotices();
    const refuse = (title: string, detail: string): void => {
      clearFigure();
      figure = { kind: "refused", id: LINK_ID, lens: EMPTY_LENS };
      reportFailure(SOURCE, title + ": " + detail, "diagrams:link");
      showNotice("danger", title, detail);
      refusedState();
    };
    const read = decodeFigureLink(payload);
    if (!read.ok) {
      refuse("This figure link could not be read", read.error);
      return;
    }
    figure = { kind: "loading", id: LINK_ID, lens: EMPTY_LENS };
    refs.frame.setAttribute("aria-busy", "true");
    showState(stateView("loading", "Drawing the figure"));
    runtimeState = { kind: "loading" };
    syncRuntime();
    let runtime: BuzzRuntime | null = null;
    let loadError = "";
    try {
      runtime = await ensureBuzz();
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
    if (stale || current !== ac) return;
    refs.frame.removeAttribute("aria-busy");
    runtimeState = runtime ? { kind: "ready", runtime } : { kind: "failed", detail: loadError };
    syncRuntime();
    if (!runtime) {
      refuse("The runtime that draws this figure did not load", loadError);
      return;
    }
    const out = toRelayout(runtime.drawFigure(figureForLink(read.figure), ""));
    if (out.kind !== "ok") {
      refuse("This figure could not be drawn", out.detail);
      return;
    }
    const rendered: RenderedDiagram = {
      id: LINK_ID,
      title: read.figure.title,
      svg: out.svg,
      nodes: linkedRows(read.figure),
      sourceUrl: "",
    };
    try {
      mount(rendered, false);
    } catch (e) {
      refuse("Could not show the figure", e instanceof Error ? e.message : String(e));
      return;
    }
    base = null;
    figure = { kind: "shown", id: LINK_ID, lens: EMPTY_LENS, rendered, laidOut: "runtime" };
  };

  const onHashChange = (): void => {
    const next = parseHash().figure;
    if (next !== undefined && next !== linkPayload) void openLink(next);
  };
  window.addEventListener("hashchange", onHashChange);

  syncRuntime();
  const linked = parseHash().figure;
  if (linked !== undefined) void openLink(linked);
  else void loadList();

  const unsubscribeHost = subscribeDefaultHost(() => {
    if (linkPayload !== null) return; // a link needs no server
    if (figure.kind === "shown") return; // a figure being read keeps the server it came from
    serverHost = resolveServerHost(parseHash()) ?? "";
    void loadList();
  });

  return {
    setVisible: () => {},
    deactivate: () => {
      stale = true;
      window.removeEventListener("hashchange", onHashChange);
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
