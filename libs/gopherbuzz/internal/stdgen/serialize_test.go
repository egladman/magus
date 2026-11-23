package stdgen

import (
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/stretchr/testify/require"
)

func TestSerialize(t *testing.T) {
	first, err := Serialize("../..")
	require.NoError(t, err)
	second, err := Serialize("../..")
	require.NoError(t, err)
	require.Equal(t, first, second)

	session := buzz.NewSession(t.Context())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	session.SetModuleDecls("serializecore", string(SerializeCore()))
	session.SetModuleDecls("serialize", string(first))
	require.NoError(t, session.Exec(t.Context(), `import "serialize";`))
}
