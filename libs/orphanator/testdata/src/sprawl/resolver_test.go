package sprawl

import "testing"

func TestResolve(t *testing.T) {
	if Resolve("a") != "a" {
		t.Fatal("no")
	}
}
