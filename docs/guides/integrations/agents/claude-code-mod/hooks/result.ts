// A copy of console/src/lib/result.ts: Claude Code's engine loads the mod from its own directory,
// so it cannot import the console's source.

// Result is how a fallible call reports failure: the caller either reads the value or handles
// the error, and nothing unwinds past it.
export type Result<T> = { readonly ok: true; readonly value: T } | { readonly ok: false; readonly error: Error }

export function succeed<T>(value: T): Result<T> {
  return { ok: true, value }
}

export function fail<T = never>(message: string): Result<T> {
  return { ok: false, error: new Error(message) }
}

// failFrom carries an existing error, or whatever a boundary caught, keeping an Error as it is.
export function failFrom<T = never>(error: unknown): Result<T> {
  return { ok: false, error: error instanceof Error ? error : new Error(String(error)) }
}

// parseJson is JSON.parse with its SyntaxError as a failed Result.
export function parseJson(text: string): Result<unknown> {
  try {
    return succeed(JSON.parse(text))
  } catch (e) {
    return failFrom(e)
  }
}

// isRecord narrows a parsed JSON value to an object whose fields can be read.
export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}
