// errMessage / errName narrow an unknown caught value (catch clauses are `unknown`
// under strict TS) to the fields error-reporting code reads.
export function errMessage(e: unknown): string {
  if (e instanceof Error) return e.message;
  if (e && typeof e === "object" && "message" in e) return String(e.message);
  return String(e);
}
export function errName(e: unknown): string {
  return e && typeof e === "object" && "name" in e ? String(e.name) : "";
}
