package review

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestReadingCommandRefusesWhatIsNotSafeToPaste(t *testing.T) {
	const host = "github.com"
	for name, at := range map[string]types.ReviewTarget{
		"id not a number": {ID: "482; rm -rf ~", Repo: "acme/acme"},
		"repo with quote": {ID: "482", Repo: "acme/acme'x"},
		"repo with space": {ID: "482", Repo: "acme/ac me"},
	} {
		assert.Empty(t, ReadingCommand(at, host), name)
	}
	assert.Equal(t, "gh pr comment 7 --repo acme/acme --body 'A reviewer is reading this now'",
		ReadingCommand(types.ReviewTarget{ID: "7", Repo: "acme/acme", Viewer: "x'; echo hi; '"}, host),
		"a login that is not a login is dropped, not quoted")
}

func TestReadingCommandNamesTheViewerAndAcceptsABotLogin(t *testing.T) {
	assert.Equal(t, "gh pr comment 482 --repo acme/acme --body 'eli is reading this now'",
		ReadingCommand(types.ReviewTarget{ID: "482", Repo: "acme/acme", Viewer: "eli"}, "github.com"))
	assert.Equal(t, "gh pr comment 482 --repo acme/acme --body 'dependabot[bot] is reading this now'",
		ReadingCommand(types.ReviewTarget{ID: "482", Repo: "acme/acme", Viewer: "dependabot[bot]"}, "github.com"))
}

func TestReadingCommandIsEmptyForAnotherForge(t *testing.T) {
	assert.Empty(t, ReadingCommand(types.ReviewTarget{ID: "7", Repo: "acme/acme"}, "gitlab.com"))
	assert.Empty(t, ReadingCommand(types.ReviewTarget{ID: "7", Repo: "acme/acme"}, ""))
}
