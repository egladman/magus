package std

import (
	"context"
	"testing"
)

func TestFmtSprintf(t *testing.T) {
	cases := []struct {
		name   string
		format string
		args   []string
		want   string
	}{
		{"no args", "hello", nil, "hello"},
		{"one verb", "v%s", []string{"1.2.3"}, "v1.2.3"},
		{"many verbs", "magus_%s_%s_%s.tar.gz", []string{"1.0", "linux", "amd64"}, "magus_1.0_linux_amd64.tar.gz"},
		{"quote verb", "%q", []string{"x y"}, `"x y"`},
		{"literal percent", "100%%", nil, "100%"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FmtSprintf(context.Background(), tc.format, tc.args...)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("FmtSprintf(%q, %v) = %q, want %q", tc.format, tc.args, got, tc.want)
			}
		})
	}
}
