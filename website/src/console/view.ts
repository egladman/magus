// view.ts - the magus console's tiny reactive-DOM layer. Zero dependencies, owned in full. NOT a
// framework: a fixed, minimal set of primitives (Signal, signal, bind, scope, h) plus the house
// rules in the console-view-layer spec. Real DOM + surgical updates - no VDOM, no template DSL, no
// hidden dependency graph. Reactivity is EXPLICIT: a component binds a specific node to a signal via
// bind(), and collects its disposers in a scope() that its teardown disposes. Keep this file tight
// (target < 120 lines); crossing that is a signal to remove something, not to keep growing it.

// --- Signal: the reactive cell ------------------------------------------------
// A value you can read, write, and subscribe to. lib/persist.ts's persisted<T> already implements
// this exact shape, so a durable cell IS a Signal - this interface just names it, so bind() works
// over both in-memory and persisted state uniformly.
export interface Signal<T> {
  get(): T;
  set(v: T): void;
  subscribe(fn: (v: T) => void): () => void; // returns an unsubscribe
}

// signal creates an in-memory reactive cell. Notification is synchronous (predictable; no
// scheduler). Setting a value always notifies; a caller that wants change-only can compare first.
export function signal<T>(initial: T): Signal<T> {
  let value = initial;
  const listeners = new Set<(v: T) => void>();
  return {
    get: () => value,
    set(v: T): void {
      value = v;
      for (const fn of [...listeners]) fn(v); // copy so an unsubscribe during notify is safe
    },
    subscribe(fn): () => void {
      listeners.add(fn);
      return () => listeners.delete(fn);
    },
  };
}

// --- bind: connect a signal to an effect --------------------------------------
export type Disposer = () => void;

// bind runs apply(sig.get()) once now, then on every change; the DOM mutation / formatting lives in
// apply, e.g. bind(count, (n) => { label.textContent = String(n); }). Returns the unsubscribe. This
// is the ONLY reactive primitive components use directly - deliberately explicit.
export function bind<T>(sig: Signal<T>, apply: (v: T) => void): Disposer {
  apply(sig.get());
  return sig.subscribe(apply);
}

// --- scope: lifecycle collection ----------------------------------------------
// A component collects its bind disposers (and any timers/observers) into a scope; its
// destroy()/deactivate() calls dispose(), so a closing tab/pane leaks nothing.
export interface Scope {
  add(d: Disposer): void;
  dispose(): void;
}

export function scope(): Scope {
  const disposers: Disposer[] = [];
  return {
    add(d: Disposer): void { disposers.push(d); },
    dispose(): void { for (const d of disposers.splice(0)) d(); },
  };
}

// --- h: the DOM builder -------------------------------------------------------
// A plain, static element builder (re-homed from dashboard/tiles/card.ts, which now re-exports it).
// Reactivity is added AFTER via bind(), never inside h() - so a reader sees exactly what updates.
// Typed to the element so callers get the right HTMLElement subtype.
export function h<K extends keyof HTMLElementTagNameMap>(
  tag: K, className?: string, text?: string,
): HTMLElementTagNameMap[K] {
  const e = document.createElement(tag);
  if (className) e.className = className;
  if (text != null) e.textContent = text;
  return e;
}
