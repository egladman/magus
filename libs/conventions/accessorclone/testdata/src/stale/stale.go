package stale // want `the baseline lists Box.Gone, which no longer returns`

import "slices"

type Box struct{ n []int }

func (b *Box) Gone() []int { return slices.Clone(b.n) }
