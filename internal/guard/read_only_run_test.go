package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A worker runs any target read-only over the projects holding its write paths, so tests it
// wrote under a lint check do not ship unrun. A run that writes back, one over another project,
// and a build of ./magus are still refused.
func TestWorkerCheckOnlyLetsAReadOnlyRunOverItsOwnPaths(t *testing.T) {
	row := testCheckLease()
	row.Check.Target = "lint"
	ctx, _ := fleetFixture(t, row)
	deps := composingWorkspace(t)

	for _, command := range []string{
		"./magus run test . --no-default-charms",
		"./magus run go::go-test . --no-default-charms -- -run TestLease",
	} {
		assert.Empty(t, denyWorkerCheckOnly(ctx, deps, row.ID, command), command)
	}
	for _, command := range []string{
		"./magus run test .",
		"./magus run format:rw . --no-default-charms",
		"./magus run go-test docs --no-default-charms",
		"./magus affected test --no-default-charms",
	} {
		assert.True(t, denyWorkerCheckOnly(ctx, deps, row.ID, command).refused(), command)
	}
	assert.Contains(t, denyWorkerCheckOnly(ctx, deps, row.ID, "./magus run go-build . --no-default-charms").Say, "builds ./magus")
}
