package logattr

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

// TestForLogsThroughTheDefaultAtCallTime pins that For follows a SetDefault
// made after the package loaded, which a logger bound at init would miss.
func TestForLogsThroughTheDefaultAtCallTime(t *testing.T) {
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	For("knowledge").Info("cannot decode symbol index")

	got := buf.String()
	if !strings.Contains(got, `msg="cannot decode symbol index"`) || !strings.Contains(got, "component=knowledge") {
		t.Fatalf("want the message and component=knowledge, got %q", got)
	}
}
