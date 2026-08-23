package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	sandboxenv "github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// covEnvSandbox returns a context carrying a policy that allows exactly the named
// variables. Every other name reads as absent, which is the information-hiding
// contract the env module is built around.
func covEnvSandbox(allow ...string) context.Context {
	return sandbox.WithPolicy(context.Background(), &sandbox.Policy{
		Env: sandboxenv.Allowlist{Allow: allow},
	})
}

func TestEnvGet(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MAGUS_COV_GET", "value")

	got, err := EnvGet(ctx, "MAGUS_COV_GET")
	require.NoError(t, err)
	assert.Equal(t, "value", got)

	got, err = EnvGet(ctx, "MAGUS_COV_GET_MISSING")
	require.NoError(t, err)
	assert.Empty(t, got, "an unset name reads as empty, not as an error")

	// A stripped name reads empty rather than raising: env.get is used as a
	// "did the user set X?" probe, and raising would break innocuous magusfiles.
	got, err = EnvGet(covEnvSandbox("SOMETHING_ELSE"), "MAGUS_COV_GET")
	require.NoError(t, err)
	assert.Empty(t, got)
}

// TestEnvLookup pins the distinction env.get cannot make, and the one place it
// deliberately refuses to: a stripped variable is reported as absent, so a hidden
// secret cannot be probed for.
func TestEnvLookup(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MAGUS_COV_LOOKUP", "value")
	t.Setenv("MAGUS_COV_LOOKUP_EMPTY", "")

	v, found, err := EnvLookup(ctx, "MAGUS_COV_LOOKUP")
	require.NoError(t, err)
	assert.Equal(t, "value", v)
	assert.True(t, found)

	v, found, err = EnvLookup(ctx, "MAGUS_COV_LOOKUP_EMPTY")
	require.NoError(t, err)
	assert.Empty(t, v)
	assert.True(t, found, "set-but-empty is present")

	v, found, err = EnvLookup(ctx, "MAGUS_COV_LOOKUP_MISSING")
	require.NoError(t, err)
	assert.Empty(t, v)
	assert.False(t, found)

	v, found, err = EnvLookup(covEnvSandbox(), "MAGUS_COV_LOOKUP")
	require.NoError(t, err)
	assert.Empty(t, v)
	assert.False(t, found, "a stripped variable must be indistinguishable from an absent one")
}

// TestEnvGetOr: the default fills in for ABSENCE only. A set-but-empty variable
// returns its own empty value, which is what separates get_or from get.
func TestEnvGetOr(t *testing.T) {
	ctx := context.Background()
	t.Setenv("MAGUS_COV_GETOR", "value")
	t.Setenv("MAGUS_COV_GETOR_EMPTY", "")

	got, err := EnvGetOr(ctx, "MAGUS_COV_GETOR", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "value", got)

	got, err = EnvGetOr(ctx, "MAGUS_COV_GETOR_EMPTY", "fallback")
	require.NoError(t, err)
	assert.Empty(t, got)

	got, err = EnvGetOr(ctx, "MAGUS_COV_GETOR_MISSING", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "fallback", got)

	got, err = EnvGetOr(covEnvSandbox(), "MAGUS_COV_GETOR", "fallback")
	require.NoError(t, err)
	assert.Equal(t, "fallback", got, "a stripped variable is absent, so the default applies")
}

// TestEnvSet covers the two refusals: a recording pass writes nothing, and the
// sandbox will not let a stripped name be put back so the next subprocess carries it.
func TestEnvSet(t *testing.T) {
	t.Setenv("MAGUS_COV_SET", "before")

	require.NoError(t, EnvSet(context.Background(), "MAGUS_COV_SET", "after"))
	assert.Equal(t, "after", os.Getenv("MAGUS_COV_SET"))

	require.NoError(t, EnvSet(types.WithTrace(context.Background()), "MAGUS_COV_SET", "traced"))
	assert.Equal(t, "after", os.Getenv("MAGUS_COV_SET"), "a recording pass must not touch the environment")

	require.NoError(t, EnvSet(covEnvSandbox("OTHER"), "MAGUS_COV_SET", "smuggled"),
		"a blocked set reports success; it just does nothing")
	assert.Equal(t, "after", os.Getenv("MAGUS_COV_SET"))
}

