// treeKeys.ts - the keyboard half of a PF tree view built from plain DOM: a roving tabindex, so the
// whole tree is one Tab stop rather than one per row, and the WAI-ARIA tree keys on top of it. PF's
// own TreeView follows the same contract; the run browser carries its own copy tied to its state, and
// this one works from the markup alone, for a tree whose rows are built elsewhere.
//
// Up and Down walk the VISIBLE rows (a row under a collapsed branch is skipped), Right opens a closed
// branch and then steps into it, Left closes an open branch and then climbs to its parent, Home and
// End jump to the ends. Enter and Space are the row button's own click.

const NODE = ".pf-v6-c-tree-view__node";

function itemOf(node: Element): HTMLElement | null {
  return node.closest<HTMLElement>("li");
}

// visible reports whether no ancestor branch of the row is collapsed.
function visible(node: Element): boolean {
  let anc = itemOf(node)?.parentElement?.closest("li") ?? null;
  while (anc) {
    if (anc.getAttribute("aria-expanded") === "false") return false;
    anc = anc.parentElement?.closest("li") ?? null;
  }
  return true;
}

// attachTreeKeys wires `tree` (the ul[role=tree]) and returns a function that re-seats the roving
// tab stop after the rows were rebuilt or the selection moved. Call it once after every build.
export function attachTreeKeys(tree: HTMLElement): () => void {
  const rows = (): HTMLElement[] =>
    [...tree.querySelectorAll<HTMLElement>(NODE)].filter((n) => visible(n));

  const seat = (node: HTMLElement | undefined): void => {
    if (!node) return;
    for (const n of tree.querySelectorAll<HTMLElement>(NODE)) n.tabIndex = n === node ? 0 : -1;
  };
  const reseat = (): void => {
    const all = rows();
    seat(
      tree.querySelector<HTMLElement>(NODE + '[aria-current="true"]') ??
        all.find((n) => n.getAttribute("tabindex") === "0") ??
        all[0],
    );
  };
  const go = (node: HTMLElement | undefined): void => {
    if (!node) return;
    seat(node);
    node.focus();
  };

  tree.addEventListener("keydown", (ev) => {
    const node = (ev.target as Element | null)?.closest<HTMLElement>(NODE);
    if (!node || ev.altKey || ev.ctrlKey || ev.metaKey) return;
    const all = rows();
    const at = all.indexOf(node);
    const item = itemOf(node);
    const expanded = item?.getAttribute("aria-expanded");
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
        if (expanded === "false") node.click();
        else if (expanded === "true") go(all[at + 1]);
        break;
      case "ArrowLeft": {
        if (expanded === "true") {
          node.click();
        } else {
          const parent = item?.parentElement?.closest("li")?.querySelector<HTMLElement>(NODE);
          go(parent ?? undefined);
        }
        break;
      }
      default:
        return;
    }
    ev.preventDefault();
  });
  // A click or programmatic focus on a row makes it the tab stop, so Shift+Tab from the next control
  // returns to where the reader was.
  tree.addEventListener("focusin", (ev) => {
    const node = (ev.target as Element | null)?.closest<HTMLElement>(NODE);
    if (node) seat(node);
  });
  return reseat;
}
