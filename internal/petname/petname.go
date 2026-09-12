// Package petname maps a seed to a short, memorable two-word name so a
// person recognizes the same job across the task list, the worktree and
// `ls jobs` by name, where an id like wave3/move is for scripts and
// depends_on.
package petname

import (
	"fmt"
	"hash/fnv"
)

// adjTag and nounTag domain-separate the two halves: each is hashed from a
// distinct input so growing one word list never perturbs the other half's
// index, unlike splitting bits or taking a quotient/remainder of one hash.
const (
	adjTag    = "petname:adjective:"
	nounTag   = "petname:noun:"
	suffixTag = "petname:suffix:"
)

const suffixAlphabet = "abcdefghijklmnopqrstuvwxyz"

// Of deterministically maps seed to a name of the form "adjective-noun".
// The same seed always yields the same name, on any machine, with no
// randomness or process state.
func Of(seed string) string {
	adj := adjectives[hash(adjTag+seed)%uint64(len(adjectives))]
	noun := nouns[hash(nounTag+seed)%uint64(len(nouns))]
	return adj + "-" + noun
}

// OfWithSuffix returns Of(seed) with a two-letter suffix appended, for
// breaking a collision with a name already live in the caller's own store;
// the caller retries with increasing n (0, 1, 2, ...) until the result no
// longer collides. This package holds no store and never checks for a
// collision itself.
func OfWithSuffix(seed string, n int) string {
	h := hash(fmt.Sprintf("%s%s#%d", suffixTag, seed, n))
	const base = uint64(len(suffixAlphabet))
	c1 := suffixAlphabet[h%base]
	c2 := suffixAlphabet[(h/base)%base]
	return fmt.Sprintf("%s-%c%c", Of(seed), c1, c2)
}

// hash picks fnv-1a 64-bit: a stdlib, allocation-free, non-cryptographic
// hash that is more than sufficient to distribute short seed strings across
// two word lists with no security property required.
func hash(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s)) // hash.Hash.Write never returns an error
	return h.Sum64()
}
