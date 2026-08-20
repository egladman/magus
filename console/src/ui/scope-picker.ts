// scope-picker.ts - the title bar's workspace scope control: which workspace THIS BROWSER TAB is
// looking at. Named for what it does to the window rather than for the thing it lists, because
// picking here changes what every surface in the tab reports on.
//
// It is the console's account switcher. Two browser tabs can sit in two workspaces at once and stay
// there; the scope rides sessionStorage (lib/scope.ts), so it belongs to the tab and not to the
// browser. That is what makes "which workspace am I in" a question with one answer per window
// instead of a per-surface setting people have to keep in step by hand.
//
// HIDDEN until a daemon serves more than one workspace. With one loaded, scoped and unscoped show
// the same thing and the control is a decision nobody has to make - the same rule the dashboard's
// own picker used before this replaced it.
import { ALL_WORKSPACES, setWorkspaceScope, shortName, workspaceScope } from "../lib/scope";

export interface ScopePicker {
  // The daemon told us which workspaces it has loaded; rebuild the menu around them.
  setWorkspaces(roots: readonly string[]): void;
}

export function initScopePicker(host: HTMLElement): ScopePicker {
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

  btn.addEventListener("click", (e) => {
    e.stopPropagation();
    if (!open) rebuild();
    setOpen(!open);
  });
  document.addEventListener("click", (e) => {
    if (open && !menu.contains(e.target as Node) && !btn.contains(e.target as Node)) setOpen(false);
  });
  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key === "Escape" && open) {
      setOpen(false);
      btn.focus();
    }
  });

  host.prepend(menu);
  host.prepend(btn);
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
  };
}
