package schema

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// ── ParseBool ────────────────────────────────────────────────────────────────

func TestParseBool_truthy(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "1", "yes", "YES"} {
		if !ParseBool(v, false) {
			t.Errorf("ParseBool(%q, false) = false, want true", v)
		}
	}
}

func TestParseBool_falsy(t *testing.T) {
	for _, v := range []string{"false", "False", "FALSE", "0", "no", "NO"} {
		if ParseBool(v, true) {
			t.Errorf("ParseBool(%q, true) = true, want false", v)
		}
	}
}

func TestParseBool_unknown_returnsDefault(t *testing.T) {
	if !ParseBool("maybe", true) {
		t.Error("ParseBool(unknown, true) = false, want true (fallback)")
	}
	if ParseBool("", false) {
		t.Error("ParseBool(empty, false) = true, want false (fallback)")
	}
}

// ── Fields population ──────────────────────────────────────────────────────────

func TestFields_nonEmpty(t *testing.T) {
	if len(Fields) == 0 {
		t.Fatal("Fields is empty — fields.go was not generated")
	}
}

func TestFields_allEnvVarsStartWithMAGUS(t *testing.T) {
	for _, f := range Fields {
		if len(f.EnvVar) < 7 || f.EnvVar[:6] != "MAGUS_" {
			t.Errorf("Field %q: EnvVar %q does not start with MAGUS_", f.GoPath, f.EnvVar)
		}
	}
}

func TestFields_noDuplicateEnvVars(t *testing.T) {
	seen := make(map[string]string, len(Fields))
	for _, f := range Fields {
		if prev, ok := seen[f.EnvVar]; ok {
			t.Errorf("duplicate EnvVar %q: GoPath %q and %q", f.EnvVar, prev, f.GoPath)
		}
		seen[f.EnvVar] = f.GoPath
	}
}

func TestFields_noDuplicateGoPaths(t *testing.T) {
	seen := make(map[string]bool, len(Fields))
	for _, f := range Fields {
		if seen[f.GoPath] {
			t.Errorf("duplicate GoPath %q", f.GoPath)
		}
		seen[f.GoPath] = true
	}
}

func TestFields_boolPtrHasNoFlagName(t *testing.T) {
	for _, f := range Fields {
		if f.Kind == KindBoolPtr && f.Flag.Long != "" {
			t.Errorf("KindBoolPtr field %q should have empty Flag.Long (env-only), got %q", f.GoPath, f.Flag.Long)
		}
	}
}

// ── FieldByEnv ──────────────────────────────────────────────────────────────────

func TestFieldByEnv_found(t *testing.T) {
	f, ok := FieldByEnv("MAGUS_CACHE_DIR")
	if !ok {
		t.Fatal("FieldByEnv(MAGUS_CACHE_DIR) not found")
	}
	if f.GoPath != "Cache.Dir" {
		t.Errorf("GoPath = %q, want Cache.Dir", f.GoPath)
	}
	if f.Kind != KindString {
		t.Errorf("Kind = %v, want KindString", f.Kind)
	}
}

func TestFieldByEnv_notFound(t *testing.T) {
	_, ok := FieldByEnv("MAGUS_DOES_NOT_EXIST")
	if ok {
		t.Error("FieldByEnv(unknown) should return false")
	}
}

// ── FieldByGoPath ───────────────────────────────────────────────────────────────

func TestFieldByGoPath_found(t *testing.T) {
	f, ok := FieldByGoPath("Cache.Dir")
	if !ok {
		t.Fatal("FieldByGoPath(Cache.Dir) not found")
	}
	if f.EnvVar != "MAGUS_CACHE_DIR" {
		t.Errorf("EnvVar = %q, want MAGUS_CACHE_DIR", f.EnvVar)
	}
}

func TestFieldByGoPath_notFound(t *testing.T) {
	_, ok := FieldByGoPath("Nonexistent.Field")
	if ok {
		t.Error("FieldByGoPath(unknown) should return false")
	}
}

// ── Field.String ──────────────────────────────────────────────────────────────

// String must be single-line so %v / fmt.Println(field) doesn't smear across
// the surrounding log output.
func TestField_String_singleLine(t *testing.T) {
	cases := []Field{
		{EnvVar: "MAGUS_CACHE_DIR", YamlPath: "cache.dir", Flag: FlagNames{Long: "cache-dir"}},
		{EnvVar: "MAGUS_HINTS_ENABLED", YamlPath: "hints.enabled", Flag: FlagNames{}},
		{EnvVar: "MAGUS_OUTPUT", YamlPath: "output", Flag: FlagNames{Long: "output", Short: "o"}},
	}
	for _, f := range cases {
		out := f.String()
		if strings.Contains(out, "\n") {
			t.Errorf("Field.String() must be single-line for %q, got %q", f.EnvVar, out)
		}
		if !strings.Contains(out, f.EnvVar) {
			t.Errorf("Field.String() missing env var: %q", out)
		}
		if !strings.Contains(out, f.YamlPath) {
			t.Errorf("Field.String() missing yaml path: %q", out)
		}
	}
}

