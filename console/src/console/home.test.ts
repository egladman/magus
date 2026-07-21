import test from "node:test";
import assert from "node:assert/strict";
import { launcherTagline } from "./home";

// launcherTagline picks a time-eligible tagline. These pin the hour-window gating (including the
// midnight-wrapping night window) and that a choice is always available at every hour.

const at = (hour: number): Date => new Date(2026, 0, 1, hour, 0, 0);

test("an eligible tagline exists at every hour", () => {
  for (let h = 0; h < 24; h++) {
    const t = launcherTagline(at(h), () => 0);
    assert.ok(t.length > 0, `hour ${h} produced an empty tagline`);
  }
});

test("morning window surfaces a morning line, not an evening one", () => {
  // pick=0 selects the first eligible entry; the ANY_HOURS pool leads, so scan the whole eligible set.
  const seen = new Set<string>();
  for (let i = 0; i < 12; i++) seen.add(launcherTagline(at(7), () => i / 12));
  assert.ok([...seen].some((t) => t.includes("Morning") || t.includes("forge") || t.includes("coffee")));
  assert.ok(![...seen].some((t) => t.includes("Evening") || t.includes("midnight")));
});

test("the night window wraps past midnight", () => {
  const seen = new Set<string>();
  for (let i = 0; i < 12; i++) { seen.add(launcherTagline(at(23), () => i / 12)); seen.add(launcherTagline(at(2), () => i / 12)); }
  assert.ok([...seen].some((t) => t.includes("midnight") || t.includes("daemon never sleeps")));
});

test("the original tagline is still in the pool", () => {
  const seen = new Set<string>();
  for (let i = 0; i < 12; i++) seen.add(launcherTagline(at(13), () => i / 12));
  assert.ok(seen.has("See what magus is up to."));
});
