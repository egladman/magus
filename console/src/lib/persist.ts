// persist.ts - one typed, namespaced, cross-tab durable cell. Replaces the
// hand-rolled localStorage try/catch blocks that were duplicated across toc.ts,
// ref-drawer.ts, and settings.ts. A cell holds the current value in memory as the
// SYNCHRONOUS source of truth: get() reads it, and set()/update() mutate it and
// notify THIS tab's subscribers BEFORE any durable write is even enqueued - so a
// get() right after a set() is never stale. The durable mirror to a JSON-encoded
// "magus:" key is serialized through a single in-flight promise chain (per cell), so
// two writes can never interleave or clobber each other: each is queued in call order
// and runs one at a time, even if the durable store later becomes async (IndexedDB).
// Writes from OTHER tabs arrive via the storage event. When storage is unavailable
// (private mode, disabled) the cell still works in memory for the session; only
// durability across reloads is lost.
//
// Create one cell per key at module scope and import it. Cells live for the app
// lifetime and intentionally expose no teardown: their single storage listener is
// a fixed cost. A future per-view cell (e.g. a settings page rebuilt on navigation)
// would need an unsubscribe; today every cell is a singleton, so none is added.
export interface Persisted<T> {
  get(): T;
  set(value: T): void;
  // Atomic read-modify-write against the LIVE in-memory value: update((prev) => next).
  // Use this instead of get()+set() whenever the next value derives from the current
  // one (splicing one key of a map, incrementing a counter), so two updates in the same
  // tick compose instead of the second clobbering the first with a value read before the first ran.
  update(fn: (prev: T) => T): void;
  // Durably write `value` WITHOUT updating the in-memory `current` or notifying subscribers, so the
  // running session keeps its live value and the write only surfaces on the next load. Backs the
  // Settings surface's "Save" (commit without hot-reload). Cross-tab caveat: another tab's storage-event
  // listener will still pick this write up and go live there - acceptable, it matches localStorage semantics.
  persistOnly(value: T): void;
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

  // The durable write is serialized through one in-flight promise chain per cell: each
  // set() appends its write to writeChain, so the writes run in call order and never
  // interleave. current is updated + subscribers notified synchronously in set() BEFORE
  // the write is enqueued, so serializing the durable side never makes a read stale.
  // localStorage.setItem is synchronous today; the chain future-proofs a swap to an async
  // store without a rewrite, and makes "last write wins, in order" a guarantee not an accident.
  let writeChain: Promise<void> = Promise.resolve();
  const enqueueWrite = (value: T): void => {
    writeChain = writeChain.then(() => {
      try {
        localStorage.setItem(full, JSON.stringify(value));
      } catch {
        /* best-effort: keep the in-memory value */
      }
    });
  };

  // The browser fires `storage` on OTHER tabs when localStorage changes, so a
  // preference toggled in one tab propagates to the rest. Guarded for non-DOM
  // contexts (unit tests, SSR) where `window` is absent.
  if (typeof window !== "undefined") {
    window.addEventListener("storage", (e) => {
      if (e.key !== full) return;
      current = load();
      for (const fn of [...listeners]) fn(current);
    });
  }

  const set = (value: T): void => {
    current = value; // sync source of truth, updated BEFORE the durable write is enqueued
    for (const fn of [...listeners]) fn(value); // notify this tab immediately (storage fires only cross-tab)
    enqueueWrite(value); // serialized durable mirror
  };

  return {
    get: () => current,
    set,
    // Read-modify-write against the LIVE current, so consecutive updates compose. Delegates
    // to set() so the sync-notify + serialized-write invariants hold identically.
    update: (fn: (prev: T) => T): void => set(fn(current)),
    // Durable write only: reuse the serialized write chain but leave `current` and subscribers alone,
    // so this session never picks the value up (it lands on the next load).
    persistOnly: (value: T): void => enqueueWrite(value),
    subscribe(fn): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
