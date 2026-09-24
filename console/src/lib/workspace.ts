// workspace.ts - reading a server's workspace list.

import { Workspace_State, type Workspace } from "@wire/status/v1alpha1/status_pb";

/**
 * Whether the server is serving w. The pool lists FAILED and LOADING workspaces too, so their
 * state can be shown; they are not loaded and must not be counted or offered as if they were.
 * An older server sends no state and lists only loaded workspaces.
 */
export function isServing(w: Workspace): boolean {
  return w.state !== Workspace_State.FAILED && w.state !== Workspace_State.LOADING;
}
