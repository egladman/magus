package doctor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The registration is a command line, not a path, and vcs/git.go quotes the executable
// when it contains a space. Reading it back wrong would probe the wrong thing and report
// a working driver as broken, or the reverse.
func TestDriverExecutableUnwrapsTheRegistration(t *testing.T) {
	for _, tc := range []struct {
		name       string
		registered string
		want       string
	}{
		{"a plain path", "/usr/local/bin/magus vcs merge-driver %O %A %B %L %P", "/usr/local/bin/magus"},
		{"a quoted path with a space", `"/Users/a b/magus" vcs merge-driver %O %A`, "/Users/a b/magus"},
		{"a bare name from PATH", "magus vcs merge-driver %O", "magus"},
		{"no arguments at all", "/usr/local/bin/magus", "/usr/local/bin/magus"},
		{"an unterminated quote is not an executable", `"/Users/a b/magus vcs merge-driver`, ""},
		{"nothing registered", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, driverExecutable(tc.registered))
		})
	}
}

// A failing magus prints its diagnostic first and its usage after, and the diagnostic is
// the part that names the cause.
func TestFirstLineKeepsTheDiagnostic(t *testing.T) {
	assert.Equal(t, "[error] unknown option \"timeout\"",
		firstLine("\n[error] unknown option \"timeout\"\nUsage: magus ...\n"))
	assert.Empty(t, firstLine("   \n  "))
}
