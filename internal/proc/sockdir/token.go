package sockdir

import (
	"errors"
	"fmt"
	"os"
)

// TokenPath is where the token for the socket at sock is kept: beside it, in the same
// private directory, so a client that found the socket can present it.
func TokenPath(sock string) string { return sock + ".token" }

// WriteToken publishes token for the socket at sock, readable by this user alone. A file
// left by a process that crashed is replaced; O_EXCL after the remove refuses to follow
// anything planted at the path in between.
func WriteToken(sock, token string) error {
	path := TokenPath(sock)
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("replace token %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	_, werr := f.WriteString(token)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write token %s: %w", path, werr)
	}
	return nil
}
