package quiet

type Box struct{ n []int }

func (b *Box) Len() int { return len(b.n) }

func Make() []int { return []int{1} }
