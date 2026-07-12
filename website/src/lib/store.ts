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
      for (const fn of listeners) fn(state);
    },
    subscribe(fn: Listener<T>): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}
