package std

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stdAPILockPath is the committed snapshot of every member a magusfile can call on a
// std host module, one dotted name per line, sorted.
var stdAPILockPath = filepath.Join("gen", "std-api.lock")

// stdAPINames lists the Buzz-facing member names of every std module: methods as
// <module>.<method>, namespace members as <module>.<namespace>.<member> and fields
// as <module>.<field>. The magus module is left out because magus-api.lock owns it.
func stdAPINames(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, m := range All() {
		if m.Name == "magus" {
			continue
		}
		for _, f := range m.Fields {
			out = append(out, m.Name+"."+CamelCase(f.Name))
		}
		for _, meth := range m.Methods {
			out = append(out, m.Name+"."+BuzzMethodName(meth))
		}
		for _, ns := range m.Namespaces {
			for _, meth := range ns.Methods {
				out = append(out, m.Name+"."+ns.Name+"."+BuzzMethodName(meth))
			}
		}
	}
	sort.Strings(out)
	return out
}

// TestStdAPILocked pins the std method names the way TestMagusAPILocked pins magus.*.
// A Method.Name is the identifier every magusfile calls, and Buzz reads a missing
// member as null, so a rename or removal loads fine and fails later with "null is not
// callable". Added members fail too, which is settled by regenerating the lock.
func TestStdAPILocked(t *testing.T) {
	got := stdAPINames(t)

	if os.Getenv("UPDATE_STD_API_LOCK") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(stdAPILockPath), 0o755))
		require.NoError(t, os.WriteFile(stdAPILockPath, []byte(strings.Join(got, "\n")+"\n"), 0o644))
		t.Logf("wrote %s (%d members)", stdAPILockPath, len(got))
		return
	}

	data, err := os.ReadFile(stdAPILockPath)
	require.NoError(t, err, "the std API lock must be committed; regenerate with `magus run magusfile-api-generate .`")
	want := strings.Fields(string(data))

	gotSet := map[string]bool{}
	for _, n := range got {
		gotSet[n] = true
	}
	wantSet := map[string]bool{}
	for _, n := range want {
		wantSet[n] = true
	}
	for _, n := range want {
		assert.Truef(t, gotSet[n],
			"%s was REMOVED or renamed in the std API.\n"+
				"A magusfile still calling it loads fine and fails at run time with "+
				"\"null is not callable\". Move every in-repo caller, list the change under "+
				"Breaking in the changelog, then regenerate the lock with "+
				"`magus run magusfile-api-generate .`", n)
	}
	for _, n := range got {
		assert.Truef(t, wantSet[n],
			"%s was ADDED to the std API; regenerate the lock with "+
				"`magus run magusfile-api-generate .`", n)
	}
}
