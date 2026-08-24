//go:build linux

package mem

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadCgroupLimit(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
		return path
	}

	assert.Equal(t, int64(2147483648), readCgroupLimit(write("v2", "2147483648\n")))
	assert.Equal(t, int64(0), readCgroupLimit(write("unlimited", "max\n")),
		"cgroup v2 spells unlimited as max")
	assert.Equal(t, int64(0), readCgroupLimit(filepath.Join(dir, "absent")),
		"a host outside a container has no file to read")
	assert.Equal(t, int64(0), readCgroupLimit(write("junk", "not-a-number\n")))
	assert.Equal(t, int64(0), readCgroupLimit(write("zero", "0\n")))

	// cgroup v1's unlimited sentinel varies by kernel and page size, so it is not
	// matched here. UsableBytes discards it by taking the smaller of the two.
	assert.Equal(t, int64(9223372036854771712), readCgroupLimit(write("v1max", "9223372036854771712\n")))
}
