// Package pycompat reproduces the CPython arithmetic the analysis pipeline was
// first written against, bit for bit: the Mersenne Twister behind random.Random
// and its seeding, math.fsum, float repr, and json.dumps with sort_keys. The
// pipeline's outputs are compared byte for byte against the Python's, so every
// place Go's own library would round or format differently goes through here.
package pycompat

import (
	"crypto/sha512"
	"math/bits"
)

const (
	mtN         = 624
	mtM         = 397
	mtMatrixA   = 0x9908b0df
	mtUpperMask = 0x80000000
	mtLowerMask = 0x7fffffff
)

// Random is CPython's random.Random: MT19937 with init_by_array seeding.
type Random struct {
	mt    [mtN]uint32
	index int
}

// newRandomInt seeds like random.Random(n) for an int n: the magnitude is cut
// into little-endian 32-bit words and fed to init_by_array.
func newRandomInt(seed int64) *Random {
	u := uint64(seed)
	if seed < 0 {
		u = uint64(-seed)
	}
	key := []uint32{uint32(u)}
	if hi := uint32(u >> 32); hi != 0 {
		key = append(key, hi)
	}
	return newRandom(key)
}

// NewRandomString seeds like random.Random(s) for a str s (seed version 2):
// the UTF-8 bytes followed by their SHA-512 digest, read as one big-endian
// integer, cut into little-endian 32-bit words with the high zero words dropped.
func NewRandomString(s string) *Random {
	b := []byte(s)
	sum := sha512.Sum512(b)
	b = append(b, sum[:]...)
	var key []uint32
	for end := len(b); end > 0; end -= 4 {
		start := max(end-4, 0)
		var w uint32
		for _, c := range b[start:end] {
			w = w<<8 | uint32(c)
		}
		key = append(key, w)
	}
	for len(key) > 1 && key[len(key)-1] == 0 {
		key = key[:len(key)-1]
	}
	return newRandom(key)
}

func newRandom(key []uint32) *Random {
	r := &Random{}
	r.mt[0] = 19650218
	for i := 1; i < mtN; i++ {
		r.mt[i] = 1812433253*(r.mt[i-1]^(r.mt[i-1]>>30)) + uint32(i)
	}
	i, j := 1, 0
	for k := max(mtN, len(key)); k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) * 1664525)) + key[j] + uint32(j)
		i++
		j++
		if i >= mtN {
			r.mt[0] = r.mt[mtN-1]
			i = 1
		}
		if j >= len(key) {
			j = 0
		}
	}
	for k := mtN - 1; k > 0; k-- {
		r.mt[i] = (r.mt[i] ^ ((r.mt[i-1] ^ (r.mt[i-1] >> 30)) * 1566083941)) - uint32(i)
		i++
		if i >= mtN {
			r.mt[0] = r.mt[mtN-1]
			i = 1
		}
	}
	r.mt[0] = 0x80000000
	r.index = mtN
	return r
}

func (r *Random) uint32() uint32 {
	if r.index >= mtN {
		r.twist()
	}
	y := r.mt[r.index]
	r.index++
	y ^= y >> 11
	y ^= (y << 7) & 0x9d2c5680
	y ^= (y << 15) & 0xefc60000
	y ^= y >> 18
	return y
}

func (r *Random) twist() {
	mag := func(y uint32) uint32 {
		if y&1 == 1 {
			return mtMatrixA
		}
		return 0
	}
	var kk int
	for ; kk < mtN-mtM; kk++ {
		y := (r.mt[kk] & mtUpperMask) | (r.mt[kk+1] & mtLowerMask)
		r.mt[kk] = r.mt[kk+mtM] ^ (y >> 1) ^ mag(y)
	}
	for ; kk < mtN-1; kk++ {
		y := (r.mt[kk] & mtUpperMask) | (r.mt[kk+1] & mtLowerMask)
		r.mt[kk] = r.mt[kk+(mtM-mtN)] ^ (y >> 1) ^ mag(y)
	}
	y := (r.mt[mtN-1] & mtUpperMask) | (r.mt[0] & mtLowerMask)
	r.mt[mtN-1] = r.mt[mtM-1] ^ (y >> 1) ^ mag(y)
	r.index = 0
}

// Float64 is random(): 53 bits from two draws, as CPython's random_random.
func (r *Random) Float64() float64 {
	a := float64(r.uint32() >> 5)
	b := float64(r.uint32() >> 6)
	return (a*67108864.0 + b) * (1.0 / 9007199254740992.0)
}

// getRandBits is getrandbits(k) for 0 <= k <= 64: whole 32-bit words are drawn
// low word first and a partial last word keeps its top bits.
func (r *Random) getRandBits(k int) uint64 {
	if k <= 0 {
		return 0
	}
	if k <= 32 {
		return uint64(r.uint32() >> (32 - k))
	}
	var out uint64
	for shift := 0; k > 0; shift += 32 {
		w := r.uint32()
		if k < 32 {
			w >>= 32 - k
		}
		out |= uint64(w) << shift
		k -= 32
	}
	return out
}

// RandRange is randrange(n) for n > 0: rejection sampling over bit_length(n)
// bits, so the draw sequence matches CPython's _randbelow_with_getrandbits.
func (r *Random) RandRange(n int) int {
	k := bits.Len(uint(n))
	v := r.getRandBits(k)
	for v >= uint64(n) {
		v = r.getRandBits(k)
	}
	return int(v)
}
