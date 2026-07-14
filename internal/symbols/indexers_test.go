package symbols

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInstallHint(t *testing.T) {
	hint := InstallHint("typescript")
	assert.Contains(t, hint, "scip-typescript", "names the indexer binary")
	assert.Contains(t, hint, "https://github.com/", "carries the install URL")

	assert.Empty(t, InstallHint(""), "no hint for a language with no indexer")
	assert.Empty(t, InstallHint("cobol"))
}
