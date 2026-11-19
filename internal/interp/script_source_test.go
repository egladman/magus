package interp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

func TestScriptReachesMagus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}
	write("plain.buzz", `import "std";`)
	write("direct.buzz", `import "magus";`)
	write("lib/deep.buzz", `import "magus/spell";`)
	write("lib/mid.buzz", `import "deep" as deep;`)
	write("cycle.buzz", `import "cycle";`)
	write("commented.buzz", "// import \"magus\";\nimport \"std\";")

	sess := buzz.NewSession(t.Context())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetIncludeDirs([]string{dir})

	for code, want := range map[string]bool{
		`import "std"; import "fs";`:                   false,
		`import "plain" as p;`:                         false,
		`import "missing";`:                            false,
		`import "cycle";`:                              false,
		`import "magus" as m;`:                         true,
		`import print from "buzz:magus";`:              true,
		`import "std"; import "direct";`:               true,
		`import "lib/mid" as mid;`:                     true,
		`import "spells/hello";`:                       true,
		"import \"std\";\nimport \"magus/diagram\";\n": true,
		"// import \"magus\";\nimport \"std\";":        false,
		"import \"std\"; // import \"magus\";":         false,
		"final s = \"import \\\"magus\\\";\";":         false,
		`import "commented";`:                          false,
	} {
		assert.Equal(t, want, scriptReachesMagus(sess, code), code)
	}
}

func TestScriptSplitArgsCutsAtTheFirstSeparator(t *testing.T) {
	before, sep, after := SplitScriptArgs([]string{"a", "--", "b", "--", "c"})
	assert.Equal(t, []string{"a"}, before)
	assert.True(t, sep)
	assert.Equal(t, []string{"b", "--", "c"}, after)

	before, sep, after = SplitScriptArgs([]string{"a"})
	assert.Equal(t, []string{"a"}, before)
	assert.False(t, sep)
	assert.Empty(t, after)
}

func TestScriptRunKeySeparatesProbes(t *testing.T) {
	assert.NotEqual(t, ScriptRunKey("p", "c", "l", nil), ScriptRunKey("p", "c", "l", []string{"--x"}),
		"different arguments are a different probe")
	assert.NotEqual(t, ScriptRunKey("p", "c", "job-a", nil), ScriptRunKey("p", "c", "job-b", nil),
		"another job's run is another ref")
}
