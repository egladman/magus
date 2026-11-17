package cache

type lookupCache struct{ m map[string]int } // want `type lookupCache is an optimization with no Benchmark in cache`

func (c *lookupCache) Get(k string) int { return c.m[k] }

type PathCache[K comparable] struct{ m map[K]string } // want `type PathCache is an optimization with no Benchmark in cache`

func (p PathCache[K]) Get(k K) string { return p.m[k] }

type ProbeOnce struct{ v int } // want `type ProbeOnce is an optimization with no Benchmark in cache`

func (p *ProbeOnce) Get() int { return p.v }
