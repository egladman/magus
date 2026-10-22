package broker

import (
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ProtocolVersion is the wire version the hello exchange carries. A broker refuses a
// client speaking another one with CodeProtocol.
const ProtocolVersion = 1

// helloMagic guards the first frame on a connection. Every later frame mutates state
// shared by every magus on the host (a claim against its memory, a release of one), so
// a connection that cannot say it is a broker client is closed before it can send one.
const helloMagic = "magus-broker-v1"

// Frame types. A request carries a non-zero ID and its reply echoes it, so one
// connection carries any number of requests at once. A later wait frame for a queued
// claim has room here: it would share the claim's ID and precede its reply.
const (
	typeHello          = "hello"
	typeHelloReply     = "hello.reply"
	typeClaim          = "claim"
	typeClaimReply     = "claim.reply"
	typeRelease        = "release"
	typeReleaseReply   = "release.reply"
	typeOverrun        = "overrun"
	typeOverrunReply   = "overrun.reply"
	typeReserve        = "reserve"
	typeReserveReply   = "reserve.reply"
	typeServiceAcquire = "service.acquire"
	typeServiceRelease = "service.release"
	typeServiceStopAll = "service.stopall"
	typeServiceReply   = "service.reply"
	typeStatus         = "status"
	typeStatusReply    = "status.reply"
	typeShutdown       = "shutdown"
	typeShutdownReply  = "shutdown.reply"
	typeCrashWatch     = "crash.watch"
	typeCrashReply     = "crash.reply"
	typeHold           = "hold"
	typeHoldRelease    = "hold.release"
	typeHoldReply      = "hold.reply"
	typeWork           = "work"
	typeWorkReply      = "work.reply"
	typeError          = "error"
)

// maxFrameBytes caps one frame. The socket is private to this user, but a client is
// still untrusted input and a line without a newline would otherwise grow forever.
const maxFrameBytes = 4 << 20

// frame is one line on the wire: a type, the request it belongs to, and a body whose
// shape the type decides.
type frame struct {
	Type string          `json:"type"`
	ID   uint64          `json:"id,omitzero"`
	Body json.RawMessage `json:"body,omitempty"`
}

// hello is the first frame a client sends: who is on the other end. The broker copies
// it onto every claim this connection takes, so status can name a holder's command.
type hello struct {
	Magic    string   `json:"magic"`
	Protocol int      `json:"protocol"`
	PID      int      `json:"pid"`
	Dir      string   `json:"dir,omitempty"`
	Argv     []string `json:"argv,omitempty"`
	Version  string   `json:"version,omitempty"`
	// Lease is the job lease the process acts under, from its BAGGAGE; "" for a run
	// nobody leased, which no validation cap counts.
	Lease string `json:"lease,omitempty"`
	// Tree is the root of Lease's job tree and MaxValidations the workspace's
	// jobs.max_validations: how many runs of that tree's leased workers the broker seats
	// at once. Zero is no cap.
	Tree           string `json:"tree,omitempty"`
	MaxValidations int    `json:"max_validations,omitzero"`
	// Token is the secret kept beside the broker socket; a hello without it gets CodeAuth.
	Token string `json:"token,omitempty"`
}

// helloReply is the broker's own identity.
type helloReply struct {
	PID      int    `json:"pid"`
	Protocol int    `json:"protocol"`
	Version  string `json:"version,omitempty"`
}

// claimRequest asks for a seat. Reassert records the claim without asking whether it
// fits: its step is already running under a broker that has since died.
type claimRequest struct {
	Claim    types.MachineClaim `json:"claim"`
	Reassert bool               `json:"reassert,omitzero"`
}

// claimReply is the verdict. It answers at once; a claim that does not fit is not
// recorded, and whether to ask again is the client's call.
type claimReply struct {
	Verdict types.MachineVerdict `json:"verdict"`
	// Seat is set on a claim kept out by its job tree's validation cap: the run waits,
	// and this names the runs it waits on.
	Seat *SeatWait `json:"seat,omitempty"`
}

// reserveRequest asks for a seat held for another process, Reader, until its first
// claim. It does not ride the asking connection: `status --wait` exits before the run
// reading its output may start.
type reserveRequest struct {
	Claim  types.MachineClaim `json:"claim"`
	Reader int                `json:"reader"`
}

// reserveReply is the verdict. Granted means the seat is held for the reader.
type reserveReply struct {
	Verdict types.MachineVerdict `json:"verdict"`
}

// holdRequest registers a command a client runs under a deadline.
type holdRequest struct {
	Hold Hold `json:"hold"`
}

// holdReply names the hold a request registered or released.
type holdReply struct {
	ID string `json:"id"`
}

// holdReleaseRequest says a held command ended on its own.
type holdReleaseRequest struct {
	ID string `json:"id"`
}

// releaseRequest returns a granted claim. An id the broker does not hold, or one another
// connection took, is ignored.
type releaseRequest struct {
	ClaimID string `json:"claim_id"`
}

// overrunRequest says a claim this connection holds went over its memory curve. An id
// it does not hold is ignored, as a release's is. The reply carries no body.
type overrunRequest struct {
	ClaimID string `json:"claim_id"`
}

// serviceAcquireRequest acquires one reference to a shared service.
type serviceAcquireRequest struct {
	Key     string      `json:"key"`
	Service serviceWire `json:"service"`
}

// serviceWire is a ServiceSpec on the wire.
type serviceWire struct {
	Command   []string `json:"command"`
	Start     []string `json:"start,omitempty"`
	Readiness []string `json:"readiness,omitempty"`
	Stop      []string `json:"stop,omitempty"`
	IdleMS    int64    `json:"idle_ms,omitzero"`
}

// serviceReleaseRequest drops one reference a service acquire took.
type serviceReleaseRequest struct {
	Key string `json:"key"`
}

// serviceReply answers a service request. Owned is set by an acquire of a service the
// broker started, and Stopped by a stop-all.
type serviceReply struct {
	Owned   bool `json:"owned,omitzero"`
	Stopped int  `json:"stopped,omitzero"`
}

// shutdownRequest stops the broker. The magic is a second guard beside the hello's,
// since dropping every claim on the host is not something a stray frame should do.
type shutdownRequest struct {
	Magic string `json:"magic"`
}

// shutdownMagic is the value shutdownRequest.Magic must carry.
const shutdownMagic = "magus-broker-shutdown-v1"

// crashWatchRequest asks the broker to watch this process for a crash. The frame carries
// two file descriptors beside it: the read end of the pipe the process's runtime copies
// a fatal crash report to, then the process's stderr. Hint is written to that stderr
// after the report, so it reads after Go's own trace.
type crashWatchRequest struct {
	Hint string `json:"hint"`
}

// ErrorCode classifies an error frame, so a client decides what to do from the code
// rather than from the message.
type ErrorCode string

const (
	// CodeProtocol is a hello with the wrong magic or protocol version.
	CodeProtocol ErrorCode = "protocol"
	// CodeAuth is a hello without the token the broker keeps beside its socket.
	CodeAuth ErrorCode = "auth"
	// CodeMalformed is a frame the broker could not decode.
	CodeMalformed ErrorCode = "malformed"
	// CodeUnknownType is a frame type the broker does not speak.
	CodeUnknownType ErrorCode = "unknown-type"
	// CodeNoServices is a service request to a broker hosting no services.
	CodeNoServices ErrorCode = "no-services"
	// CodeService is a service that could not be started or never became ready.
	CodeService ErrorCode = "service"
	// CodeNoCrashWatch is a crash watch the broker cannot take: the frame arrived without
	// its two file descriptors, as it does on a platform that cannot pass them.
	CodeNoCrashWatch ErrorCode = "no-crash-watch"
	// CodeDraining is a new claim or service reference asked of a broker that is shutting
	// down: it seats nothing new while the runs it holds finish. The next run to find no
	// broker starts another.
	CodeDraining ErrorCode = "draining"
)

// errorReply is the body of an error frame.
type errorReply struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}
