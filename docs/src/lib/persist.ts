// persist.ts - one typed, namespaced, cross-tab durable cell. Replaces the
// hand-rolled localStorage try/catch blocks that were duplicated across toc.ts,
// ref-drawer.ts, and settings.ts. A cell reads/writes a JSON-encoded value under
// a "magus:" key, notifies THIS tab's subscribers synchronously on set, and
// mirrors writes from OTHER tabs via the storage event. When storage is
// unavailable (private mode, disabled) it degrades to the in-memory fallback.
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

  const read = (): T => {
    try {
      const raw = localStorage.getItem(full);
      // Trusts the stored shape is still T; callers of non-primitive cells must re-validate
      // on read (settings.ts clamps its numbers). Corrupt JSON falls through to the catch.
      return raw === null ? fallback : (JSON.parse(raw) as T);
    } catch {
      return fallback; // storage disabled or value corrupt: fall back to the default
    }
  };

  // The browser fires `storage` on OTHER tabs when localStorage changes, so a
  // preference toggled in one tab propagates to the rest.
  window.addEventListener("storage", (e) => {
    if (e.key === full) for (const fn of listeners) fn(read());
  });

  return {
    get: read,
    set(value: T): void {
      try { localStorage.setItem(full, JSON.stringify(value)); } catch { /* ignore */ }
      for (const fn of listeners) fn(value); // notify this tab immediately (storage fires only cross-tab)
    },
    subscribe(fn): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
