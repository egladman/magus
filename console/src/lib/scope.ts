// scope.ts - which workspace this browser tab is looking at.
//
// SESSION storage, not local: the scope belongs to the BROWSER TAB, so two tabs can sit in two
// workspaces at once and neither moves when the other switches. That is the whole point of scoping by
// tab rather than by a control on one surface - you pick a workspace the way you pick an account, and
// everything in that tab answers for it.
//
// The shell and each surface are SEPARATE BUNDLES with their own module instances, so a module-level
// variable set by one is invisible to the others. Both halves therefore read the same sessionStorage
// key, and a change is announced with a window event: same-tab writes do not fire `storage`, so
// without the event a surface would never learn the shell had switched.
//
// The empty string means "every workspace this daemon has loaded" - the daemon-wide view, and the
// default. It is a real choice rather than an absence: with one workspace loaded, scoped and unscoped
// show the same thing, which is why this stays invisible until a daemon serves more than one.
const KEY = "magus:workspace-scope";
const EVENT = "console:workspace-scope";

export const ALL_WORKSPACES = "";

export function workspaceScope(): string {
  try {
    return sessionStorage.getItem(KEY) ?? ALL_WORKSPACES;
  } catch {
    return ALL_WORKSPACES; // storage disabled: everything stays daemon-wide, which is the honest fallback
  }
}

export function setWorkspaceScope(root: string): void {
  try {
    if (root === ALL_WORKSPACES) sessionStorage.removeItem(KEY);
    else sessionStorage.setItem(KEY, root);
  } catch {
    // Not durable this session; the event below still moves the live UI.
  }
  window.dispatchEvent(new CustomEvent(EVENT, { detail: root }));
}

// Which workspaces the daemon has loaded, announced by whoever learned it first.
//
// The shell polls GetStatus every 15s, but a SURFACE often knows sooner and more reliably: the
// dashboard holds an open status stream, and in the offline demo it is the only thing that knows at
// all, because there is no daemon to poll. So the list is published rather than owned - the shell's
// scope picker builds its menu from whatever arrives, from either source.
const WORKSPACES_EVENT = "console:workspaces";

export function publishWorkspaces(roots: readonly string[]): void {
  window.dispatchEvent(new CustomEvent(WORKSPACES_EVENT, { detail: [...roots] }));
}

export function onWorkspaces(fn: (roots: string[]) => void): () => void {
  const handler = (e: Event): void => {
    // Array.isArray proves the shape, not the element type; a non-string root reaches shortName and
    // throws on .split. Filtering is the same rigor onWorkspaceScope below applies to its scalar.
    if (e instanceof CustomEvent && Array.isArray(e.detail)) {
      fn((e.detail as unknown[]).filter((r): r is string => typeof r === "string"));
    }
  };
  window.addEventListener(WORKSPACES_EVENT, handler);
  return () => window.removeEventListener(WORKSPACES_EVENT, handler);
}

// onWorkspaceScope subscribes to changes made ANYWHERE in this browser tab - the shell's selector, or
// a surface that offers its own way in. Returns an unsubscribe.
export function onWorkspaceScope(fn: (root: string) => void): () => void {
  const handler = (e: Event): void => {
    fn(e instanceof CustomEvent && typeof e.detail === "string" ? e.detail : workspaceScope());
  };
  window.addEventListener(EVENT, handler);
  return () => window.removeEventListener(EVENT, handler);
}

// inScope reports whether a record belonging to `root` should be shown under the current scope.
// A record with NO workspace (a daemon-wide action - an MCP call, per activity.proto) is shown in
// every scope: it is not attributable, so hiding it would be claiming it belongs elsewhere.
export function inScope(recordRoot: string, scope: string = workspaceScope()): boolean {
  if (scope === ALL_WORKSPACES) return true;
  if (!recordRoot) return true;
  return recordRoot === scope;
}

// shortName renders a workspace root for a control: the last path segment, which is what people call
// the workspace. Falls back to the whole root when there is no segment to take.
export function shortName(root: string): string {
  if (!root) return "All workspaces";
  const parts = root.split("/").filter((p) => p !== "");
  return parts[parts.length - 1] ?? root;
}
