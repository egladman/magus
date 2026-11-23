package stdgen

import (
	"os"
	"path/filepath"
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
	for name, rendered := range map[string][]byte{
		"serialize.buzz": first, "serializecore.buzz": SerializeCore(),
	} {
		embedded, err := os.ReadFile(filepath.Join("../../std/gen", name))
		require.NoError(t, err)
		require.Equal(t, string(rendered), string(embedded), "%s is stale", name)
	}

	session := buzz.NewSession(t.Context())
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	session.SetModuleDecls("serializecore", string(SerializeCore()))
	session.SetModuleDecls("serialize", string(first))
	require.NoError(t, session.Exec(t.Context(), `import "serialize";`))
}
