package interp_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/interp"
)

func TestColorEnabledForFile_Nil(t *testing.T) {
	if interp.ColorEnabledForFile(nil) {
		t.Error("ColorEnabledForFile(nil) = true, want false")
	}
}

func TestPrintSourceContext_NonexistentFile(t *testing.T) {
	var sb strings.Builder
	interp.PrintSourceContext(&sb, "/no/such/file/xyz.go", 1, 2, false)
	if !strings.Contains(sb.String(), "cannot read source") {
		t.Errorf("expected error message in output, got: %q", sb.String())
	}
}

func TestPrintSourceContext_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "src.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var sb strings.Builder
	interp.PrintSourceContext(&sb, path, 3, 1, false)
	out := sb.String()
	if !strings.Contains(out, "line2") || !strings.Contains(out, "line3") || !strings.Contains(out, "line4") {
		t.Errorf("PrintSourceContext output missing expected lines: %q", out)
	}
}
