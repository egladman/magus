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
      host.classList.add("console-home"); // add, don't clobber the console-pane class the outlet set
      const title = document.createElement("h1");
      title.className = "console-home-title";
      title.textContent = "magus console";
      const sub = document.createElement("p");
      sub.className = "console-home-sub";
      sub.textContent = "Open a surface as a tab. Each is a live lens on the daemon.";

      const grid = document.createElement("div");
      grid.className = "console-home-grid";
      for (const s of surfaces) {
        const card = document.createElement("span");
        card.className = "console-home-card";
        card.dataset.pageId = s.pageId;
        // tabindex (not role=button) keeps it keyboard-reachable without Pico theming the card blue.
        card.setAttribute("tabindex", "0");
        card.setAttribute("aria-label", "Open " + s.label);
        const label = document.createElement("span");
        label.className = "console-home-card-label";
        label.textContent = s.label;
        const hint = document.createElement("span");
        hint.className = "console-home-card-hint";
        hint.textContent = s.hint;
        card.append(label, hint);
        card.addEventListener("click", () => open(s.pageId));
        card.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(s.pageId); } });
        grid.append(card);
      }

      host.append(title, sub, grid);
      return { search: noSearch, deactivate() { host.replaceChildren(); } };
    },
  };
}
