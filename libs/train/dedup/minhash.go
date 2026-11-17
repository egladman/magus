package dedup

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash/fnv"
	"math/rand/v2"
	"slices"
	"strings"
)

// The parameters stage S1 fixes: 128 permutations, 5-token shingles, 32 bands of 4 rows,
// Jaccard at or above 0.85. perms must equal bands*rows.
const (
	Perms     = 128
	Shingle   = 5
	Bands     = 32
	Rows      = 4
	Threshold = 0.85

	// seed fixes the permutations. Changing it changes every signature, so the split
	// digest moves with it.
	seed uint64 = 0x636f64656d6f6465
)

var perms = func() [Perms][2]uint64 {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var p [Perms][2]uint64
	for i := range p {
		p[i] = [2]uint64{rng.Uint64() | 1, rng.Uint64()}
	}
	return p
}()

// Normalize is the bytes the exact dedup hashes: CRLF read as LF, trailing blanks off
// every line, and no blank lines at the end.
func Normalize(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// ExactKey is the sha256 of the normalized bytes, in hex.
func ExactKey(src string) string {
	sum := sha256.Sum256([]byte(Normalize(src)))
	return hex.EncodeToString(sum[:])
}

// Shingles is the set of hashes of every run of Shingle consecutive tokens, sorted and
// unique. A file with fewer tokens has none and takes part in exact dedup only.
func Shingles(tokens []string) []uint64 {
	if len(tokens) < Shingle {
		return nil
	}
	hashes := make([]uint64, len(tokens))
	for i, t := range tokens {
		h := fnv.New64a()
		_, _ = h.Write([]byte(t))
		hashes[i] = h.Sum64()
	}
	out := make([]uint64, 0, len(tokens)-Shingle+1)
	for i := 0; i+Shingle <= len(hashes); i++ {
		var h uint64 = 14695981039346656037
		for _, x := range hashes[i : i+Shingle] {
			h = (h ^ x) * 1099511628211
			h ^= h >> 29
		}
		out = append(out, h)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Signature is the MinHash of a shingle set: per permutation, the least permuted hash.
func Signature(shingles []uint64) [Perms]uint64 {
	var sig [Perms]uint64
	for i := range sig {
		sig[i] = ^uint64(0)
	}
	for _, s := range shingles {
		for i, p := range perms {
			if v := p[0]*s + p[1]; v < sig[i] {
				sig[i] = v
			}
		}
	}
	return sig
}

// BandKeys is the Bands LSH keys of a signature: the hash of each run of Rows values,
// tagged by its band so equal runs in different bands do not meet.
func BandKeys(sig [Perms]uint64) [Bands]uint64 {
	var keys [Bands]uint64
	var buf [8]byte
	for b := range keys {
		h := fnv.New64a()
		binary.LittleEndian.PutUint64(buf[:], uint64(b))
		_, _ = h.Write(buf[:])
		for _, v := range sig[b*Rows : (b+1)*Rows] {
			binary.LittleEndian.PutUint64(buf[:], v)
			_, _ = h.Write(buf[:])
		}
		keys[b] = h.Sum64()
	}
	return keys
}

// Jaccard is |a∩b| / |a∪b| over two sorted unique shingle sets.
func Jaccard(a, b []uint64) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	i, j, both := 0, 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			both++
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return float64(both) / float64(len(a)+len(b)-both)
}
