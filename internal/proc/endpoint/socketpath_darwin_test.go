//go:build darwin

package endpoint

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// darwin has no way to reach a directory by a shorter name, so a path past sun_path is
// refused by name, length and limit before any bind or connect.
func TestAPathLongerThanSocketPathIsRefusedOnDarwin(t *testing.T) {
	path := filepath.Join("/tmp", strings.Repeat("d", 100), "s.sock")
	ep := Endpoint{Scheme: "unix", Addr: path}
	want := fmt.Sprintf("endpoint: unix socket path %s is 112 bytes, and darwin holds at most 103", path)
	_, err := ep.Listen()
	assert.EqualError(t, err, want)
	_, err = ep.Dial(t.Context())
	assert.EqualError(t, err, want)
}
