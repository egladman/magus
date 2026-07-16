// home.ts - the console's launcher. It is NOT a tab: the console renders it as the outlet's empty
// state (main.ts) whenever the workspace has zero open tabs (fresh load, or after the last tab is
// closed). Clicking a card opens that surface as a real tab; with a tab open, the command palette
// ("Open ...") is how another surface is launched. This module just builds the launcher DOM - a
// heading, a lede, and a PatternFly Gallery of clickable Cards - and leaves mounting to the console.

// A surface the launcher can open: the pageId the console registered it under, and a human label.
export interface Launchable {
  pageId: string;
  label: string;
  hint: string;
}

// buildLauncher builds the launcher DOM as the outlet's empty state. `surfaces` is what it offers to
// open; `open` asks the console to open one as a tab. The returned element carries data-surface="home"
// (its heading/lede layout is ID-scoped in console.css) and is appended straight into
// #console-outlet-content as a sibling of the tab panes, shown only when no tab is active.
export function buildLauncher(surfaces: Launchable[], open: (pageId: string) => void): HTMLElement {
  // data-surface tags the empty state; its heading/lede layout is ID-scoped in console.css. The
  // launcher is a PatternFly Gallery of clickable Cards - the [data-open] hook the click handler keys
  // on rides on each card, and the whole card is the keyboard-reachable target (tabindex + Enter/Space).
  const root = document.createElement("div");
  root.dataset.surface = "home";

  const title = document.createElement("h1");
  title.textContent = "magus console";
  const sub = document.createElement("p");
  sub.textContent = "Pick a tool to open it in a tab.";

  const gallery = document.createElement("div");
  gallery.className = "pf-v6-l-gallery pf-m-gutter";
  for (const s of surfaces) {
    const card = document.createElement("div");
    card.className = "pf-v6-c-card pf-m-clickable pf-m-compact";
    card.dataset.open = s.pageId;
    // A real clickable button: role=button + tabindex make it keyboard-reachable and announce it
    // as a button; the Enter/Space handler below completes the contract.
    card.setAttribute("role", "button");
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

  root.append(title, sub, gallery);
  return root;
}