func TestEnvList(t *testing.T) {
	t.Setenv("MAGUS_COV_LIST", "value")

	all, err := EnvList(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "value", all["MAGUS_COV_LIST"])

	filtered, err := EnvList(covEnvSandbox("MAGUS_COV_LIST"))
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"MAGUS_COV_LIST": "value"}, filtered,
		"only the allowed name survives the policy")
}

// TestEnvParseDotenv is the whole .env grammar in one table. It is pure: nothing
// here reads or writes the process environment.
func TestEnvParseDotenv(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    map[string]string
	}{
		{"bare assignment", "FOO=bar", map[string]string{"FOO": "bar"}},
		{"blank lines and comments", "\n# a comment\n\n   # indented\nFOO=bar\n", map[string]string{"FOO": "bar"}},
		{"leading export", "export FOO=bar", map[string]string{"FOO": "bar"}},
		{"carriage returns", "FOO=bar\r\nBAZ=qux\r\n", map[string]string{"FOO": "bar", "BAZ": "qux"}},
		{"space around the equals", "  KEY  =  value  ", map[string]string{"KEY": "value"}},
		{"no equals is skipped", "JUST_A_WORD\nFOO=bar", map[string]string{"FOO": "bar"}},
		{"empty key is skipped", "=orphan\nFOO=bar", map[string]string{"FOO": "bar"}},
		{"empty value", "FOO=", map[string]string{"FOO": ""}},
		{"inline comment after an unquoted value", "FOO=bar # trailing", map[string]string{"FOO": "bar"}},
		{"a hash without a leading space is part of the value", "FOO=bar#tag", map[string]string{"FOO": "bar#tag"}},
		{"single quotes are literal", `FOO='a\nb # keep'`, map[string]string{"FOO": `a\nb # keep`}},
		{"double quotes honor escapes", `FOO="a\nb\tc\"d\\e"`, map[string]string{"FOO": "a\nb\tc\"d\\e"}},
		{"double quotes protect a hash", `FOO="bar # kept"`, map[string]string{"FOO": "bar # kept"}},
		{"a lone quote is not a quoted value", `FOO="`, map[string]string{"FOO": `"`}},
		{"last assignment of a key wins", "FOO=one\nFOO=two", map[string]string{"FOO": "two"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EnvParseDotenv(context.Background(), tc.content)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEnvReadDotenv(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("FOO=bar\n# comment\nBAZ=\"qux\"\n"), 0o644))

	got, err := EnvReadDotenv(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"FOO": "bar", "BAZ": "qux"}, got)

	_, err = EnvReadDotenv(ctx, filepath.Join(dir, "absent.env"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "env.read_dotenv")
}

// TestEnvLoadDotenv covers the dotenv convention magus keeps: a name already set
// in the process environment wins over the file, and a recording pass sets nothing.
func TestEnvLoadDotenv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path,
		[]byte("MAGUS_COV_LOAD_NEW=fromfile\nMAGUS_COV_LOAD_EXISTING=fromfile\n"), 0o644))

	t.Setenv("MAGUS_COV_LOAD_EXISTING", "fromenv")
	t.Cleanup(func() { _ = os.Unsetenv("MAGUS_COV_LOAD_NEW") })

	require.NoError(t, EnvLoadDotenv(types.WithTrace(context.Background()), path))
	_, ok := os.LookupEnv("MAGUS_COV_LOAD_NEW")
	assert.False(t, ok, "a recording pass must set nothing")

	require.NoError(t, EnvLoadDotenv(context.Background(), path))
	assert.Equal(t, "fromfile", os.Getenv("MAGUS_COV_LOAD_NEW"))
	assert.Equal(t, "fromenv", os.Getenv("MAGUS_COV_LOAD_EXISTING"),
		"an already-set name wins over the file")

	require.Error(t, EnvLoadDotenv(context.Background(), filepath.Join(dir, "absent.env")))
}
