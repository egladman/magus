package ward

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func service(bin string, args ...string) types.SpellOp {
	return types.SpellOp{Kind: types.OpKindService, Command: types.Command{Bin: bin, Args: args}}
}

func command(bin string, args ...string) types.SpellOp {
	return types.SpellOp{Command: types.Command{Bin: bin, Args: args}}
}

func TestDetachOnServiceIsError(t *testing.T) {
	for _, args := range [][]string{
		{"run", "-d", "postgres:15"},
		{"run", "--detach", "postgres:15"},
		{"run", "--detach=true", "postgres:15"},
		{"run", "-itd", "postgres:15"}, // combined short-flag block
	} {
		diags := Check("db", service("docker", args...))
		require.Len(t, diags, 1, "args=%v", args)
		assert.Equal(t, types.ServiceOpDetached, diags[0].Code, "args=%v", args)
	}
}

func TestServiceWithoutDetachIsClean(t *testing.T) {
	assert.Empty(t, Check("db", service("docker", "run", "-p", "5432:5432", "postgres:15")))
	// A full path to the binary still resolves by basename.
	assert.Empty(t, Check("db", service("/usr/bin/docker", "run", "postgres:15")))
}

func TestDetachFlagScopedToContainerBins(t *testing.T) {
	// dnsmasq -d means "run in the foreground" - the OPPOSITE of detach - so it must
	// not be flagged. This is why the ward is bin-scoped, not a universal -d match.
	assert.Empty(t, Check("dns", service("dnsmasq", "-d", "--port", "5353")))
}

func TestWatchOnCommandIsError(t *testing.T) {
	diags := Check("typecheck", command("tsc", "--watch"))
	require.Len(t, diags, 1)
	assert.Equal(t, types.CommandOpNeverExits, diags[0].Code)

	// Through a runner (npx tsc --watch) the tool is looked through.
	diags = Check("typecheck", command("npx", "tsc", "--watch"))
	require.Len(t, diags, 1)
	assert.Equal(t, types.CommandOpNeverExits, diags[0].Code)
}

func TestWatchOnServiceIsClean(t *testing.T) {
	// A watcher modeled as a SERVICE op is correct - that is exactly the fix.
	assert.Empty(t, Check("dev", service("tsc", "--watch")))
}

func TestWatchScopedToWatchTools(t *testing.T) {
	// --watch on an unrelated tool is not assumed to be a never-exiting loop.
	assert.Empty(t, Check("build", command("make", "--watch")))
}

func TestNonWatchCommandClean(t *testing.T) {
	assert.Empty(t, Check("build", command("tsc", "--noEmit")))
	assert.Empty(t, Check("build", command("go", "build", "./...")))
}
