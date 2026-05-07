package buzz

// testConformanceMeta holds the directives parsed from a conformance fixture.
// Shared by bytecode_test.go and conformance_test.go (package buzz_test version).
type testConformanceMeta struct {
	expect string
	errStr string
	skip   string
}

// parseConformanceMeta reads the leading comment block of src for @expect,
// @error, and @skip directives. Used by both the conformance and bytecode
// round-trip tests.
func parseConformanceMeta(src string) testConformanceMeta {
	var m testConformanceMeta
	for _, line := range splitLines(src) {
		if len(line) == 0 || line[0] != '/' {
			break
		}
		rest := line[2:] // strip "//"
		if len(rest) > 0 && rest[0] == ' ' {
			rest = rest[1:]
		}
		if v, ok := cutPrefix(rest, "@expect:"); ok {
			m.expect = trimSpace(v)
		} else if v, ok := cutPrefix(rest, "@error:"); ok {
			m.errStr = trimSpace(v)
		} else if v, ok := cutPrefix(rest, "@skip:"); ok {
			m.skip = trimSpace(v)
		}
	}
	return m
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

func cutPrefix(s, prefix string) (string, bool) {
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):], true
	}
	return s, false
}

// containsStdImport reports whether src contains any `import "<module>"` for
// the Buzz standard library modules that require buzzstd.Register to resolve.
func containsStdImport(src string) bool {
	stdModules := []string{`"std"`, `"math"`, `"fs"`, `"os"`, `"crypto"`, `"gc"`, `"debug"`, `"io"`, `"serialize"`, `"buffer"`, `"ffi"`}
	for _, mod := range stdModules {
		if contains(src, "import "+mod) {
			return true
		}
	}
	return false
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func trimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\r' || s[i] == '\n') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\r' || s[j-1] == '\n') {
		j--
	}
	return s[i:j]
}
