package environ

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestOverlaysAreIndependent is the server case: two runs in one process each set a
// variable, and neither the other run nor the process environment sees it.
func TestOverlaysAreIndependent(t *testing.T) {
	t.Setenv("RUNENV_SHARED", "process")
	a, b := With(context.Background()), With(context.Background())

	From(a).Set("RUNENV_SHARED", "a")
	From(a).Set("RUNENV_ONLY_A", "1")
	From(b).Unset("RUNENV_SHARED")

	v, ok := Lookup(a, "RUNENV_SHARED")
	assert.Equal(t, "a", v)
	assert.True(t, ok)
	_, ok = Lookup(b, "RUNENV_SHARED")
	assert.False(t, ok, "b unset it for itself")
	_, ok = Lookup(b, "RUNENV_ONLY_A")
	assert.False(t, ok, "a's set must not reach b")

	assert.Equal(t, "process", os.Getenv("RUNENV_SHARED"), "the process environment is untouched")
	_, ok = os.LookupEnv("RUNENV_ONLY_A")
	assert.False(t, ok)

	v, ok = Lookup(context.Background(), "RUNENV_SHARED")
	assert.Equal(t, "process", v, "no overlay reads the process")
	assert.True(t, ok)
}

func TestApply(t *testing.T) {
	ctx := With(context.Background())
	env := []string{"KEEP=1", "CHANGE=old", "DROP=x"}
	assert.Equal(t, env, From(ctx).Apply(env), "an empty overlay returns env as is")
	assert.Equal(t, env, (*Overlay)(nil).Apply(env))

	From(ctx).Set("CHANGE", "new")
	From(ctx).Set("ADD", "a=b")
	From(ctx).Unset("DROP")
	assert.Equal(t, []string{"KEEP=1", "ADD=a=b", "CHANGE=new"}, From(ctx).Apply(env))
}
