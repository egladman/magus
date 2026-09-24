package cache

import "path/filepath"

// stampAbsent is the digest of a stamp that does not exist, so a deleted stamp and an
// unreadable one both differ from any recorded content digest.
const stampAbsent = "absent"

// stampDigests reads each stamp's content digest, relative to root. nil for no stamps.
func stampDigests(root string, stamps []string) map[string]string {
	if len(stamps) == 0 {
		return nil
	}
	out := make(map[string]string, len(stamps))
	for _, s := range stamps {
		d, err := hashFile(filepath.Join(root, s))
		if err != nil {
			d = stampAbsent
		}
		out[s] = d
	}
	return out
}

// movedStamps returns the stamps whose digest now differs from the recorded one. A
// stamp the entry never recorded counts as moved: the entry cannot vouch for it.
func movedStamps(root string, stamps []string, recorded map[string]string) []string {
	var moved []string
	for s, d := range stampDigests(root, stamps) {
		if r, ok := recorded[s]; !ok || r != d || d == stampAbsent {
			moved = append(moved, s)
		}
	}
	return moved
}
