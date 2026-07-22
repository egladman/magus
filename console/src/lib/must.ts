// must asserts a value is present, returning it non-null or throwing a clear error.
// It replaces a `!` non-null assertion at a site where the value is known to exist:
// same intent (proceed as non-null), but a violated assumption throws a named error
// instead of a downstream `undefined` access.
export function must<T>(value: T | null | undefined): T {
  if (value === null || value === undefined) {
    throw new Error("must: a required value was null or undefined");
  }
  return value;
}

// errMessage / errName narrow an unknown caught value (catch clauses are `unknown`
// under strict TS) to the fields error-reporting code reads, without an `any` cast.
export function errMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (e && typeof e === "object" && "message" in e)
    return String((e as { message: unknown }).message);
  return String(e);
}
export function errName(e: unknown): string {
  return e && typeof e === "object" && "name" in e ? String((e as { name: unknown }).name) : "";
}
