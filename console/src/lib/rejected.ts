// A double for a failing platform call returns a rejected promise.
// The one Promise.reject below an entry lives here.

/** rejected is a promise that rejects with reason. */
export function rejected(reason: unknown): Promise<never> {
  return Promise.reject(reason);
}

/** Held is a pending promise and the function that rejects it. */
export type Held = { promise: Promise<never>; reject: (reason: unknown) => void };

/** held returns a promise that stays pending until its reject is called. */
export function held(): Held {
  let reject: (reason: unknown) => void = () => {};
  const promise = new Promise<never>((_, fail) => {
    reject = fail;
  });
  return { promise, reject };
}
