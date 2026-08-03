// store.ts - a tiny reactive store (a single signal). Streams write; tiles subscribe.
//
// One value of type T is held. set() shallow-merges a partial and notifies every
// subscriber with the new whole value. There is no batching or diffing here - the
// dashboard ticks about once a second, so a straight fan-out is plenty, and each
// tile decides for itself which slice it re-renders. subscribe() returns an
// unsubscribe function and does NOT fire immediately (call get() for the seed).

export type Listener<T> = (state: T) => void;

export interface Store<T> {
  get(): T;
  set(patch: Partial<T>): void;
  subscribe(fn: Listener<T>): () => void;
}

export function createStore<T extends object>(initial: T): Store<T> {
  let state = initial;
  const listeners = new Set<Listener<T>>();
  return {
    get: () => state,
    set(patch: Partial<T>): void {
      state = { ...state, ...patch };
      // Snapshot, and isolate each listener. Iterating the live Set lets a subscriber that
      // unsubscribes mid-notify change the set being walked, and one THROWING subscriber
      // would abandon the loop - so every tile registered after the bad one silently stops
      // receiving updates, with nothing on screen to say why. The dashboard fans one tick
      // out to every tile, so a single broken tile must not take the board down with it.
      // persist.ts already snapshots this way.
      for (const fn of [...listeners]) {
        try {
          fn(state);
        } catch (err) {
          console.error("store: a subscriber threw; the remaining subscribers still ran", err);
        }
      }
    },
    subscribe(fn: Listener<T>): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
