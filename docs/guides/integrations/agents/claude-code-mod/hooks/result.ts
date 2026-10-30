// A copy of docs/src/gen/magus/types.ts's Result, less ConnectError.
// Claude Code loads the mod from its own directory, so it cannot import it.

export interface Diagnostic {
  code: string
  message: string
  url?: string
  fix?: string
}

export class MagusError extends Error implements Diagnostic {
  readonly code: string
  readonly url?: string
  readonly fix?: string

  constructor(d: Diagnostic, options?: ErrorOptions) {
    super(d.message, options)
    this.name = 'MagusError'
    this.code = d.code
    if (d.url !== undefined) this.url = d.url
    if (d.fix !== undefined) this.fix = d.fix
  }
}

export type Result<T, E extends Error = MagusError> = { value: T; error?: undefined } | { value?: undefined; error: E }

export function ok<T>(value: T): Result<T, never> {
  return { value }
}

export function fail<E extends Error>(error: E): Result<never, E> {
  return { error }
}

export function attempt<T>(fn: () => T): Result<T> {
  try {
    return ok(fn())
  } catch (e) {
    return fail(toMagusError(e))
  }
}

export async function fromPromise<T>(p: Promise<T>): Promise<Result<T>> {
  try {
    return ok(await p)
  } catch (e) {
    return fail(toMagusError(e))
  }
}

export function parseJSON(text: string): Result<unknown> {
  return attempt((): unknown => JSON.parse(text))
}

function toMagusError(e: unknown): MagusError {
  if (e instanceof MagusError) return e
  const message = e instanceof Error ? e.message : String(e)
  return new MagusError({ code: '', message }, { cause: e })
}

// isRecord narrows a parsed JSON value to an object whose fields can be read.
export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === 'object' && v !== null && !Array.isArray(v)
}
