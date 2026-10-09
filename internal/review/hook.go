package review

// prePushHook is a POSIX sh script git runs before a push, with one line per ref on stdin:
// `<local ref> <local sha> <remote ref> <remote sha>`.
const prePushHook = `#!/bin/sh
# Lists the hunks of what you are about to push that nothing marks as read. It only
# prints, to stderr, and always exits 0: the push is never held up.
command -v magus >/dev/null 2>&1 || exit 0

while read -r lref lsha rref rsha; do
	case "$lsha" in
	*[!0]*) ;;
	*) continue ;;
	esac

	base=$rsha
	case "$rsha" in
	*[!0]*) ;;
	*)
		# A new branch: compare against where it left the default branch.
		upstream=$(git symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null) || upstream=origin/main
		base=$(git merge-base "$upstream" "$lsha" 2>/dev/null) || continue
		;;
	esac

	magus diff --unread --rev "$base...$lsha" </dev/null >&2 || true
done

exit 0
`

// PrePushHook returns a pre-push git hook that runs "magus diff --unread" on each range being
// pushed. The script exits 0 whatever the report says. magus prints it and never installs
// it: a person decides what runs inside their own repository.
func PrePushHook() string { return prePushHook }
