// unreachable ends a branch the type system proves cannot run. It is the one
// sanctioned throw below a program's entry (hack/lint/no-throw.buzz).
//
// A switch over a union or an enum names every member, has no default arm, and
// is followed by `return unreachable(x)`: tsc then fails at the call when a
// member is missing, because x is no longer `never`.
export function unreachable(x: never): never {
  throw new Error(`unreachable: ${String(x)}`);
}
