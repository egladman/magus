// home.ts - the console's default surface: a launcher that opens the other surfaces as tabs. It is a
// real PageModule (so it proves the console's mount/activate pipeline end to end) and stays the
// natural landing tab when the console opens with an empty workspace. Its search is a no-op - there
// is nothing to filter on the launcher.

import type { PageController, PageModule, SearchProvider } from "./page";

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
}

const noSearch: SearchProvider<null> = {
  placeholder: "",
  parse: () => null,
  apply: () => ({ matches: 0 }),
};

// homePage builds the launcher. `surfaces` is what it offers to open; `open` asks the console to open
// one as a tab. The console supplies both so home stays decoupled from the registry.
export function homePage(surfaces: Launchable[], open: (pageId: string) => void): PageModule<null, null> {
  return {
    id: "home",
    title: "Home",
    async activate(host: HTMLElement): Promise<PageController<null, null>> {
      // Class-free + semantic: data-surface tags the pane, the layout is styled via
      // #console-outlet [data-surface=home] and its child elements (h1, p, ul, li[data-open]).
      host.dataset.surface = "home";
      const title = document.createElement("h1");
      title.textContent = "magus console";
      const sub = document.createElement("p");
      sub.textContent = "Open a surface as a tab. Each is a live lens on the daemon.";

      const list = document.createElement("ul");
      for (const s of surfaces) {
        const card = document.createElement("li");
        card.dataset.open = s.pageId;
        // tabindex (not role=button) keeps it keyboard-reachable without Pico theming the card blue.
        card.setAttribute("tabindex", "0");
        card.setAttribute("aria-label", "Open " + s.label);
        const label = document.createElement("strong");
        label.textContent = s.label;
        const hint = document.createElement("small");
        hint.textContent = s.hint;
        card.append(label, hint);
        card.addEventListener("click", () => open(s.pageId));
        card.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(s.pageId); } });
        list.append(card);
      }

      host.append(title, sub, list);
      return { search: noSearch, deactivate() { host.replaceChildren(); } };
    },
  };
}
