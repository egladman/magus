// glossary-terms.ts - two interactions on auto-linked glossary terms, split by input:
//
//   hover / focus -> a quick popover, gated on matchMedia("(hover: hover)") so a touch
//                    tap's synthetic pre-click mouseenter/focus can't flash it open (desktop
//                    peek only)
//   click / tap   -> on a hover-capable device the popover above already shows everything
//                    a click would (both render the same cardBody()), so the click just
//                    follows the link; on touch (no hover) it instead opens a split reveal -
//                    the content parts open below the term's paragraph to show a recessed
//                    panel - since that's the only way to see the definition at all
//
// Everything is static. The render pass (lib/glossary.buzz) links each term and bakes its
// definition into the link (data-def), so both surfaces read the text straight off the DOM
// - no fetch, no JSON. The full "referenced on" backlinks live on the glossary page (the
// href target), because a term's reference set is a whole-corpus aggregate. With no JS the
// term is still a plain link to that glossary entry, so nothing is lost.
//
// The popover registers with the popups coordinator so opening it closes any other menu.
import { registerPopup, notifyPopupOpen } from "./popups.js";

// Human labels for the data-kind on a linked term. Glossary terms carry no kind; code
// entities and convention hints do.
const KIND_LABELS: Record<string, string> = {
  code: "Diagnostic code",
  command: "Command",
  method: "Method",
  config: "Config key",
  convention: "Convention",
};

