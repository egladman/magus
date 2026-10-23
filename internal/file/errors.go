package file

import "errors"

// ErrSkipWrite, returned by a store's update fn, ends the update without writing and
// without an error: the fn looked and found nothing to change.
var ErrSkipWrite = errors.New("skip this write")
