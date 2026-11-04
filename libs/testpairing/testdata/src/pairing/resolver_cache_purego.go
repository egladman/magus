// A source file carrying a build-looking suffix, whose trimmed name is NOT itself a
// source file while a shorter prefix (resolver) is. Trimming before the exact-pair
// lookup would hide this file and send its test to the narrowing search.
package pairing

func resolveCachedPureGo(s string) string { return Resolve(s) }
