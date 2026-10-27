package image

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func validSpec() Spec {
	return Spec{Platform: Platform{OS: "linux", Architecture: "amd64"}}
}

func TestImageSpecPlatform(t *testing.T) {
	tests := []struct {
		name    string
		p       Platform
		wantErr string
	}{
		{"amd64", Platform{"linux", "amd64", ""}, ""},
		{"arm64", Platform{"linux", "arm64", ""}, ""},
		{"arm64 v8", Platform{"linux", "arm64", "v8"}, ""},
		{"windows", Platform{"windows", "amd64", ""}, "accepted: linux"},
		{"empty os", Platform{"", "amd64", ""}, "accepted: linux"},
		{"386", Platform{"linux", "386", ""}, "amd64, arm64"},
		{"amd64 variant", Platform{"linux", "amd64", "v2"}, "for amd64"},
		{"arm64 v7", Platform{"linux", "arm64", "v7"}, "v8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { wantErr(t, tt.p.Validate(), tt.wantErr) })
	}
}

func TestImageSpecDestination(t *testing.T) {
	tests := []struct {
		dest    string
		wantErr string
	}{
		{"/usr/bin/magus", ""},
		{"", "absolute"},
		{"usr/bin", "absolute"},
		{"/", "root"},
		{"/usr/bin/", "trailing slash"},
		{"/usr/../bin", ".. element"},
		{"/usr//bin", "not clean"},
		{"/usr/./bin", "not clean"},
		{"/usr/\x00bin", "NUL"},
	}
	for _, tt := range tests {
		t.Run(tt.dest, func(t *testing.T) {
			s := validSpec()
			s.Files = []File{{Source: "a", Destination: tt.dest, Mode: 0o755}}
			wantErr(t, s.Validate(), tt.wantErr)
		})
	}
}

func TestImageSpecOverlap(t *testing.T) {
	tests := []struct {
		name    string
		files   []File
		links   []Symlink
		wantErr string
	}{
		{"siblings", []File{{Destination: "/a/b"}, {Destination: "/a/c"}}, nil, ""},
		{"prefix without slash", []File{{Destination: "/a"}, {Destination: "/ab"}}, nil, ""},
		{"duplicate files", []File{{Destination: "/a"}, {Destination: "/a"}}, nil, "placed twice"},
		{"file and symlink", []File{{Destination: "/a"}}, []Symlink{{Destination: "/a", Target: "x"}}, "placed twice"},
		{"below a file", []File{{Destination: "/a"}, {Destination: "/a/b"}}, nil, "below"},
		{"below a symlink", []File{{Destination: "/a/b"}}, []Symlink{{Destination: "/a", Target: "x"}}, "below"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			s.Files, s.Symlinks = tt.files, tt.links
			wantErr(t, s.Validate(), tt.wantErr)
		})
	}
}

func TestImageSpecMode(t *testing.T) {
	tests := []struct {
		name    string
		mode    uint32
		wantErr string
	}{
		{"0755", 0o755, ""},
		{"setuid spelled", 0o4755, ""},
		{"sticky spelled", 0o1777, ""},
		{"directory bit", 1 << 31, "mode"},
		{"above sticky", 0o10000, "mode"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			s.Files = []File{{Destination: "/a", Mode: fs.FileMode(tt.mode)}}
			wantErr(t, s.Validate(), tt.wantErr)
		})
	}
}

func TestImageSpecSymlinkTarget(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		wantErr string
	}{
		{"relative", "../lib/x", ""},
		{"dangling absolute", "/nowhere", ""},
		{"empty", "", "target is empty"},
		{"nul", "a\x00b", "NUL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			s.Symlinks = []Symlink{{Destination: "/l", Target: tt.target}}
			wantErr(t, s.Validate(), tt.wantErr)
		})
	}
}

func TestImageSpecEnvKeys(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		unset   []string
		wantErr string
	}{
		{"ok", map[string]string{"PATH": "/bin"}, []string{"HOME"}, ""},
		{"empty key", map[string]string{"": "v"}, nil, "env key"},
		{"equals in key", map[string]string{"A=B": "v"}, nil, "env key"},
		{"empty unset", nil, []string{""}, "env key"},
		{"equals in unset", nil, []string{"A=B"}, "env key"},
		{"equals in value", map[string]string{"A": "b=c"}, nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := validSpec()
			s.Env, s.UnsetEnv = tt.env, tt.unset
			wantErr(t, s.Validate(), tt.wantErr)
		})
	}
}

func TestImageSpecValidatePlatform(t *testing.T) {
	wantErr(t, Spec{}.Validate(), "image.assemble: image: platform os")
}

func TestImageSpecDecode(t *testing.T) {
	full := map[string]any{
		"base":            "/b",
		"platform":        map[string]any{"os": "linux", "architecture": "arm64", "variant": "v8"},
		"files":           []any{map[string]any{"source": "s", "destination": "/d", "mode": 0o755, "uid": 1, "gid": 2}},
		"symlinks":        []any{map[string]any{"destination": "/l", "target": "t", "uid": 3, "gid": 4}},
		"env":             map[string]any{"A": "1"},
		"unsetEnv":        []any{"B"},
		"user":            "u",
		"workingDir":      "/w",
		"entrypoint":      []any{"/e"},
		"resetEntrypoint": true,
		"command":         []any{"c"},
		"resetCommand":    true,
		"labels":          map[string]any{"l": "v"},
		"annotations":     map[string]any{"a": "v"},
		"created":         "2020-01-01T00:00:00Z",
	}
	got, err := DecodeSpec(full)
	if err != nil {
		t.Fatalf("DecodeSpec: %v", err)
	}
	want := Spec{
		Base:            "/b",
		Platform:        Platform{"linux", "arm64", "v8"},
		Files:           []File{{"s", "/d", 0o755, 1, 2}},
		Symlinks:        []Symlink{{"/l", "t", 3, 4}},
		Env:             map[string]string{"A": "1"},
		UnsetEnv:        []string{"B"},
		User:            "u",
		WorkingDir:      "/w",
		Entrypoint:      []string{"/e"},
		ResetEntrypoint: true,
		Command:         []string{"c"},
		ResetCommand:    true,
		Labels:          map[string]string{"l": "v"},
		Annotations:     map[string]string{"a": "v"},
		Created:         "2020-01-01T00:00:00Z",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("DecodeSpec:\n got %+v\nwant %+v", got, want)
	}
}

