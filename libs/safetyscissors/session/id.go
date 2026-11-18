package session

import "fmt"

// InvalidIDError is a name [ValidFileID] refuses where it would name a store file: an
// invocation id, or the kind a fold cache is filed under. Field says which.
type InvalidIDError struct {
	Field string
	ID    string
}

func (e *InvalidIDError) Error() string {
	return fmt.Sprintf("session: %s %q must be alphanumeric with hyphen and underscore, since it names a store file", e.Field, e.ID)
}

// ValidFileID reports whether id can name a store file: an invocation id, or the host
// session id a load files under. A loader can refuse a host id with it before writing
// half a stream. The rule is the file name's, which is why it is strict: an id that could
// contain a separator could escape the store directory. It is an ASCII letter or digit
// followed by letters, digits, hyphens and underscores.
//
// Minting an id is the caller's: the package keeps no process state to make one unique.
//
// journal.ValidInvocationID is a copy of this loop: it judges only invocation
// ids, and this one also judges host session ids and fold kinds.
func ValidFileID(id string) bool {
	if id == "" || !isAlnum(id[0]) {
		return false
	}
	for i := 1; i < len(id); i++ {
		if c := id[i]; !isAlnum(c) && c != '-' && c != '_' {
			return false
		}
	}
	return true
}

func isAlnum(c byte) bool {
	return ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9')
}
