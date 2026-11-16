package job

import (
	"fmt"
	"strings"
)

// EntryPoint is where a request entered magus. magus reads it from its own dispatch,
// never from anything the caller said about itself.
type EntryPoint string

const (
	// EntryPointCLI is a magus command run directly, by a person or by a program.
	EntryPointCLI EntryPoint = "cli"
	// EntryPointHook is `magus shell` called by a host's hook wiring.
	EntryPointHook EntryPoint = "hook"
	// EntryPointMCP is a tool call over MCP.
	EntryPointMCP EntryPoint = "mcp"
	// EntryPointRPC is an authenticated call on the server's HTTP API.
	EntryPointRPC EntryPoint = "rpc"
	// EntryPointServer is `magus server` acting on its own schedule.
	EntryPointServer EntryPoint = "server"
)

// Origin is where a record came from, as far as magus could see. Each field is bound to
// one channel, so the field says the provenance: User and UID are the OS account the
// writing process ran as, EntryPoint is where the request entered magus, Host is the
// label the host's wiring gave itself, and Session and Agent are the ids the host
// delivered.
//
// An empty field means that channel had nothing. It never means "a person": a record
// with no Session is unattributed, and User is what says whose account wrote it.
type Origin struct {
	// User is the OS username; UID is the OS user id, kept as the OS spells it (a
	// number on Unix, a SID on Windows) so uid 0 is not an empty value.
	User string `json:"user,omitempty" yaml:"user,omitempty"`
	UID  string `json:"uid,omitempty" yaml:"uid,omitempty"`
	// EntryPoint is where the request entered magus.
	EntryPoint EntryPoint `json:"entry_point,omitempty" yaml:"entry_point,omitempty"`
	// Host is the program running the session (an agent host, the console), as its wiring
	// named itself. A label, not a verified identity.
	Host string `json:"host,omitempty" yaml:"host,omitempty"`
	// Session is the host's conversation id, as the host delivered it.
	Session string `json:"session,omitempty" yaml:"session,omitempty"`
	// Agent is the host's subagent id within Session, empty for the main conversation.
	Agent string `json:"agent,omitempty" yaml:"agent,omitempty"`
	// Credential is the bearer a request presented, as the verifier that admitted it named
	// it. A bearer proves possession of that credential, not who holds it.
	Credential Credential `json:"credential,omitzero" yaml:"credential,omitempty"`
}

// CredentialKind is which kind of bearer a credential is. The verifier that admits a
// request defines the kinds; this module defines none.
type CredentialKind string

// Credential is what a request was admitted as, as an origin records it: which kind of
// bearer, which one, and what its owner called it. What the bearer may do is the
// verifier's, and no record keeps it. It never holds a secret.
type Credential struct {
	// Kind is which bearer this is, stored under "class".
	Kind CredentialKind `json:"class,omitempty" yaml:"class,omitempty" buzz:"class"`
	// ID names this bearer for as long as it lives, and is never its secret.
	ID string `json:"id,omitempty" yaml:"id,omitempty"`
	// Name is the label the bearer was issued under, "" for none.
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

// phrase renders c for a row head as its kind, name and id: "stored laptop (3fa9c1d2)",
// and "" for the zero credential.
func (c Credential) phrase() string {
	words := strings.TrimSpace(fmt.Sprintf("%s %s", c.Kind, c.Name))
	switch {
	case c.ID == "":
		return words
	case words == "":
		return c.ID
	}
	return fmt.Sprintf("%s (%s)", words, c.ID)
}

// Label renders the origin as one phrase for a row head: "eli", "eli via <host>",
// "eli via <host> via agent a1b2", "eli via stored laptop (3fa9c1d2)", "server". It is
// "unattributed" when no channel named anything. A reader that needs one field, a filter
// included, reads that field, never this.
func (o Origin) Label() string {
	if o.EntryPoint == EntryPointServer {
		return string(EntryPointServer)
	}
	label := o.User
	for _, via := range []string{o.Host, phrase("agent", o.Agent), o.Credential.phrase()} {
		switch {
		case via == "":
		case label == "":
			label = via
		default:
			label += " via " + via
		}
	}
	if label == "" {
		return "unattributed"
	}
	return label
}

func phrase(what, name string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%s %s", what, name)
}

// Names reports whether any channel of the origin (User, Host, Agent, the credential's
// kind, id or name, or the EntryPoint) is exactly name. It is what an activity filter
// matches on: each field separately, so "eli" never matches a host that happens to contain
// it and a change to [Origin.Label]'s wording cannot change what a filter selects. A
// credential's name is a reusable label and its id the identity, so both match.
func (o Origin) Names(name string) bool {
	if name == "" {
		return false
	}
	c := o.Credential
	for _, field := range []string{o.User, o.Host, o.Agent, string(c.Kind), c.ID, c.Name, string(o.EntryPoint)} {
		if field == name {
			return true
		}
	}
	return false
}
