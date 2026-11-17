package interp

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// opens is an opener that finds no workspace and counts how often it was asked.
type opens struct {
	mu  sync.Mutex
	n   int
	err error
}

func (o *opens) open(context.Context, string) (*ScriptWorkspace, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.n++
	return nil, o.err
}

func (o *opens) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

func runnerOver(o *opens) *ScriptRunner {
	return &ScriptRunner{Open: o.open, Adopted: func(context.Context) bool { return false }}
}

func TestScriptRunner_ContextRefusesAFailedLoadUnderTheSandbox(t *testing.T) {
	o := &opens{err: errors.New("magusfile.buzz:1: broken on purpose")}
	r := runnerOver(o)

	r.Sandbox = true
	_, err := r.Context(t.Context(), t.TempDir())
	require.ErrorIs(t, err, types.WorkspaceLoadFailed)
	assert.ErrorContains(t, err, "broken on purpose")
	assert.Equal(t, 1, o.count())

	// Adopted, so the open is attempted with the sandbox off too.
	r.Sandbox = false
	r.Adopted = func(context.Context) bool { return true }
	_, err = r.Context(t.Context(), t.TempDir())
	require.NoError(t, err, "with the sandbox off a failed load still only warns")
	assert.Equal(t, 2, o.count())
}

// A script run carries one evaluation holder, which std's graph members read.
// So two magus\graph().refs calls in one script build the symbol graph once.
func TestScriptRunner_RunContextCarriesOneEvaluation(t *testing.T) {
	ctx, err := runnerOver(&opens{}).RunContext(t.Context(), t.TempDir())
	require.NoError(t, err)
	once := types.EvalOnceFromContext(ctx)
	require.NotNil(t, once, "graph reads in a script would each rebuild the graph")

	builds := 0
	for range 2 {
		_, err := once.Do("graph+symbols", func() (any, error) {
			builds++
			return nil, nil
		})
		require.NoError(t, err)
	}
	assert.Equal(t, 1, builds)
}

func TestScriptRunner_ContextCarriesTheScriptsOwnInvocationAncestor(t *testing.T) {
	ctx, err := runnerOver(&opens{}).Context(t.Context(), t.TempDir())
	require.NoError(t, err)
	ancestors := types.InvocationAncestorsFromContext(ctx)
	require.NotEmpty(t, ancestors, "a magus the script spawns would otherwise be blind to its ancestry")
	assert.True(t, strings.HasPrefix(ancestors[len(ancestors)-1], strconv.Itoa(os.Getpid())+":"), "%v", ancestors)
}
