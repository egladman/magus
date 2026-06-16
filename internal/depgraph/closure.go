package depgraph

import "math/bits"

// bitClosure holds forward and reverse transitive-closure bitsets.
// fwd: v can reach w; rev: transpose of fwd. Memory: 2*n*ceil(n/64)*8 bytes.
type bitClosure struct {
	n     int32
	words int32    // ceil(n/64)
	fwd   []uint64 // n*words
	rev   []uint64 // n*words
}

// buildClosure builds forward and reverse closures in reverse-topo/forward-topo sweeps.
func buildClosure(g *Graph) *bitClosure {
	n := g.n
	words := (n + 63) / 64
	c := &bitClosure{
		n:     n,
		words: words,
		fwd:   make([]uint64, int(n)*int(words)),
		rev:   make([]uint64, int(n)*int(words)),
	}

	for i := int(n) - 1; i >= 0; i-- {
		v := g.topo[i]
		row := c.fwdRow(v)
		row[v/64] |= 1 << (v % 64) // include self
		lo, hi := g.fwdOff[v], g.fwdOff[v+1]
		for k := lo; k < hi; k++ {
			w := ID(g.fwdT[k])
			wRow := c.fwdRow(w)
			for j := int32(0); j < words; j++ {
				row[j] |= wRow[j]
			}
		}
	}

	for i := 0; i < int(n); i++ {
		v := g.topo[i]
		row := c.revRow(v)
		row[v/64] |= 1 << (v % 64) // include self
		lo, hi := g.revOff[v], g.revOff[v+1]
		for k := lo; k < hi; k++ {
			u := ID(g.revT[k])
			uRow := c.revRow(u)
			for j := int32(0); j < words; j++ {
				row[j] |= uRow[j]
			}
		}
	}

	return c
}

func (c *bitClosure) fwdRow(v ID) []uint64 {
	base := int(v) * int(c.words)
	return c.fwd[base : base+int(c.words)]
}

func (c *bitClosure) revRow(v ID) []uint64 {
	base := int(v) * int(c.words)
	return c.rev[base : base+int(c.words)]
}

// Reachable reports whether u can reach v following forward edges.
func (c *bitClosure) Reachable(u, v ID) bool {
	row := c.fwdRow(u)
	return row[v/64]>>(v%64)&1 == 1
}

// ReverseClosure appends all nodes that can reach any seed to dst; out-of-range seeds dropped.
func (c *bitClosure) ReverseClosure(dst []ID, seeds []ID, n int32) []ID {
	words := int(c.words)
	mask := make([]uint64, words)
	for _, s := range seeds {
		if s < 0 || int32(s) >= n {
			continue
		}
		sRow := c.revRow(s)
		for j := 0; j < words; j++ {
			mask[j] |= sRow[j]
		}
	}
	for v := ID(0); int(v) < int(c.n); v++ {
		if mask[v/64]>>(v%64)&1 == 1 {
			dst = append(dst, v)
		}
	}
	return dst
}

// BlastRadius returns, for each node, the count of nodes that can transitively reach it.
func (c *bitClosure) BlastRadius() []int32 {
	n := int(c.n)
	out := make([]int32, n)
	for v := 0; v < n; v++ {
		row := c.revRow(ID(v))
		cnt := 0
		for _, w := range row {
			cnt += bits.OnesCount64(w)
		}
		out[v] = int32(cnt)
	}
	return out
}

// CCD returns the Cumulative Component Dependency (sum of forward reachability counts).
func (c *bitClosure) CCD() int64 {
	n := int(c.n)
	var total int64
	for v := 0; v < n; v++ {
		row := c.fwdRow(ID(v))
		for _, w := range row {
			total += int64(bits.OnesCount64(w))
		}
	}
	return total
}
