import { test } from "node:test";
import assert from "node:assert/strict";
import {
  EDGE_CAP,
  FLOW_DASH_PX,
  FLOW_GAP_PX,
  type FlowLink,
  dashOffset,
  flowOrigin,
  flowSpeed,
  flows,
  pulseProgress,
  resetMotion,
  setFlowLinks,
  setPulses,
} from "./flow.js";

const PERIOD = FLOW_DASH_PX + FLOW_GAP_PX;

const link = (source: string, target: string, relation = "depends_on"): FlowLink => ({
  source,
  target,
  relation,
});

test("dashOffset: zero phase is zero in both directions", () => {
  assert.equal(dashOffset(0, 24, true), 0);
  assert.equal(dashOffset(0, 24, false), 0);
});

test("dashOffset: forward goes negative so dashes travel toward the path's end", () => {
  // 24 px/s for 250 ms is 6 px, inside one period.
  assert.equal(dashOffset(250, 24, true), -6);
  assert.equal(dashOffset(250, 24, false), 6);
});

test("dashOffset: wraps at one dash period", () => {
  // One second at PERIOD px/s travels exactly one period.
  assert.equal(dashOffset(1000, PERIOD, true), 0);
  assert.ok(Math.abs(dashOffset(2500, PERIOD, true)) < PERIOD);
});

test("dashOffset: divides by zoom so the motion keeps its screen speed", () => {
  assert.equal(dashOffset(250, 24, true, 2), -3);
});

test("flowSpeed: a fixed table per relation, with a default", () => {
  assert.ok(flowSpeed("depends_on") > flowSpeed("contains"));
  assert.equal(flowSpeed("imports"), flowSpeed("never-a-relation"));
});

test("flowOrigin: depends_on flows from the dependency (target), other relations from source", () => {
  assert.equal(flowOrigin(link("app", "lib")), "lib");
  assert.equal(flowOrigin(link("dir", "file", "contains")), "dir");
  assert.equal(flowOrigin({ source: { id: "a" }, target: { id: "b" }, relation: "uses" }), "a");
});

test("setFlowLinks: null, empty and over-cap leave flow off", () => {
  resetMotion();
  assert.equal(setFlowLinks(null), false);
  assert.equal(setFlowLinks([]), false);
  const over = Array.from({ length: EDGE_CAP + 1 }, (_, i) => link("n" + i, "n" + (i + 1)));
  assert.equal(setFlowLinks(over), false);
  assert.equal(flows(over[0], null, 0), false);
});

test("setFlowLinks: exactly EDGE_CAP links turns flow on", () => {
  resetMotion();
  const at = Array.from({ length: EDGE_CAP }, (_, i) => link("n" + i, "n" + (i + 1)));
  assert.equal(setFlowLinks(at), true);
  assert.equal(flows(at[EDGE_CAP - 1], null, 0), true);
});

test("flows: the view's set, by identity, not by endpoints", () => {
  resetMotion();
  const inSet = link("a", "b");
  setFlowLinks([inSet]);
  assert.equal(flows(inSet, null, 0), true);
  assert.equal(flows(link("a", "b"), null, 0), false);
});

test("flows: edges incident to the highlight, while its neighborhood fits the cap", () => {
  resetMotion();
  const e = link("a", "b");
  assert.equal(flows(e, "a", 3), true);
  assert.equal(flows(e, "b", 3), true);
  assert.equal(flows(e, "c", 3), false);
  assert.equal(flows(e, "a", EDGE_CAP + 1), false);
});

test("pulseProgress: reports age over lifetime, then null once expired", () => {
  resetMotion();
  setPulses(["a", "b"], 0);
  const half = pulseProgress(450);
  assert.ok(half);
  assert.equal(half.get("a"), 0.5);
  assert.equal(pulseProgress(901), null);
});

test("resetMotion: clears the flow set and the pulses", () => {
  const e = link("a", "b");
  setFlowLinks([e]);
  setPulses(["a"], 0);
  resetMotion();
  assert.equal(flows(e, null, 0), false);
  assert.equal(pulseProgress(0), null);
});