func TestImageSpecDecodeErrors(t *testing.T) {
	tests := []struct {
		name    string
		in      map[string]any
		wantErr string
	}{
		{"empty is fine", map[string]any{}, ""},
		{"unknown key", map[string]any{"bogus": 1}, `unknown key "bogus"`},
		{"snake case key", map[string]any{"working_dir": "/"}, "unknown key"},
		{"base type", map[string]any{"base": 1}, "base: want a string"},
		{"flag type", map[string]any{"resetCommand": "yes"}, "resetCommand: want a bool"},
		{"platform type", map[string]any{"platform": "linux"}, "platform: want an object"},
		{"platform key", map[string]any{"platform": map[string]any{"arch": "amd64"}}, `unknown key "arch"`},
		{"platform field type", map[string]any{"platform": map[string]any{"os": 1}}, "os: want a string"},
		{"files type", map[string]any{"files": "x"}, "files: want a list"},
		{"file element", map[string]any{"files": []any{"x"}}, "files[0]: want an object"},
		{"file key", map[string]any{"files": []any{map[string]any{"src": "x"}}}, `unknown key "src"`},
		{"file mode type", map[string]any{"files": []any{map[string]any{"mode": "0755"}}}, "mode: want an int"},
		{"file mode negative", map[string]any{"files": []any{map[string]any{"mode": -1}}}, "mode"},
		{"fractional mode", map[string]any{"files": []any{map[string]any{"mode": 1.5}}}, "mode: want an int"},
		{"symlink key", map[string]any{"symlinks": []any{map[string]any{"to": "x"}}}, `unknown key "to"`},
		{"symlink uid type", map[string]any{"symlinks": []any{map[string]any{"uid": "0"}}}, "uid: want an int"},
		{"env value type", map[string]any{"env": map[string]any{"A": 1}}, "env.A: want a string"},
		{"env type", map[string]any{"env": []any{}}, "env: want a map"},
		{"command element", map[string]any{"command": []any{1}}, "command[0]: want a string"},
		{"entrypoint type", map[string]any{"entrypoint": "x"}, "entrypoint: want a list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeSpec(tt.in)
			wantErr(t, err, tt.wantErr)
		})
	}
}

func TestImageSpecDecodeFloatMode(t *testing.T) {
	got, err := DecodeSpec(map[string]any{"files": []any{map[string]any{"destination": "/d", "mode": float64(0o644)}}})
	if err != nil {
		t.Fatalf("DecodeSpec: %v", err)
	}
	if got.Files[0].Mode != 0o644 {
		t.Errorf("mode = %#o, want 0644", got.Files[0].Mode)
	}
}

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	switch {
	case want == "" && err != nil:
		t.Fatalf("unexpected error: %v", err)
	case want != "" && err == nil:
		t.Fatalf("no error, want one containing %q", want)
	case want != "" && !strings.Contains(err.Error(), want):
		t.Fatalf("error %q does not contain %q", err, want)
	}
}

func TestNewSpecWithChainAssemblesLayout(t *testing.T) {
	src := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(src, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "layout")
	mode, err := NewMode(0o755)
	if err != nil {
		t.Fatal(err)
	}
	file := HostFile{Value: src}.Place(DestFile{Value: "/usr/bin/app"}, mode)
	layout, err := NewSpec(NewLinuxAmd64()).
		WithFile(file).
		WithEnv("APP", "1").
		WithEntrypoint("/usr/bin/app").
		WithUser("65532").
		AssembleLayout(context.Background(), dest)
	if err != nil {
		t.Fatal(err)
	}
	if layout.Op != OpAssembleLayout {
		t.Fatalf("op: %s", layout.Op)
	}
	if layout.Layer.IsZero() {
		t.Fatal("expected a layer")
	}
	if layout.Path != dest {
		t.Fatalf("path: %s", layout.Path)
	}
	_, manifest, cfg := imageTestReadManifest(t, dest)
	if len(manifest.Layers) != 1 {
		t.Fatalf("layers: %d", len(manifest.Layers))
	}
	if got := cfg.Config.Entrypoint; len(got) != 1 || got[0] != "/usr/bin/app" {
		t.Fatalf("entrypoint: %v", got)
	}
	if cfg.Config.User != "65532" {
		t.Fatalf("user: %s", cfg.Config.User)
	}
	found := false
	for _, e := range cfg.Config.Env {
		if e == "APP=1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("env missing APP=1: %v", cfg.Config.Env)
	}
}

func TestNilSpecAssembleLayoutErrors(t *testing.T) {
	var s *Spec
	_, err := s.AssembleLayout(context.Background(), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "nil spec") {
		t.Fatalf("got %v", err)
	}
}
