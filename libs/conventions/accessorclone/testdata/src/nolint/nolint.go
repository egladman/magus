package nolint // want `a //nolint cannot exempt accessorclone`

type Box struct{ n []int }

func (b *Box) Held() []int { return b.n } //nolint:accessorclone // listed in the baseline
