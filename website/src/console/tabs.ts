// tabs.ts - the console workspace model: which surfaces are open as tabs and which is
// active. The state is a plain value (Workspace) mutated only through the PURE reducers
// below (openTab / closeTab / setActive), so the logic is unit-testable without a DOM;
// the reducers never mutate their input, they return a new Workspace. A persisted cell
// (workspaceStore) keeps the whole thing durable so reopening the console restores the
// exact set of tabs and the active one - the native "your workspace survived a restart"
// feel. The DOM tab strip that renders a Workspace lives with the console app (Phase 6),
// which is the only place multiple surfaces share one document; it reads and writes
// through these reducers.

import { persisted, type Persisted } from "../lib/persist";
import type { Pane } from "./tiling";

// A tab is one open instance of a surface: `id` is the tab's own identity (so the same
// surface can be opened twice), `pageId` is the tab's PRIMARY surface (dashboard|graph|logs)
// - its identity and title. `layout` is the tab's split-pane tree (tiling.ts): absent means the
// tab is a single un-split surface (the common case); present means the tab has been tiled, and
// the tree - serialized whole on every change - restores the exact split layout on reload.
export interface TabState {
  id: string;
  pageId: string;
  title: string;
  layout?: Pane;
}

export interface Workspace {
  tabs: TabState[];
  activeId: string | null;
}

export const emptyWorkspace: Workspace = { tabs: [], activeId: null };

// openTab appends a tab and activates it. Opening a tab whose id already exists does
// NOT duplicate it - it just activates the existing one (idempotent by id), so a
// double-open is harmless.
export function openTab(ws: Workspace, tab: TabState): Workspace {
  if (ws.tabs.some((t) => t.id === tab.id)) {
    return { tabs: ws.tabs, activeId: tab.id };
  }
  return { tabs: [...ws.tabs, tab], activeId: tab.id };
}

// closeTab removes a tab. When the closed tab was active, focus falls to its left
// neighbor (or the new left end), else to null when the last tab closes - the
// least-surprising "what gets focus next" for a tab strip. Closing a non-active tab
// leaves the active one untouched.
export function closeTab(ws: Workspace, id: string): Workspace {
  const idx = ws.tabs.findIndex((t) => t.id === id);
  if (idx === -1) return ws;
  const tabs = ws.tabs.filter((t) => t.id !== id);
  if (ws.activeId !== id) return { tabs, activeId: ws.activeId };
  if (tabs.length === 0) return { tabs, activeId: null };
  // Prefer the tab now sitting where the closed one's left neighbor was.
  const nextIdx = Math.max(0, idx - 1);
  return { tabs, activeId: tabs[nextIdx].id };
}

// setActive focuses an existing tab; an unknown id is a no-op (returns the input),
// so a stale deep link can't blank the workspace.
export function setActive(ws: Workspace, id: string): Workspace {
  if (!ws.tabs.some((t) => t.id === id)) return ws;
  if (ws.activeId === id) return ws;
  return { tabs: ws.tabs, activeId: id };
}

// setLayout records a tab's split-pane tree (after a split / close-pane / divider drag), so the
// tiled layout is durable and restores on reload. An unknown tab id is a no-op. The reducer replaces
// only the matching tab (new array, new tab object), leaving the active tab and every sibling
// untouched, so it composes with the tab reducers above.
export function setLayout(ws: Workspace, tabId: string, layout: Pane): Workspace {
  if (!ws.tabs.some((t) => t.id === tabId)) return ws;
  return { tabs: ws.tabs.map((t) => (t.id === tabId ? { ...t, layout } : t)), activeId: ws.activeId };
}

// workspaceStore is the durable cell the console binds to: read-modify-write it with the
// reducers and re-render the strip. Because it is persisted, the tab set and active tab
// come back on the next load.
export function workspaceStore(): Persisted<Workspace> {
  return persisted<Workspace>("workspace", emptyWorkspace);
}
