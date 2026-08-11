package registry

import "testing"

// TestIsLoopback guards against S-1: a prefix check like
// strings.HasPrefix(host, "127.") also matches "127.evil.example", a hostname
// an attacker registers and controls DNS for. requireHTTPS exempts anything
// isLoopback calls loopback, so that bug would downgrade such a source to
// plaintext HTTP against an attacker-controlled host.
func TestIsLoopback(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", true},
		{"::1", true},
		{"localhost", true},
		{"127.evil.example", false},
		{"127foo.example", false},
		{"0127.0.0.1", false},
	}
	for _, tc := range cases {
		if got := isLoopback(tc.host); got != tc.want {
			t.Errorf("isLoopback(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}
