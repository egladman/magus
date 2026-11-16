package session

import (
	"errors"
	"regexp"
)

// idRE is the shape of an id that names a store file: it must not be able to escape the
// store directory.
var idRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)

// ErrInvalidID is an id [ValidFileID] refuses.
var ErrInvalidID = errors.New("an id must be alphanumeric with hyphen and underscore, since it names the invocation's file")

// ValidFileID reports whether id can name a store file: an invocation id, or the host
// session id a load files under. A loader can refuse a host id with it before writing
// half a stream. The rule is the file name's, which is why it is strict: an id that could
// contain a separator could escape the store directory.
//
// Minting an id is the caller's: the package keeps no process state to make one unique.
func ValidFileID(id string) bool { return idRE.MatchString(id) }
