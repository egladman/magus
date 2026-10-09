package knowledge

// IsTestPath reports whether path is a test file, by the same rule the knowledge lenses
// apply to symbol sources. A trailing ":<line>" is ignored.
func IsTestPath(path string) bool { return isTestSource(path) }
