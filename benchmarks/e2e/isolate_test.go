// cross-cutting: the package's TestMain, which keeps every test here off the user's runtime dir

package e2e

import (
	"testing"

	"github.com/egladman/magus/internal/testenv"
)

func TestMain(m *testing.M) { testenv.Main(m) }
