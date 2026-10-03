package std

import (
	"bytes"
	"context"
	"io"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// One session serves two callers; each call's context picks where io.stdout and
// io.stderr write, the way print's OutFunc does.
func TestStreamsRouteThroughStdoutAndStderrFunc(t *testing.T) {
	type streams struct{ out, err bytes.Buffer }
	var a, b streams
	pick := func(ctx context.Context) *streams {
		if ctx.Value(callerKey{}) == "b" {
			return &b
		}
		return &a
	}
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	require.NoError(t, sess.Provide(buzz.ModuleEnv{
		Out:        io.Discard,
		StdoutFunc: func(ctx context.Context) io.Writer { return &pick(ctx).out },
		StderrFunc: func(ctx context.Context) io.Writer { return &pick(ctx).err },
	}, Modules...))

	const program = `import "io"; io\stdout.write("to out"); io\stderr.write("to err");`
	require.NoError(t, sess.Exec(context.WithValue(context.Background(), callerKey{}, "a"), program))
	require.NoError(t, sess.Exec(context.WithValue(context.Background(), callerKey{}, "b"), program))
	for name, s := range map[string]*streams{"a": &a, "b": &b} {
		assert.Equal(t, "to out", s.out.String(), "caller %s stdout", name)
		assert.Equal(t, "to err", s.err.String(), "caller %s stderr", name)
	}
}
