package sprawl // want `resolver_edge_cases_test.go narrows resolver.go; these tests belong in resolver_test.go`

import "testing"

func TestResolveEmpty(t *testing.T) {
	if Resolve("") != "" {
		t.Fatal("no")
	}
}
