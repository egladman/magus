import test from "node:test";
import assert from "node:assert/strict";
import { createNotificationStore } from "./notifications";

// These pin the pure store: admission, dedupe, the error-only unseen-dot logic, and the mutators. The
// bell/drawer DOM and the cross-bundle event plumbing are exercised in the browser, not here.

// A notification cannot be constructed without a `source` - it is a REQUIRED field, enforced at the type
// level (so a new caller cannot forget where a signal came from). This is a compile-time guarantee, so
// the check lives here as a @ts-expect-error the typecheck (tsc) verifies; the runtime call is dead.
test("source is type-level required", () => {
  const s = createNotificationStore();
  // @ts-expect-error - `source` is required; omitting it must not typecheck.
  const bad = () => s.notify({ message: "no source" });
  assert.equal(typeof bad, "function");
});

test("records newest-first and defaults to ok kind (history tier)", () => {
  const s = createNotificationStore();
  s.notify({ source: "Dashboard", message: "first", at: 1 });
  s.notify({ source: "Dashboard", message: "second", at: 2 });
  const list = s.list();
  assert.equal(list.length, 2);
  assert.equal(list[0].message, "second"); // newest first
  assert.equal(list[1].message, "first");
  assert.equal(list[0].kind, "ok"); // default
  assert.equal(list[0].source, "Dashboard");
});

test("only error-kind counts toward the unseen-dot; ok/warn record silently", () => {
  const s = createNotificationStore();
  s.notify({ source: "Settings", message: "ok", kind: "ok" });
  s.notify({ source: "Log Viewer", message: "warn", kind: "warn" });
  assert.equal(s.unseenCount(), 0, "ok/warn never light the dot");
  s.notify({ source: "Dashboard", message: "boom", kind: "error" });
  assert.equal(s.unseenCount(), 1, "error lights the dot");
  s.notify({ source: "Dashboard", message: "boom2", kind: "error" });
  assert.equal(s.unseenCount(), 2);
});

test("error entries start unseen; ok/warn start seen", () => {
  const s = createNotificationStore();
  const ok = s.notify({ source: "Settings", message: "ok", kind: "ok" });
  const err = s.notify({ source: "Dashboard", message: "boom", kind: "error" });
  assert.equal(ok?.seen, true);
  assert.equal(err?.seen, false);
});

test("dedupe: a repeated key is admitted once (only notify on transition)", () => {
  const s = createNotificationStore();
  const first = s.notify({ source: "Dashboard", message: "degraded", kind: "error", key: "dash:health:warn" });
  const second = s.notify({ source: "Dashboard", message: "degraded again", kind: "error", key: "dash:health:warn" });
  assert.ok(first);
  assert.equal(second, null, "same key is deduped away");
  assert.equal(s.list().length, 1);
});

test("dedupe survives a dismiss: a trimmed transition does not re-fire", () => {
  const s = createNotificationStore();
  const n = s.notify({ source: "Log Viewer", message: "fail", kind: "error", key: "fail:refABC" });
  s.dismiss(n!.id);
  assert.equal(s.list().length, 0);
  const again = s.notify({ source: "Log Viewer", message: "fail", kind: "error", key: "fail:refABC" });
  assert.equal(again, null, "the key is remembered even after the entry is gone");
});

test("no key means no dedupe (identical toasts both record)", () => {
  const s = createNotificationStore();
  s.notify({ source: "Settings", message: "same", kind: "ok" });
  s.notify({ source: "Settings", message: "same", kind: "ok" });
  assert.equal(s.list().length, 2);
});

test("markAllSeen clears the unseen count", () => {
  const s = createNotificationStore();
  s.notify({ source: "Dashboard", message: "boom", kind: "error" });
  s.notify({ source: "Dashboard", message: "boom2", kind: "error" });
  assert.equal(s.unseenCount(), 2);
  s.markAllSeen();
  assert.equal(s.unseenCount(), 0);
});

test("dismiss removes one; clear removes all", () => {
  const s = createNotificationStore();
  const a = s.notify({ source: "Share", message: "a" });
  s.notify({ source: "Share", message: "b" });
  s.dismiss(a!.id);
  assert.deepEqual(s.list().map((n) => n.message), ["b"]);
  s.clear();
  assert.equal(s.list().length, 0);
});

test("link normalization: bare href, full link, and empty", () => {
  const s = createNotificationStore();
  const bare = s.notify({ source: "Dashboard", message: "a", link: "../logs/#ref=x" });
  const full = s.notify({ source: "Dashboard", message: "b", link: { label: "Open logs", href: "../logs/#ref=y" } });
  const empty = s.notify({ source: "Dashboard", message: "c", link: "" });
  assert.deepEqual(bare?.link, { label: "Open", href: "../logs/#ref=x" });
  assert.deepEqual(full?.link, { label: "Open logs", href: "../logs/#ref=y" });
  assert.equal(empty?.link, undefined);
});

test("subscribe fires on change and unsubscribe stops it", () => {
  const s = createNotificationStore();
  let hits = 0;
  const off = s.subscribe(() => { hits++; });
  s.notify({ source: "Dashboard", message: "a" });
  assert.equal(hits, 1);
  s.dismiss(s.list()[0].id);
  assert.equal(hits, 2);
  off();
  s.notify({ source: "Dashboard", message: "b" });
  assert.equal(hits, 2, "no more callbacks after unsubscribe");
});
