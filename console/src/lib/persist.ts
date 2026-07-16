// persist.ts - one typed, namespaced, cross-tab durable cell. Replaces the
// hand-rolled localStorage try/catch blocks that were duplicated across toc.ts,
// ref-drawer.ts, and settings.ts. A cell holds the current value in memory as the
// synchronous source of truth, mirrors it to a JSON-encoded "magus:" key, notifies
// THIS tab's subscribers on set, and picks up writes from OTHER tabs via the
// storage event. When storage is unavailable (private mode, disabled) the cell
// still works in memory for the session; only durability across reloads is lost.
//
// Create one cell per key at module scope and import it. Cells live for the app
// lifetime and intentionally expose no teardown: their single storage listener is
// a fixed cost. A future per-view cell (e.g. a settings page rebuilt on navigation)
// would need an unsubscribe; today every cell is a singleton, so none is added.
export interface Persisted<T> {
  get(): T;
  set(value: T): void;
  subscribe(fn: (value: T) => void): () => void; // returns an unsubscribe fn
}

// persisted<boolean> stores a bool, persisted<number> a number, etc. The
// `fallback` both seeds the value when nothing is stored and pins the type T.
export function persisted<T>(key: string, fallback: T): Persisted<T> {
  const NS = "magus:"; // namespace so keys never collide with other apps on the origin
  const full = NS + key;
  const listeners = new Set<(v: T) => void>();

  const load = (): T => {
    try {
      const raw = localStorage.getItem(full);
      return raw === null ? fallback : (JSON.parse(raw) as T);
    } catch {
      return fallback; // storage disabled or value corrupt: fall back to the default
    }
  };

  // In-memory value is the source of truth for get(), so the cell stays correct
  // even when the localStorage write below is a no-op (private mode / disabled).
  let current = load();

  // The browser fires `storage` on OTHER tabs when localStorage changes, so a
  // preference toggled in one tab propagates to the rest.
  window.addEventListener("storage", (e) => {
    if (e.key !== full) return;
    current = load();
    for (const fn of listeners) fn(current);
  });

  return {
    get: () => current,
    set(value: T): void {
      current = value;
      try { localStorage.setItem(full, JSON.stringify(value)); } catch { /* best-effort: keep the in-memory value */ }
      for (const fn of listeners) fn(value); // notify this tab immediately (storage fires only cross-tab)
    },
    subscribe(fn): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
