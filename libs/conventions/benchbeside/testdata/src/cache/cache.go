package cache

type lookupCache struct{ m map[string]int } // want `type lookupCache is an optimization with no Benchmark in cache`

func (c *lookupCache) Get(k string) int { return c.m[k] }

type PathMemo[K comparable] struct{ m map[K]string } // want `type PathMemo is an optimization with no Benchmark in cache`

func (p PathMemo[K]) Get(k K) string { return p.m[k] }