func TestField_String_flagFormatting(t *testing.T) {
	tests := []struct {
		name  string
		field Field
		want  string
	}{
		{
			"long flag only",
			Field{EnvVar: "MAGUS_CACHE_DIR", YamlPath: "cache.dir", Flag: FlagNames{Long: "cache-dir"}},
			"MAGUS_CACHE_DIR (--cache-dir, cache.dir)",
		},
		{
			"short and long",
			Field{EnvVar: "MAGUS_OUTPUT", YamlPath: "output", Flag: FlagNames{Long: "output", Short: "o"}},
			"MAGUS_OUTPUT (-o, --output, output)",
		},
		{
			"env-only",
			Field{EnvVar: "MAGUS_HINTS_ENABLED", YamlPath: "hints.enabled", Flag: FlagNames{}},
			"MAGUS_HINTS_ENABLED (env-only, hints.enabled)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.field.String(); got != tc.want {
				t.Errorf("Field.String() = %q, want %q", got, tc.want)
			}
		})
	}
}

// ── Field.Describe ──────────────────────────────────────────────────────────────

func TestField_Describe_withFlag(t *testing.T) {
	f := Field{
		GoPath:   "Cache.Dir",
		YamlPath: "cache.dir",
		EnvVar:   "MAGUS_CACHE_DIR",
		Flag:     FlagNames{Long: "cache-dir"},
		Kind:     KindString,
		Usage:    "cache directory",
	}
	out := f.Describe()
	if !strings.Contains(out, "MAGUS_CACHE_DIR") {
		t.Errorf("Describe() missing env var: %q", out)
	}
	if !strings.Contains(out, "--cache-dir") {
		t.Errorf("Describe() missing flag: %q", out)
	}
	if !strings.Contains(out, "cache.dir") {
		t.Errorf("Describe() missing yaml path: %q", out)
	}
}

func TestField_Describe_envOnly(t *testing.T) {
	f := Field{
		GoPath:   "Hints.Enabled",
		YamlPath: "hints.enabled",
		EnvVar:   "MAGUS_HINTS_ENABLED",
		Flag:     FlagNames{},
		Kind:     KindBoolPtr,
		Usage:    "hints enabled",
	}
	out := f.Describe()
	if !strings.Contains(out, "env-only") {
		t.Errorf("Describe() of env-only field missing '(env-only)': %q", out)
	}
}

func TestField_Describe_withShort(t *testing.T) {
	f := Field{
		GoPath:   "Output",
		YamlPath: "output",
		EnvVar:   "MAGUS_OUTPUT",
		Flag:     FlagNames{Long: "output", Short: "o"},
		Kind:     KindString,
	}
	out := f.Describe()
	if !strings.Contains(out, "-o") {
		t.Errorf("Describe() missing short flag: %q", out)
	}
}

// ── Kind round-trip via Fields ──────────────────────────────────────────────────

func TestKind_hasDurationField(t *testing.T) {
	for _, f := range Fields {
		if f.Kind == KindDuration {
			return
		}
	}
	t.Error("no KindDuration field found — generator may have lost time.Duration detection")
}

// ── UseEnv ────────────────────────────────────────────────────────────────────

func TestUseEnv_nonNil(t *testing.T) {
	if UseEnv() == nil {
		t.Error("UseEnv() returned nil")
	}
}

func TestEnvPrefix(t *testing.T) {
	if EnvPrefix != "MAGUS" {
		t.Errorf("EnvPrefix = %q, want MAGUS", EnvPrefix)
	}
}

// TestSchemaNotDrifted re-runs the schema generator into temp files and
// diffs them against the committed fields.go, bind.go, and env.go.
// Fails if any committed file is out of date, meaning a Config change
// requires re-running:
//
//	go generate ./magus/cmd/magus/...
func TestSchemaNotDrifted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping drift check in short mode")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	schemaDir := filepath.Dir(thisFile)
	// e.g. magus/schema

	tmp := t.TempDir()
	fieldsOut := filepath.Join(tmp, "fields.go")
	bindOut := filepath.Join(tmp, "bind.go")
	envOut := filepath.Join(tmp, "env.go")

	generatorPath := filepath.Join(schemaDir, "..", "cmd", "magus-config-gen")
	configPath := filepath.Join(schemaDir, "..", "internal", "config", "config.go")

	cmd := exec.Command(
		"go", "run", generatorPath,
		"-config", configPath,
		"-fields-out", fieldsOut,
		"-bind-out", bindOut,
		"-apply-env-out", envOut,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("schema generator failed: %v\n%s", err, out)
	}

	checks := []struct {
		name      string
		committed string
		generated string
	}{
		{
			"fields.go",
			filepath.Join(schemaDir, "gen", "fields.go"),
			fieldsOut,
		},
		{
			"bind.go",
			filepath.Join(schemaDir, "..", "cmd", "magus", "gen", "bind.go"),
			bindOut,
		},
		{
			"env.go",
			filepath.Join(schemaDir, "..", "internal", "config", "gen", "env.go"),
			envOut,
		},
	}

	for _, c := range checks {
		want, err := os.ReadFile(c.committed)
		if err != nil {
			t.Fatalf("read committed %s: %v", c.name, err)
		}
		got, err := os.ReadFile(c.generated)
		if err != nil {
			t.Fatalf("read generated %s: %v", c.name, err)
		}
		if !bytes.Equal(want, got) {
			t.Errorf("%s is out of date — run: go generate ./magus/cmd/magus/...", c.name)
		}
	}
}