export function initGlossaryTerms(): void {
  const terms = document.querySelectorAll<HTMLAnchorElement>(
    "a.glossary-term, a.code-xref, a.convention-hint",
  );
  if (!terms.length) return;

  function esc(s: string): string {
    const d = document.createElement("div");
    d.textContent = s;
    return d.innerHTML;
  }

  // Shared inner markup for both surfaces: an optional kind label (code entities), the baked
  // definition, then a jump to the reference page - the full glossary entry (with backlinks)
  // for a term, or the code/command/method/config reference otherwise.
  function cardBody(term: HTMLAnchorElement): string {
    const def = term.dataset.def || "";
    const href = term.getAttribute("href") || "";
    const kind = term.dataset.kind || "";
    const label = KIND_LABELS[kind];
    const jump =
      kind === "convention"
        ? "About this convention"
        : label
          ? "Open reference"
          : "Full entry &amp; references";
    return (
      (label ? `<p class="gloss-kind">${label}</p>` : "") +
      (def ? `<p class="gloss-def">${esc(def)}</p>` : "") +
      `<div class="gloss-jumps"><a href="${esc(href)}">${jump} &rarr;</a></div>`
    );
  }

  // --- Hover popover -------------------------------------------------------------------
  // Touch devices report hover:none, but a tap still fires a synthetic mouseenter (and
  // sometimes focus) just before the click as part of the browser's tap-compatibility
  // sequence - without this guard, tapping a term would flash the popover open right
  // alongside the split-reveal it's actually meant to trigger. Real hover-capable
  // devices (including keyboard nav on a desktop/laptop, which reports hover:hover even
  // without an active mouse) are unaffected.
  const canHover = !!(window.matchMedia && window.matchMedia("(hover: hover)").matches);
  const pop = document.createElement("div");
  pop.className = "glossary-popover";
  pop.hidden = true;
  pop.setAttribute("role", "dialog");
  document.body.appendChild(pop);
  let popTerm: HTMLAnchorElement | null = null;
  let hideTimer: number | undefined;

  function hidePop(): void {
    pop.hidden = true;
    popTerm = null;
  }
  const dismissable = { close: hidePop };
  registerPopup(dismissable);

  function scheduleHide(): void {
    window.clearTimeout(hideTimer);
    hideTimer = window.setTimeout(hidePop, 180);
  }
  function cancelHide(): void {
    window.clearTimeout(hideTimer);
  }

  function showPop(term: HTMLAnchorElement): void {
    if (!canHover) return;
    if (revealTerm === term) return; // its reveal is already open; skip the peek
    cancelHide();
    pop.innerHTML = cardBody(term);
    const r = term.getBoundingClientRect();
    pop.style.visibility = "hidden";
    pop.hidden = false;
    let left = r.left + window.scrollX;
    const maxLeft = window.scrollX + document.documentElement.clientWidth - pop.offsetWidth - 8;
    if (left > maxLeft) left = Math.max(window.scrollX + 8, maxLeft);
    let top = r.bottom + window.scrollY + 6;
    const ph = pop.offsetHeight;
    if (r.bottom + ph + 12 > window.innerHeight && r.top - ph - 6 > 0)
      top = r.top + window.scrollY - ph - 6;
    pop.style.left = `${Math.round(left)}px`;
    pop.style.top = `${Math.round(top)}px`;
    pop.style.visibility = "";
    popTerm = term;
    notifyPopupOpen(dismissable);
  }

  // --- Click split-reveal --------------------------------------------------------------
  let reveal: HTMLElement | null = null;
  let revealTerm: HTMLAnchorElement | null = null;

  function closeReveal(): void {
    if (!reveal) return;
    const el = reveal;
    const term = revealTerm;
    el.style.height = `${el.scrollHeight}px`;
    requestAnimationFrame(() => {
      el.classList.remove("is-open");
      el.style.height = "0";
    });
    if (term) term.classList.remove("is-open");
    const done = (e: TransitionEvent): void => {
      if (e.propertyName !== "height") return;
      el.removeEventListener("transitionend", done);
      el.remove();
    };
    el.addEventListener("transitionend", done);
    reveal = null;
    revealTerm = null;
  }

  function openReveal(term: HTMLAnchorElement): void {
    if (revealTerm === term) {
      closeReveal();
      return;
    }
    if (reveal) closeReveal();
    const block = term.closest("p, li, blockquote, pre, figure") as HTMLElement | null;
    if (!block) return;
    const el = document.createElement("div");
    el.className = "glossary-reveal";
    el.innerHTML =
      `<div class="glossary-reveal-inner">` +
      `<button class="glossary-reveal-close" type="button" aria-label="Close">&times;</button>` +
      cardBody(term) +
      `</div>`;
    block.insertAdjacentElement("afterend", el);
    term.classList.add("is-open");
    // Inner panel height plus its 0.5rem top+bottom margin, so the trench opens to the exact
    // content height before JS settles it to auto.
    const target = (el.firstElementChild as HTMLElement).offsetHeight + 16;
    el.style.height = "0";
    requestAnimationFrame(() => {
      el.classList.add("is-open");
      el.style.height = `${target}px`;
    });
    const settle = (e: TransitionEvent): void => {
      if (e.propertyName === "height" && el.classList.contains("is-open")) el.style.height = "auto";
    };
    el.addEventListener("transitionend", settle);
    el.querySelector(".glossary-reveal-close")?.addEventListener("click", closeReveal);
    reveal = el;
    revealTerm = term;
  }

  // --- Wiring --------------------------------------------------------------------------
  terms.forEach((term) => {
    term.addEventListener("mouseenter", () => showPop(term));
    term.addEventListener("mouseleave", scheduleHide);
    term.addEventListener("focus", () => showPop(term));
    term.addEventListener("blur", scheduleHide);
    term.addEventListener("click", (e: MouseEvent) => {
      // The reveal and the hover popover render the exact same cardBody() content, so on
      // a hover-capable device the popover already covers a click's worth of extra
      // information - let the click follow the link normally instead of also parting the
      // page open. Touch devices have no hover, so their click is the only way to see the
      // definition at all; that's where the reveal earns its keep.
      if (canHover) return;
      e.preventDefault();
      hidePop();
      openReveal(term);
    });
  });
  pop.addEventListener("mouseenter", cancelHide);
  pop.addEventListener("mouseleave", scheduleHide);

  document.addEventListener("keydown", (e: KeyboardEvent) => {
    if (e.key !== "Escape") return;
    if (!pop.hidden) hidePop();
    closeReveal();
  });
  document.addEventListener("click", (e: MouseEvent) => {
    if (pop.hidden) return;
    const t = e.target as Node;
    if (pop.contains(t) || (popTerm && popTerm.contains(t))) return;
    hidePop();
  });
}
