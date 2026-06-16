package project

import (
	"testing"
)

func TestIsIgnoreDir(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{".git", true},
		{".hg", true},
		{".jj", true},
		{".magus", true},
		{".build", true},
		{"vendor", true},
		{"node_modules", true},
		{"target", true},
		{"gen", true},
		{"starter", true},
		{"src", false},
		{"cmd", false},
		{"pkg", false},
		{"internal", false},
	}
	for _, tc := range cases {
		if got := IsIgnoreDir(tc.name); got != tc.want {
			t.Errorf("IsIgnoreDir(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestIgnoreDirs_ContainsExpected(t *testing.T) {
	must := []string{".git", "vendor", "node_modules"}
	for _, d := range must {
		found := false
		for _, v := range IgnoreDirs {
			if v == d {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("IgnoreDirs missing %q", d)
		}
	}
}
