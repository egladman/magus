package review

import "fmt"

// ThreadLookupError says why an id named no thread when the review itself explains it: it was
// only partly read, or nothing was read at all.
func ThreadLookupError(err error, reason string, comments int) error {
	switch {
	case reason != "":
		return fmt.Errorf("the review was only partly read: %s: %w", reason, err)
	case comments == 0:
		return fmt.Errorf("no comments were found: %w", err)
	}
	return err
}
