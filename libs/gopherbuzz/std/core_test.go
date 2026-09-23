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

type callerKey struct{}

const printHello = `import "std"; std\print("hello");`

func TestPrintWritesToOut(t *testing.T) {
	var out bytes.Buffer
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	RegisterWithOutput(sess, &out)

	require.NoError(t, sess.Exec(context.Background(), printHello))
	assert.Equal(t, "hello\n", out.String())
}

// One session serves two callers; each call's context picks its own writer.
func TestPrintRoutesPerCallWithOutputFunc(t *testing.T) {
	var a, b bytes.Buffer
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	RegisterWithOutputFunc(sess, func(ctx context.Context) io.Writer {
		if ctx.Value(callerKey{}) == "b" {
			return &b
		}
		return &a
	})

	require.NoError(t, sess.Exec(context.WithValue(context.Background(), callerKey{}, "a"), printHello))
	require.NoError(t, sess.Exec(context.WithValue(context.Background(), callerKey{}, "b"), printHello))
	assert.Equal(t, "hello\n", a.String())
	assert.Equal(t, "hello\n", b.String())
}

func TestPrintFailsWhenOutputFuncReturnsNil(t *testing.T) {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	RegisterWithOutputFunc(sess, func(context.Context) io.Writer { return nil })

	var err error
	require.NotPanics(t, func() { err = sess.Exec(context.Background(), printHello) })
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no output writer")
}

func TestBindRefusesBothOutAndOutFunc(t *testing.T) {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()
	err := sess.Provide(buzz.ModuleEnv{
		Ctx:     context.Background(),
		Out:     io.Discard,
		OutFunc: func(context.Context) io.Writer { return io.Discard },
	}, Modules...)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both Out and OutFunc")
}
