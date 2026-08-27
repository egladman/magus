package viewer

import (
	"errors"
	"strconv"

	"connectrpc.com/connect"
)

// page reads a page token as an offset into total items and returns the slice bounds plus the next
// token, empty at the end.
//
// An unparseable token errors rather than restarting at zero: a caller paging a list must not be
// handed page one again and take it for the end.
func page(total int, token string, size int) (from, to int, next string, err error) {
	if token != "" {
		from, err = strconv.Atoi(token)
		if err != nil || from < 0 {
			return 0, 0, "", connect.NewError(connect.CodeInvalidArgument, errors.New("viewer: bad page token "+strconv.Quote(token)))
		}
	}
	// The cap the proto's validate rule sets, for callers that bypass it (a test, direct Go).
	if size <= 0 || size > 5000 {
		size = 5000
	}
	if from > total {
		from = total
	}
	if to = from + size; to > total {
		to = total
	}
	if to == total {
		return from, to, "", nil
	}
	return from, to, strconv.Itoa(to), nil
}
