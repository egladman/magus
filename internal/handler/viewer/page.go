package viewer

import (
	"errors"
	"strconv"

	"connectrpc.com/connect"
)

// page reads a page token as an offset into a collection of total items and returns the slice
// bounds plus the next token, empty when the page reaches the end.
//
// The token IS an offset: these collections are read whole from the store on every call, so there
// is no cursor to resume from and a token shaped like one would promise a streaming read nothing
// performs. An unparseable token errors rather than silently restarting at zero - a caller paging
// a list must not be handed page one again and take it for the end.
func page(total int, token string, size int) (from, to int, next string, err error) {
	if token != "" {
		from, err = strconv.Atoi(token)
		if err != nil || from < 0 {
			return 0, 0, "", connect.NewError(connect.CodeInvalidArgument, errors.New("viewer: bad page token "+strconv.Quote(token)))
		}
	}
	// Capped the way the proto's own validate rule caps it, for the case that rule is not
	// enforced (a direct Go caller, a test).
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
