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
      // data-surface tags the pane; its heading/lede layout is ID-scoped in console.css. The launcher
      // is a PatternFly Gallery of clickable Cards - the [data-open] hook the click handler keys on
      // rides on each card, and the whole card is the keyboard-reachable target (tabindex + Enter/Space).
      host.dataset.surface = "home";
      const title = document.createElement("h1");
      title.textContent = "magus console";
      const sub = document.createElement("p");
      sub.textContent = "Open a surface as a tab. Each is a live lens on the daemon.";

      const gallery = document.createElement("div");
      gallery.className = "pf-v6-l-gallery pf-m-gutter";
      for (const s of surfaces) {
        const card = document.createElement("div");
        card.className = "pf-v6-c-card pf-m-clickable pf-m-compact";
        card.dataset.open = s.pageId;
        // tabindex (not role=button) keeps the card keyboard-reachable without Pico's [role=button]
        // rule (still loaded until W4) painting it as a white-on-white centered primary button.
        card.setAttribute("tabindex", "0");
        card.setAttribute("aria-label", "Open " + s.label);
        const titleEl = document.createElement("div");
        titleEl.className = "pf-v6-c-card__title";
        const titleText = document.createElement("span");
        titleText.className = "pf-v6-c-card__title-text";
        titleText.textContent = s.label;
        titleEl.append(titleText);
        const body = document.createElement("div");
        body.className = "pf-v6-c-card__body";
        body.textContent = s.hint;
        card.append(titleEl, body);
        card.addEventListener("click", () => open(s.pageId));
        card.addEventListener("keydown", (ev) => { if (ev.key === "Enter" || ev.key === " ") { ev.preventDefault(); open(s.pageId); } });
        gallery.append(card);
      }

      host.append(title, sub, gallery);
      return { search: noSearch, deactivate() { host.replaceChildren(); } };
    },
  };
}
