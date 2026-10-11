package review

import (
	"regexp"

	"github.com/egladman/magus/types"
)

var (
	// A GitHub pull request number.
	githubNumber = regexp.MustCompile(`^[0-9]+$`)
	// A GitHub owner/name, with no quote or space a shell line could be broken by.
	githubRepo = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	// A GitHub login, bots included, with no quote or space a shell line could be broken by.
	githubLogin = regexp.MustCompile(`^[A-Za-z0-9_.-]+(\[bot\])?$`)
)

// ReadingCommand is the gh command that tells a GitHub pull request's participants the reader is
// on it, or "" for any other forge: only GitHub's shape is known, and a guessed command for a
// forge magus has not seen would be one the person had to debug before running. host is the
// remote's host name, "github.com" for a pull request this command can address.
//
// Every part is checked against the shape GitHub gives it before it reaches a shell line the
// person will paste, because the repo and the login arrive from a provider and a remote.
func ReadingCommand(at types.ReviewTarget, host string) string {
	if host != "github.com" || !githubNumber.MatchString(at.ID) || !githubRepo.MatchString(at.Repo) {
		return ""
	}
	who := "A reviewer"
	if githubLogin.MatchString(at.Viewer) {
		who = at.Viewer
	}
	return "gh pr comment " + at.ID + " --repo " + at.Repo + " --body '" + who + " is reading this now'"
}
