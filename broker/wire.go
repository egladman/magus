package broker

import (
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
)

// ProtocolVersion is the newest wire version this build speaks, and MinProtocolVersion
// the oldest it still answers. A hello offers a range and the broker picks the newest
// version both sides speak; a hello with no overlap is refused with CodeProtocol.
//
// The compatibility rule, which TestBrokerAnswersThePreviousProtocol enforces:
//
//   - Within a version, changes are additive only: a new frame type, a new optional
//     field, a new error code. A field is never renamed, retyped, removed or given a new
//     meaning, and the hello exchange never changes shape at all.
//   - A peer ignores a field it does not know, except inside a service acquire, where an
//     unknown field is a request this broker cannot honor (CodeUnsupported).
//   - A frame type a broker does not know is answered with CodeUnknownType, which is how
//     a newer client learns that an older broker predates it.
//   - An error code a client does not know is a failed request and nothing more.
//   - Anything else is a new ProtocolVersion, and a broker keeps answering the previous
//     version for at least one release: MinProtocolVersion stays at or below
//     ProtocolVersion-1 until that release has shipped.
const (
	ProtocolVersion    = 1
	MinProtocolVersion = 1
)

// helloMagic guards the first frame on a connection. Every later frame mutates state
// shared by every magus on the host (a claim against its memory, a release of one), so
// a connection that cannot say it is a broker client is closed before it can send one.
// It names the handshake, not the protocol version, and never changes.
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
	typeServiceAcquire = "service.acquire"
	typeServiceRelease = "service.release"
	typeServiceStopAll = "service.stopall"
	typeServiceReply   = "service.reply"
	typeStatus         = "status"
	typeStatusReply    = "status.reply"
	typeShutdown       = "shutdown"
	typeShutdownReply  = "shutdown.reply"
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

// hello is the first frame a client sends: who is on the other end, and the protocol
// versions it speaks. The broker copies the identity onto every claim this connection
// takes, so status can name a holder's command.
type hello struct {
	Magic string `json:"magic"`
	// Protocol is the newest version the client speaks, MinProtocol the oldest; zero
	// means the client speaks Protocol alone.
	Protocol    int      `json:"protocol"`
	MinProtocol int      `json:"min_protocol,omitzero"`
	PID         int      `json:"pid"`
	Dir         string   `json:"dir,omitempty"`
	Argv        []string `json:"argv,omitempty"`
	Version     string   `json:"version,omitempty"`
}

// helloReply is the broker's own identity and the version it chose for this connection.
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
}

// releaseRequest returns a granted claim. An id the broker does not hold, or one another
// connection took, is ignored.
type releaseRequest struct {
	ClaimID string `json:"claim_id"`
}

// serviceAcquireRequest acquires one reference to a shared service. The broker decodes
// it strictly: a member it does not know asks for something it would silently drop.
type serviceAcquireRequest struct {
	Key     string      `json:"key"`
	Service serviceWire `json:"service"`
}

// serviceWire is a ServiceSpec on the wire.
type serviceWire struct {
	Command   []string `json:"command"`
	Readiness []string `json:"readiness,omitempty"`
	Stop      []string `json:"stop,omitempty"`
	IdleMS    int64    `json:"idle_ms,omitzero"`
}

// serviceReleaseRequest drops one reference a service acquire took.
type serviceReleaseRequest struct {
	Key string `json:"key"`
}

// serviceReply answers a service request. Stopped is set by a stop-all.
type serviceReply struct {
	Stopped int `json:"stopped,omitzero"`
}

// status.reply's body is types.StatusBroker, whose JSON is part of this wire.

// shutdownRequest stops the broker. The magic is a second guard beside the hello's,
// since dropping every claim on the host is not something a stray frame should do.
type shutdownRequest struct {
	Magic string `json:"magic"`
}

// shutdownMagic is the value shutdownRequest.Magic must carry.
const shutdownMagic = "magus-broker-shutdown-v1"

// ErrorCode classifies an error frame, so a client decides what to do from the code
// rather than from the message. Codes are stable strings; new ones are additive.
type ErrorCode string

const (
	// CodeProtocol is a hello with the wrong magic or no protocol version in common, or
	// a reply the client cannot read.
	CodeProtocol ErrorCode = "protocol"
	// CodeMalformed is a frame the broker could not decode.
	CodeMalformed ErrorCode = "malformed"
	// CodeUnknownType is a frame type the broker does not speak.
	CodeUnknownType ErrorCode = "unknown-type"
	// CodeUnsupported is a well-formed request asking for something this broker cannot
	// honor, such as a service field it predates.
	CodeUnsupported ErrorCode = "unsupported"
	// CodeNoServices is a service request to a broker hosting no services.
	CodeNoServices ErrorCode = "no-services"
	// CodeService is a service that could not be started or never became ready.
	CodeService ErrorCode = "service"
)

// errorReply is the body of an error frame.
type errorReply struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
}
