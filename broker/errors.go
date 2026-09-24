package broker

import "errors"

// ErrUnavailable is returned, wrapped, when no broker answers: nothing is listening,
// the connection closed under a request, or the broker refused the hello. Under
// `broker: required` a step refuses with it; under best-effort the step runs
// unarbitrated.
var ErrUnavailable = errors.New("broker: unavailable")

// Error is an error frame a broker answered with, or a reply the client could not read
// (CodeProtocol). Code, not Message, is what to act on: match it with errors.Is against
// the sentinel for its code, or read Code after errors.As.
type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// Is matches any *Error with the same code, so errors.Is(err, ErrNoServices) holds
// whatever the message says.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// One sentinel per error code, for errors.Is.
var (
	ErrProtocol    = &Error{Code: CodeProtocol, Message: "broker: protocol mismatch"}
	ErrMalformed   = &Error{Code: CodeMalformed, Message: "broker: malformed frame"}
	ErrUnknownType = &Error{Code: CodeUnknownType, Message: "broker: unknown frame type"}
	ErrUnsupported = &Error{Code: CodeUnsupported, Message: "broker: unsupported request"}
	ErrNoServices  = &Error{Code: CodeNoServices, Message: "broker: this broker hosts no services"}
	ErrService     = &Error{Code: CodeService, Message: "broker: service failed"}
)
