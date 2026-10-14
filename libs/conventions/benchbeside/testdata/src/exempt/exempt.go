package exempt

// optimization: raise the limit before the heap reaches its minimum.
func Raise() {}

type LimitCache struct{ n int }

func (c *LimitCache) Limit() int { return c.n }
