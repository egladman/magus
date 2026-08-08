// A source file carrying a build suffix, whose trimmed name (resolver_cache) is
// NOT itself a source file while a shorter prefix (resolver) is. That is the shape
// that regressed: trimming before the exact-pair lookup hides this file, and the
// trimmed name then falls through to the narrowing search and finds resolver.go.
package sprawl

func resolveCachedPureGo(s string) string { return Resolve(s) }
