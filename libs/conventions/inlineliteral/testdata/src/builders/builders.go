package builders

import "strings"

type point struct{ x, y int }

type config struct{ name string }

func newPoint(x int) point { return point{x: x, y: 1} }

func address(x int) *point { return &point{x: x} }

func shared() point { return point{} }

func label(c config) string { return c.name }

func limit() int64 { return 5 }

func zero() point { return point{} }

func guarded(c *config) *point {
	if c == nil {
		return nil
	}
	return &point{x: len(c.name)}
}

func unused() strings.Builder { return strings.Builder{} } // want `unused returns one composite literal and nothing references it: delete it`

func builder() strings.Builder { return strings.Builder{} }

func take(f func() point) point { return f() }

func use(n int, c config) {
	p := newPoint(n)  // want `newPoint returns one composite literal and has one use site: inline it`
	q := address(n).x // want `address returns one composite literal and has one use site: inline it`
	a, b, d := shared(), shared(), shared()
	s := label(c)    // want `label returns one field and has one use site: inline it`
	l := limit()     // want `limit returns one literal and has one use site: inline it`
	z := take(zero)  // want `zero returns one composite literal and has one use site: inline it`
	g := guarded(&c) // want `guarded returns one composite literal and has one use site: inline it, keeping its nil check at the site`
	_, _, _, _, _, _, _, _, _ = p, q, a, b, d, s, l, z, g
}
