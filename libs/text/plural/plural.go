package plural

import "strconv"

// Word returns one when n is 1 and many otherwise, so 0 is plural. The caller
// spells both forms; nothing derives "es" or "ies" from a noun.
func Word(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Count returns n and Word(n, one, many) separated by a space: "1 file",
// "3 files".
func Count(n int, one, many string) string {
	return strconv.Itoa(n) + " " + Word(n, one, many)
}
