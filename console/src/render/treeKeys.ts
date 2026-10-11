// treeKeys.ts - the keyboard half of a PF tree view built from plain DOM. The roving tabindex lives on
// each li[role=treeitem], as in PF's own markup, so the whole tree is one Tab stop rather than one
// per row, and the WAI-ARIA tree keys sit on top of it. The row buttons stay out of the tab order:
// the focused item is the thing a screen reader announces with its expanded and selected state.
//
// Up and Down walk the VISIBLE items (one under a collapsed branch is skipped), Right opens a closed
// branch and then steps into it, Left closes an open branch and then climbs to its parent, Home and
// End jump to the ends. Enter and Space press the focused item's own button. The run browser carries
// its own copy of these keys because its Enter selects without toggling.

const ITEM = 'li[role="treeitem"]';
const NODE = ":scope > .pf-v6-c-tree-view__content > .pf-v6-c-tree-view__node";

function treeNode(item: HTMLElement): HTMLElement | null {
  return item.querySelector<HTMLElement>(NODE);
}

function parentItem(item: HTMLElement): HTMLElement | null {
  return item.parentElement?.closest<HTMLElement>(ITEM) ?? null;
}

// visible reports whether no ancestor branch of the item is collapsed.
function visible(item: HTMLElement): boolean {
  for (let anc = parentItem(item); anc; anc = parentItem(anc)) {
    if (anc.getAttribute("aria-expanded") === "false") return false;
  }
  return true;
}

// attachTreeKeys wires `tree` (the ul[role=tree]) and returns a function that re-seats the roving
// tab stop after the items were rebuilt or the selection moved. Call it once after every build.
export function attachTreeKeys(tree: HTMLElement): () => void {
  const items = (): HTMLElement[] => [...tree.querySelectorAll<HTMLElement>(ITEM)].filter(visible);

  const seat = (item: HTMLElement | undefined): void => {
    if (!item) return;
    for (const it of tree.querySelectorAll<HTMLElement>(ITEM)) it.tabIndex = it === item ? 0 : -1;
    for (const n of tree.querySelectorAll<HTMLElement>(".pf-v6-c-tree-view__node")) n.tabIndex = -1;
  };
  const reseat = (): void => {
    const all = items();
    seat(
      all.find((it) => it.getAttribute("aria-selected") === "true") ??
        all.find((it) => it.getAttribute("tabindex") === "0") ??
        all[0],
    );
  };
  const go = (item: HTMLElement | undefined): void => {
    if (!item) return;
    seat(item);
    item.focus();
  };

  tree.addEventListener("keydown", (ev) => {
    const item = (ev.target as Element | null)?.closest<HTMLElement>(ITEM);
    if (!item || ev.altKey || ev.ctrlKey || ev.metaKey) return;
    const all = items();
    const at = all.indexOf(item);
    const expanded = item.getAttribute("aria-expanded");
    switch (ev.key) {
      case "ArrowDown":
        go(all[at + 1]);
        break;
      case "ArrowUp":
        go(all[at - 1]);
        break;
      case "Home":
        go(all[0]);
        break;
      case "End":
        go(all[all.length - 1]);
        break;
      case "ArrowRight":
        if (expanded === "false") treeNode(item)?.click();
        else if (expanded === "true") go(all[at + 1]);
        break;
      case "ArrowLeft":
        if (expanded === "true") treeNode(item)?.click();
        else go(parentItem(item) ?? undefined);
        break;
      case "Enter":
      case " ":
        if (ev.target !== item) return;
        treeNode(item)?.click();
        break;
      default:
        return;
    }
    ev.preventDefault();
  });
  // A click or programmatic focus on an item makes it the tab stop, so Shift+Tab from the next
  // control returns to where the reader was.
  tree.addEventListener("focusin", (ev) => {
    const item = (ev.target as Element | null)?.closest<HTMLElement>(ITEM);
    if (item) seat(item);
  });
  return reseat;
}
