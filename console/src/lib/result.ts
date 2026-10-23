// Result is how a fallible call reports failure: the caller either reads the value or handles
// the error, and nothing unwinds past it.
export type Result<T> =
  | { readonly ok: true; readonly value: T }
  | { readonly ok: false; readonly error: Error };

export function succeed<T>(value: T): Result<T> {
  return { ok: true, value };
}

export function fail<T = never>(message: string): Result<T> {
  return { ok: false, error: new Error(message) };
}

// failFrom carries an existing error, or whatever a boundary caught, keeping an Error (and its
// subclass) as it is.
export function failFrom<T = never>(error: unknown): Result<T> {
  return { ok: false, error: asError(error) };
}

// asError wraps a caught value, which is `unknown`, so a boundary can hand it on as an Error.
export function asError(caught: unknown): Error {
  return caught instanceof Error ? caught : new Error(String(caught));
}
