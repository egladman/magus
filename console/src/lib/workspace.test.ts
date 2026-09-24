// workspace.test.ts - which listed workspaces count as loaded.

import assert from "node:assert/strict";
import { test } from "node:test";
import { create } from "@bufbuild/protobuf";
import { Workspace_State, WorkspaceSchema } from "@wire/status/v1alpha1/status_pb";
import { isServing } from "./workspace";

test("only an active workspace, or one from a server that sends no state, is serving", () => {
  const ws = (state: Workspace_State) => create(WorkspaceSchema, { root: "/repo", state });
  assert.equal(isServing(ws(Workspace_State.UNSPECIFIED)), true);
  assert.equal(isServing(ws(Workspace_State.ACTIVE)), true);
  assert.equal(isServing(ws(Workspace_State.LOADING)), false);
  assert.equal(isServing(ws(Workspace_State.FAILED)), false);
});
