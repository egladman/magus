package main // want `move the logic this test drives into the package that owns it, then test it there`

import "testing"

func TestRun(t *testing.T) {
	run()
}
