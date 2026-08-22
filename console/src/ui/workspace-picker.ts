// workspace-picker.ts - the title bar's control for which workspace THIS BROWSER TAB is looking at.
// Picking here changes what every surface in the tab reports on.
//
// Named for the workspace rather than for the scope so it does not collide with view.ts's `Scope`,
// which is a component's disposer bag and has nothing to do with any of this. The CSS hooks are still
// spelled console-shell-scope__*; they name the tab's scope, which is accurate, and moving them would
// be a stylesheet change for a TypeScript problem.
//
// It is the console's account switcher. Two browser tabs can sit in two workspaces at once and stay
// there; the scope rides sessionStorage (lib/scope.ts), so it belongs to the tab and not to the
// browser. That is what makes "which workspace am I in" a question with one answer per window
// instead of a per-surface setting people have to keep in step by hand.
//
// HIDDEN until a daemon serves more than one workspace. With one loaded, scoped and unscoped show
// the same thing and the control is a decision nobody has to make - the same rule the dashboard's
// own picker used before this replaced it.
import { parseHash, wantsDemo } from "../lib/daemon";
import {
  ALL_WORKSPACES,
  onWorkspaceScope,
  setWorkspaceScope,
  shortName,
  workspaceScope,
} from "../lib/scope";

export interface WorkspacePickerOptions {
  // Enter or leave the daemon-free demo. This is the ONLY way in now - the seven per-surface
  // "See the demo" buttons are gone - so an unwired picker leaves the demo reachable only by typing
  // #demo into the address bar.
  onDemo(enter: boolean): void;
}

export interface WorkspacePicker {
  // The daemon told us which workspaces it has loaded; rebuild the menu around them.
  setWorkspaces(roots: readonly string[]): void;
  // Releases the document listeners and the scope subscription, and removes the control. The two
  // listeners live on `document`, not on the control, so dropping the reference alone would leak them
  // and leave a dead menu answering outside clicks for the life of the page.
  destroy(): void;
}

