package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A context with no profile hands back a nil *Profile. Kept, it would put the
// session on its observed path for nothing.
func TestAddCompileObserverIgnoresANilProfile(t *testing.T) {
	sess := NewSession(context.Background())
	sess.AddCompileObserver(ProfileFromContext(context.Background()))
	// Not assert.Nil: it reads a nil pointer inside an interface as nil.
	assert.True(t, sess.compileObserver == nil, "observer: %#v", sess.compileObserver)
}

func TestProfileRecordsWhatItWasAdded(t *testing.T) {
	ctx := context.Background()
	p := NewProfile()
	sess := NewSession(ctx, WithEmbedded())
	sess.AddCompileObserver(ProfileFromContext(WithProfile(ctx, p)))
	require.NoError(t, sess.Exec(ctx, "var n = 1;"))
	assert.Contains(t, p.Report(), "compile")
}
