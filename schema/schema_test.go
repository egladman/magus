package schema

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseBool_truthy(t *testing.T) {
	for _, v := range []string{"true", "True", "TRUE", "1", "yes", "YES"} {
		assert.Truef(t, ParseBool(v, false), "ParseBool(%q, false) = false, want true", v)
	}
}

func TestParseBool_falsy(t *testing.T) {
	for _, v := range []string{"false", "False", "FALSE", "0", "no", "NO"} {
		assert.Falsef(t, ParseBool(v, true), "ParseBool(%q, true) = true, want false", v)
	}
}

func TestParseBool_unknown_returnsDefault(t *testing.T) {
	assert.True(t, ParseBool("maybe", true), "ParseBool(unknown, true) = false, want true (fallback)")
	assert.False(t, ParseBool("", false), "ParseBool(empty, false) = true, want false (fallback)")
}

func TestFields_nonEmpty(t *testing.T) {
	require.NotEmpty(t, Fields, "Fields is empty — fields.go was not generated")
}

func TestFields_allEnvVarsStartWithMAGUS(t *testing.T) {
	for _, f := range Fields {
		assert.Truef(t, len(f.EnvVar) >= 7 && f.EnvVar[:6] == "MAGUS_",
			"Field %q: EnvVar %q does not start with MAGUS_", f.GoPath, f.EnvVar)
	}
}

func TestFields_noDuplicateEnvVars(t *testing.T) {
	seen := make(map[string]string, len(Fields))
	for _, f := range Fields {
		prev, ok := seen[f.EnvVar]
		assert.Falsef(t, ok, "duplicate EnvVar %q: GoPath %q and %q", f.EnvVar, prev, f.GoPath)
		seen[f.EnvVar] = f.GoPath
	}
}

func TestFields_noDuplicateGoPaths(t *testing.T) {
	seen := make(map[string]bool, len(Fields))
	for _, f := range Fields {
		assert.Falsef(t, seen[f.GoPath], "duplicate GoPath %q", f.GoPath)
		seen[f.GoPath] = true
	}
}

func TestFields_boolPtrHasNoFlagName(t *testing.T) {
	for _, f := range Fields {
		if f.Kind == KindBoolPtr {
			assert.Emptyf(t, f.Flag.Long,
				"KindBoolPtr field %q should have empty Flag.Long (env-only), got %q", f.GoPath, f.Flag.Long)
		}
	}
}

func TestFieldByEnv_found(t *testing.T) {
	f, ok := FieldByEnv("MAGUS_CACHE_DIR")
	require.True(t, ok, "FieldByEnv(MAGUS_CACHE_DIR) not found")
	assert.Equal(t, "Cache.Dir", f.GoPath)
	assert.Equal(t, KindString, f.Kind)
}

func TestFieldByEnv_notFound(t *testing.T) {
	_, ok := FieldByEnv("MAGUS_DOES_NOT_EXIST")
	assert.False(t, ok, "FieldByEnv(unknown) should return false")
}

func TestFieldByGoPath_found(t *testing.T) {
	f, ok := FieldByGoPath("Cache.Dir")
	require.True(t, ok, "FieldByGoPath(Cache.Dir) not found")
	assert.Equal(t, "MAGUS_CACHE_DIR", f.EnvVar)
}

func TestFieldByGoPath_notFound(t *testing.T) {
	_, ok := FieldByGoPath("Nonexistent.Field")
	assert.False(t, ok, "FieldByGoPath(unknown) should return false")
}

// String must be single-line so %v / fmt.Println(field) doesn't smear across
// the surrounding log output.
func TestField_String_singleLine(t *testing.T) {
	fields := []Field{
		{EnvVar: "MAGUS_CACHE_DIR", YamlPath: "cache.dir", Flag: FlagNames{Long: "cache-dir"}},
		{EnvVar: "MAGUS_HINTS_ENABLED", YamlPath: "hints.enabled", Flag: FlagNames{}},
		{EnvVar: "MAGUS_OUTPUT", YamlPath: "output", Flag: FlagNames{Long: "output", Short: "o"}},
	}
	for _, f := range fields {
		out := f.String()
		assert.NotContainsf(t, out, "\n", "Field.String() must be single-line for %q, got %q", f.EnvVar, out)
		assert.Containsf(t, out, f.EnvVar, "Field.String() missing env var: %q", out)
		assert.Containsf(t, out, f.YamlPath, "Field.String() missing yaml path: %q", out)
	}
}

func TestField_String_flagFormatting(t *testing.T) {
	t.Run("long flag only", func(t *testing.T) {
		f := Field{EnvVar: "MAGUS_CACHE_DIR", YamlPath: "cache.dir", Flag: FlagNames{Long: "cache-dir"}}
		assert.Equal(t, "MAGUS_CACHE_DIR (--cache-dir, cache.dir)", f.String())
	})
	t.Run("short and long", func(t *testing.T) {
		f := Field{EnvVar: "MAGUS_OUTPUT", YamlPath: "output", Flag: FlagNames{Long: "output", Short: "o"}}
		assert.Equal(t, "MAGUS_OUTPUT (-o, --output, output)", f.String())
	})
	t.Run("env-only", func(t *testing.T) {
		f := Field{EnvVar: "MAGUS_HINTS_ENABLED", YamlPath: "hints.enabled", Flag: FlagNames{}}
		assert.Equal(t, "MAGUS_HINTS_ENABLED (env-only, hints.enabled)", f.String())
	})
}

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
	assert.Containsf(t, out, "MAGUS_CACHE_DIR", "Describe() missing env var: %q", out)
	assert.Containsf(t, out, "--cache-dir", "Describe() missing flag: %q", out)
	assert.Containsf(t, out, "cache.dir", "Describe() missing yaml path: %q", out)
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
	assert.Containsf(t, out, "env-only", "Describe() of env-only field missing '(env-only)': %q", out)
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
	assert.Containsf(t, out, "-o", "Describe() missing short flag: %q", out)
}

func TestKind_hasDurationField(t *testing.T) {
	found := false
	for _, f := range Fields {
		if f.Kind == KindDuration {
			found = true
			break
		}
	}
	assert.True(t, found, "no KindDuration field found — generator may have lost time.Duration detection")
}

func TestUseEnv_nonNil(t *testing.T) {
	assert.NotNil(t, UseEnv(), "UseEnv() returned nil")
}

func TestEnvPrefix(t *testing.T) {
	assert.Equal(t, "MAGUS", EnvPrefix)
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
	require.True(t, ok, "runtime.Caller failed")
	schemaDir := filepath.Dir(thisFile)
	// e.g. magus/schema

	tmp := t.TempDir()
	fieldsOut := filepath.Join(tmp, "fields.go")
	bindOut := filepath.Join(tmp, "bind.go")
	envOut := filepath.Join(tmp, "env.go")

	generatorPath := filepath.Join(schemaDir, "..", "cmd", "magus-scribe")
	configPath := filepath.Join(schemaDir, "..", "internal", "config", "config.go")

	cmd := exec.Command(
		"go", "run", generatorPath, "config",
		"-config", configPath,
		"-fields-out", fieldsOut,
		"-bind-out", bindOut,
		"-apply-env-out", envOut,
	)
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "schema generator failed: %s", out)

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
		require.NoErrorf(t, err, "read committed %s", c.name)
		got, err := os.ReadFile(c.generated)
		require.NoErrorf(t, err, "read generated %s", c.name)
		assert.Truef(t, bytes.Equal(want, got),
			"%s is out of date — run: go generate ./magus/cmd/magus/...", c.name)
	}
}
