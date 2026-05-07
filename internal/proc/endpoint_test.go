package proc_test

import (
	"testing"

	"github.com/egladman/magus/internal/proc"
)

func TestParseEndpoint(t *testing.T) {
	tests := []struct {
		input    string
		wantAddr string
		wantStr  string
		wantErr  bool
	}{
		// canonical unix:// form
		{"unix:///var/run/magus.sock", "/var/run/magus.sock", "unix:///var/run/magus.sock", false},
		{"unix:///tmp/magus-1234-abcd.sock", "/tmp/magus-1234-abcd.sock", "unix:///tmp/magus-1234-abcd.sock", false},
		// bare path back-compat
		{"/tmp/magus.sock", "/tmp/magus.sock", "unix:///tmp/magus.sock", false},
		// errors
		{"unix://", "", "", true},
		{"tcp://localhost:9000", "", "", true},
		{"grpc://foo", "", "", true},
		{"", "", "", true},
	}

	for _, tc := range tests {
		ep, err := proc.ParseEndpoint(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("ParseEndpoint(%q): expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseEndpoint(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if ep.Addr != tc.wantAddr {
			t.Errorf("ParseEndpoint(%q).Addr = %q, want %q", tc.input, ep.Addr, tc.wantAddr)
		}
		if ep.String() != tc.wantStr {
			t.Errorf("ParseEndpoint(%q).String() = %q, want %q", tc.input, ep.String(), tc.wantStr)
		}
		if ep.Network() != "unix" {
			t.Errorf("ParseEndpoint(%q).Network() = %q, want unix", tc.input, ep.Network())
		}
	}
}
