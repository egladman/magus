package types

// Transport is the entry point a record arrived through. magus reads it from its own
// dispatch, never from anything the caller said about itself.
type Transport string

const (
	// TransportCLI is a magus command run directly, by a person or by a program.
	TransportCLI Transport = "cli"
	// TransportHook is `magus shell` called by a host's hook wiring.
	TransportHook Transport = "hook"
	// TransportMCP is a tool call on the MCP surface.
	TransportMCP Transport = "mcp"
	// TransportRPC is an authenticated call on the daemon's HTTP API.
	TransportRPC Transport = "rpc"
	// TransportDaemon is the daemon acting on its own schedule.
	TransportDaemon Transport = "daemon"
)

// Origin is where a record came from, as far as magus could see. Each field is bound to
// one channel, so the field says the provenance: User and UID are the OS account the
// writing process ran as, Transport is the entry point, Host is the label the host's
// wiring gave itself, and Session and Agent are the ids the host delivered.
//
// An empty field means that channel had nothing. It never means "a person": a record
// with no Session is unattributed, and User is what says whose account wrote it.
type Origin struct {
	// User is the OS username; UID is the OS user id, kept as the OS spells it (a
	// number on Unix, a SID on Windows) so uid 0 is not an empty value.
	User string `json:"user,omitempty" yaml:"user,omitempty"`
	UID  string `json:"uid,omitempty" yaml:"uid,omitempty"`
	// Transport is the entry point the record arrived through.
	Transport Transport `json:"transport,omitempty" yaml:"transport,omitempty"`
	// Host is the program running the session (an agent host, the console), as its wiring
	// named itself. A label, not a verified identity.
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
	// Session is the host's conversation id, as the host delivered it.
	Session string `json:"session,omitempty" yaml:"session,omitempty"`
	// Agent is the host's subagent id within Session, empty for the main conversation.
	Agent string `json:"agent,omitempty" yaml:"agent,omitempty"`
}

// Attributed reports whether a host delivered a session for this record. False is
// "unattributed", which is a fact about the channel and says nothing about who acted.
func (o Origin) Attributed() bool { return o.Session != "" }
