import test from "node:test";
import assert from "node:assert/strict";
import { launcherTagline, launcherTitle } from "./home";

// launcherTagline picks a time-eligible tagline. These pin the hour-window gating (including the
// midnight-wrapping night window) and that a choice is always available at every hour.

// 2026-01-01 is a Thursday, so `at` is a plain weekday unless a test says otherwise.
const at = (hour: number): Date => new Date(2026, 0, 1, hour, 0, 0);
// 2026-01-03 is a Saturday, 2026-01-05 a Monday, 2026-01-02 a Friday.
const onDay = (day: number, hour: number): Date => new Date(2026, 0, day, hour, 0, 0);

// Draw densely enough to reach every entry in the eligible pool, whatever its size - a fixed draw
// count silently stops covering the tail as lines are added.
const poolAt = (d: Date): Set<string> => {
  const seen = new Set<string>();
  for (let i = 0; i < 400; i++) seen.add(launcherTagline(d, () => i / 400));
  return seen;
};

test("an eligible tagline exists at every hour of every weekday", () => {
  // Both gates at once: a narrow hour window crossed with a narrow day set could otherwise leave
  // some slot with an empty pool, which reads as a crash (undefined.text) rather than a dull line.
  for (let day = 1; day <= 7; day++) {
    for (let h = 0; h < 24; h++) {
      const t = launcherTagline(onDay(day, h), () => 0);
      assert.ok(t.length > 0, `day ${day} hour ${h} produced an empty tagline`);
    }
  }
});

test("morning window surfaces a morning line, not an evening one", () => {
  // pick=0 selects the first eligible entry; the ANY_HOURS pool leads, so scan the whole eligible set.
  const seen = poolAt(at(7));
  assert.ok(
    [...seen].some((t) => t.includes("Morning") || t.includes("forge") || t.includes("coffee")),
  );
  assert.ok(![...seen].some((t) => t.includes("Evening") || t.includes("midnight")));
});

test("the night window wraps past midnight", () => {
  const seen = new Set([...poolAt(at(23)), ...poolAt(at(2))]);
  assert.ok([...seen].some((t) => t.includes("midnight") || t.includes("daemon never sleeps")));
});

test("the original tagline is still in the pool", () => {
  const seen = poolAt(at(13));
  assert.ok(seen.has("See what magus is up to."));
});

// launcherTitle rotates the launcher heading. Unlike the taglines it is not time-gated, so the only
// contract is that every entry is reachable and none is empty.

test("launcherTitle reaches more than one heading and never returns empty", () => {
  const seen = new Set<string>();
  for (let i = 0; i < 24; i++) {
    const t = launcherTitle(() => i / 24);
    assert.ok(t.length > 0, `pick ${i} produced an empty title`);
    seen.add(t);
  }
  assert.ok(seen.size > 1, "the heading never varied");
  assert.ok(seen.has("What do you want to open?"), "the original heading left the pool");
});

test("launcherTitle stays in range at the pick boundaries", () => {
  assert.ok(launcherTitle(() => 0).length > 0);
  // Math.random() is [0,1), so this is the largest value the default ever yields.
  assert.ok(launcherTitle(() => 0.999999).length > 0);
});

// The day gate is a second axis, not a relabeling of the hour gate: a weekend line must be
// unreachable midweek, and a weekday-only line unreachable at the weekend.
test("day-gated taglines only appear on their days", () => {
  const saturday = poolAt(onDay(3, 14));
  const thursday = poolAt(at(14));

  assert.ok([...saturday].some((t) => t.includes("Weekend") || t.includes("weekend")));
  assert.ok(
    ![...thursday].some((t) => t.includes("Weekend") || t.includes("weekend")),
    "a weekend line reached a Thursday",
  );

  assert.ok([...poolAt(onDay(5, 8))].some((t) => t.includes("Monday")));
  assert.ok(![...thursday].some((t) => t.includes("Monday")), "a Monday line reached a Thursday");
  assert.ok([...poolAt(onDay(2, 18))].some((t) => t.includes("Friday")));
});
