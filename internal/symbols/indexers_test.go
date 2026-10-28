package symbols

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstallHint(t *testing.T) {
	assert.Contains(t, InstallHint("buzz"), "scip-buzz", "names the indexer binary")
	assert.Contains(t, InstallHint("buzz"), "libs/scipbuzz", "carries the install URL")

	assert.Empty(t, InstallHint(""), "no hint for a language with no indexer")
	assert.Empty(t, InstallHint("cobol"))
	assert.Empty(t, InstallHint("go"), "Go is indexed in process: nothing to install")
}

// The hint names the binary the spell declares, which need not be the one the table
// knows, and looks nothing up: whether it is installed is the caller's question.
func TestMissingIndexerHint(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	assert.Equal(t, "the buzz SCIP indexer (my-scip-buzz) is not installed; get it from https://github.com/egladman/magus/tree/main/libs/scipbuzz",
		MissingIndexerHint("buzz", "my-scip-buzz"))
	assert.Equal(t, "the cobol SCIP indexer (scip-cobol) is not installed", MissingIndexerHint("cobol", "scip-cobol"))
	assert.Empty(t, MissingIndexerHint("go", ""), "no binary, nothing to install")
}
