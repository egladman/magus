package buzz

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCachedImportKeepsItsOwnExports(t *testing.T) {
	dir := writeTxtar(t, `
-- library.buzz --
namespace shared\common;
export fun answer() > str { return "common"; }
-- rival.buzz --
export fun answer() > str { return "rival"; }
-- first.buzz --
import "library";
export fun read() > str { return library\answer(); }
-- second.buzz --
namespace shared\consumer;
import "rival";
import "library";
export fun basename() > str { return library\answer(); }
export fun absolute() > str { return shared\common\answer(); }
export fun relative() > str { return common\answer(); }
`)
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer s.Close()
	s.SetIncludeDirs([]string{dir})
	require.NoError(t, s.Exec(ctx, `
import "first" as first;
import "second" as second;
final initial = first\read();
final basename = second\basename();
final absolute = second\absolute();
final relative = second\relative();
`))
	for _, name := range []string{"initial", "basename", "absolute", "relative"} {
		require.Equal(t, "common", s.Globals()[name].String(), name)
	}
}

func TestCachedDeclarationsKeepTheirOwnExports(t *testing.T) {
	dir := writeTxtar(t, `
-- rival.buzz --
export fun answer() > str { return "rival"; }
-- first.buzz --
import "common";
export fun read() > str { return common\answer(); }
-- second.buzz --
import "rival";
import "common";
export fun read() > str { return common\answer(); }
`)
	ctx := context.Background()
	s := NewSession(ctx, WithEmbedded())
	defer s.Close()
	s.SetIncludeDirs([]string{dir})
	s.SetModuleDecls("common", `namespace common; export fun answer() > str { return "common"; }`)
	require.NoError(t, s.Exec(ctx, `
import "first" as first;
import "second" as second;
final initial = first\read();
final cached = second\read();
`))
	for _, name := range []string{"initial", "cached"} {
		require.Equal(t, "common", s.Globals()[name].String(), name)
	}
}
