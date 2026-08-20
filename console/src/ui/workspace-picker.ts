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
import {
  ALL_WORKSPACES,
  onWorkspaceScope,
  setWorkspaceScope,
  shortName,
  workspaceScope,
} from "../lib/scope";

export interface WorkspacePicker {
  // The daemon told us which workspaces it has loaded; rebuild the menu around them.
  setWorkspaces(roots: readonly string[]): void;
  // Releases the document listeners and the scope subscription, and removes the control. The two
  // listeners live on `document`, not on the control, so dropping the reference alone would leak them
  // and leave a dead menu answering outside clicks for the life of the page.
  destroy(): void;
}

export function initWorkspacePicker(host: HTMLElement): WorkspacePicker {
  const btn = document.createElement("button");
  btn.id = "console-scope-btn";
  btn.type = "button";
  btn.className = "pf-v6-c-button pf-m-plain";
  btn.setAttribute("aria-haspopup", "true");
  btn.setAttribute("aria-expanded", "false");
  const label = document.createElement("span");
  label.className = "console-shell-scope__label";
  btn.append(label);

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
    // More than one loaded is the only case where scope is a question. Below that the control would
    // be an always-present reminder of a decision with one possible answer.
    const relevant = roots.length > 1;
    btn.hidden = !relevant;
    if (!relevant) {
      setOpen(false);
      return;
    }
    label.textContent = shortName(scope);
    const name = scope === ALL_WORKSPACES ? "all workspaces" : shortName(scope);
    btn.title = "Workspace: " + name;
    btn.setAttribute(
      "aria-label",
      "Workspace: " + name + ". Change which workspace this tab shows.",
    );
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
    );
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
  host.prepend(btn);
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
      btn.remove();
      menu.remove();
    },
  };
}
