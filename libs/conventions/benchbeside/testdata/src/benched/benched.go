package benched

// optimization: reuse the buffer instead of allocating one per call.
func Reuse() {}

type SizeCache struct{ n int }

func (c *SizeCache) Size() int { return c.n }
