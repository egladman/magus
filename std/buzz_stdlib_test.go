package std

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuzzStdlibEquiv(t *testing.T) {
	got, ok := BuzzStdlibEquiv("fs", "mkdir_all")
	assert.True(t, ok)
	assert.Equal(t, "fs.makeDirectory", got)

	// os.exit, os.sleep and crypto.*_file are deliberately absent: the Buzz stdlib
	// call does something materially different in each case.
	for _, tc := range [][2]string{{"os", "exit"}, {"os", "sleep"}, {"crypto", "sha256_file"}, {"fs", "no_such_method"}} {
		got, ok := BuzzStdlibEquiv(tc[0], tc[1])
		assert.Falsef(t, ok, "BuzzStdlibEquiv(%q, %q)", tc[0], tc[1])
		assert.Empty(t, got)
	}
}