export function initWorkspacePicker(
  host: HTMLElement,
  opts: WorkspacePickerOptions,
): WorkspacePicker {
  const inDemo = (): boolean => wantsDemo(parseHash());
  // ONE control, not a label parked beside a button. The two were adjacent siblings - a bare caption
  // next to a bordered chip - and read as two unrelated things in a row of icon buttons rather than as
  // a field and its value. The border belongs to the WRAPPER, so it encloses both halves; the caption
  // sits on its own segment behind a hairline, the way a form addon does. Without that enclosure the
  // caption is just more text in the title bar.
  const wrap = document.createElement("div");
  wrap.className = "console-shell-scope";
  wrap.id = "console-scope";

  const caption = document.createElement("span");
  caption.className = "console-shell-scope__caption";
  caption.id = "console-scope-caption";
  caption.textContent = "Workspace";

  const btn = document.createElement("button");
  btn.id = "console-scope-btn";
  btn.type = "button";
  btn.className = "pf-v6-c-button pf-m-plain";
  btn.setAttribute("aria-haspopup", "true");
  btn.setAttribute("aria-expanded", "false");
  const label = document.createElement("span");
  label.className = "console-shell-scope__label";
  // A caret, because aria-haspopup tells a screen reader this opens a menu and nothing told anyone
  // else. Without it the value reads as a status readout rather than as something to press.
  const caret = document.createElement("span");
  caret.className = "console-shell-scope__caret";
  caret.innerHTML =
    '<svg viewBox="0 0 24 24" width="12" height="12" fill="none" stroke="currentColor" ' +
    'stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' +
    '<path d="M6 9l6 6 6-6"/></svg>';
  btn.append(label, caret);
  wrap.append(caption, btn);

  const menu = document.createElement("div");
  menu.className = "pf-v6-c-menu console-shell-scope__menu";
  menu.hidden = true;
  const list = document.createElement("ul");
  list.className = "pf-v6-c-menu__list";
  list.setAttribute("role", "menu");
  const content = document.createElement("div");
  content.className = "pf-v6-c-menu__content";
  content.append(list);
  menu.append(content);

  let open = false;
  const setOpen = (v: boolean): void => {
    open = v;
    menu.hidden = !v;
    btn.setAttribute("aria-expanded", v ? "true" : "false");
  };

  let roots: readonly string[] = [];

  const paint = (): void => {
    const scope = workspaceScope();
    // ALWAYS shown now. It used to hide below two workspaces, on the grounds that scope is not a
    // question with one possible answer - true when this control only chose a scope. It is also the
    // way into the demo now, and a console with no daemon has exactly zero workspaces, so the old
    // rule would have hidden the one control that offers anything at all on a first visit.
    wrap.hidden = false;
    const demo = inDemo();
    // The control names the WORKSPACE and nothing else. It used to read "Demo data" (replacing the
    // one fact it exists to report), then carried a "demo" tag beside the name - which put a word
    // in the title bar that repeats what the status bar's connection dot already says, on every
    // screen, permanently. The demo mark belongs in the MENU, against the row that enters it.
    // "All", not shortName's "All workspaces": the caption beside this button already says
    // Workspace, so the full phrase read as "Workspace All workspaces". The MENU row keeps the
    // long form, because nothing there supplies the noun.
    label.textContent = scope === ALL_WORKSPACES ? "All" : shortName(scope);
    const name = scope === ALL_WORKSPACES ? "all workspaces" : shortName(scope);
    btn.title =
      "Workspace: " +
      name +
      (demo ? " (demo data)" : "") +
      ". Change which workspace this tab shows.";
    // The caption supplies the "Workspace" half of the name; the button's own text supplies the value.
    btn.setAttribute("aria-labelledby", "console-scope-caption " + btn.id);
  };

  const rebuild = (): void => {
    const scope = workspaceScope();
    const row = (root: string, text: string): HTMLLIElement => {
      const li = document.createElement("li");
      li.className = "pf-v6-c-menu__list-item";
      li.setAttribute("role", "none");
      const b = document.createElement("button");
      b.type = "button";
      b.className = "pf-v6-c-menu__item";
      b.setAttribute("role", "menuitemradio");
      b.setAttribute("aria-checked", root === scope ? "true" : "false");
      if (root === scope) b.classList.add("pf-m-selected");
      const main = document.createElement("span");
      main.className = "pf-v6-c-menu__item-main";
      const t = document.createElement("span");
      t.className = "pf-v6-c-menu__item-text";
      t.textContent = text;
      // The full root as a tooltip: two workspaces can share a last segment, and the segment is all
      // the row shows.
      if (root) b.title = root;
      main.append(t);
      // In the demo these roots are SYNTHETIC. They are spelled like real checkouts (~/Repos/acme),
      // so without a mark the menu presents fabricated workspaces as though the daemon had loaded
      // them - the same class of untruth as a screen claiming a credential it never checked.
      if (root && inDemo()) {
        const tag = document.createElement("span");
        tag.className = "console-shell-scope__tag";
        tag.textContent = "demo";
        main.append(tag);
      }
      b.append(main);
      b.addEventListener("click", () => {
        setOpen(false);
        // Focus would otherwise sit on an element that just became hidden, which drops it to the body
        // and restarts the next Tab at the top of the page.
        btn.focus();
        setWorkspaceScope(root);
      });
      li.append(b);
      return li;
    };
    list.replaceChildren(
      // Daemon-wide first: it is the default, and it is the only scope under which the machine
      // readings (pool, cache, latency) describe exactly what they measure.
      row(ALL_WORKSPACES, "All workspaces"),
      ...roots.map((r) => row(r, shortName(r))),
      // Offered only when you are NOT in the demo. "Leave demo" was a verb that existed in one state
      // and read as clutter in a menu whose every other row answers "which workspace" - and the way
      // out is already there: the status bar says "demo" and clicking it opens the daemon address.
      ...(inDemo() ? [] : [demoRow()]),
    );
  };

  // An ACTION, not a scope, and only ever an entrance. It sits apart from the radio group above because
  // entering the demo is not choosing which workspace to look at - it changes whether anything on
  // screen is real. Keeping it in the same radio group would have made "am I looking at fabricated
  // data" one of the workspaces.
  const demoRow = (): HTMLLIElement => {
    const li = document.createElement("li");
    li.className = "pf-v6-c-menu__list-item console-shell-scope__demorow";
    li.setAttribute("role", "none");
    const b = document.createElement("button");
    b.type = "button";
    b.className = "pf-v6-c-menu__item";
    b.setAttribute("role", "menuitem");
    b.title = "Explore a populated console with no daemon running";
    const main = document.createElement("span");
    main.className = "pf-v6-c-menu__item-main";
    const t = document.createElement("span");
    t.className = "pf-v6-c-menu__item-text";
    t.textContent = "Demo data";
    main.append(t);
    const tag = document.createElement("span");
    tag.className = "console-shell-scope__tag";
    tag.textContent = "sample";
    main.append(tag);
    b.append(main);
    b.addEventListener("click", () => {
      setOpen(false);
      btn.focus();
      opts.onDemo(true);
    });
    li.append(b);
    return li;
  };

  const listeners = new AbortController();
  const { signal } = listeners;

  btn.addEventListener(
    "click",
    (e) => {
      e.stopPropagation();
      if (!open) rebuild();
      setOpen(!open);
    },
    { signal },
  );
  document.addEventListener(
    "click",
    (e) => {
      const target = e.target instanceof Node ? e.target : null;
      if (open && !menu.contains(target) && !btn.contains(target)) setOpen(false);
    },
    { signal },
  );
  document.addEventListener(
    "keydown",
    (e: KeyboardEvent) => {
      if (e.key === "Escape" && open) {
        setOpen(false);
        btn.focus();
      }
    },
    { signal },
  );

  host.prepend(menu);
  host.prepend(wrap);
  // The control has to follow the scope, not just set it: the scope can change from anywhere in the
  // tab, and without this the button kept announcing the workspace you left.
  const unsubscribe = onWorkspaceScope(paint);
  paint();

  return {
    setWorkspaces(next) {
      // Same list, same menu: the poll runs every 15s and rebuilding on every tick would close a menu
      // under the pointer.
      if (next.length === roots.length && next.every((r, i) => r === roots[i])) return;
      roots = [...next];
      paint();
      if (open) rebuild();
    },
    destroy() {
      listeners.abort();
      unsubscribe();
      wrap.remove();
      menu.remove();
    },
  };
}
