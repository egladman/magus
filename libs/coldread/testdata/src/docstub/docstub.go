// Package docstub holds doc comments that only repeat the name, and the ones
// that say one thing more.
package docstub

// want +2 `docstub: doc comment only repeats the name Client`

// Client is a Client.
type Client struct{}

// want +2 `docstub: doc comment only repeats the name NewClient`

// NewClient creates a new Client.
func NewClient() *Client { return &Client{} }

// want +2 `docstub: doc comment only repeats the name Close`

// Close ...
func (c *Client) Close() {}

// want +2 `docstub: doc comment only repeats the name DefaultTimeout`

// DefaultTimeout is the default timeout.
const DefaultTimeout = 3

// NewServer validates the config and does not dial.
func NewServer() {}

// String implements fmt.Stringer.
func (c *Client) String() string { return "" }

// Reset returns the client to its zero state; it is safe on a nil client.
func (c *Client) Reset() {}

// Options is a Options.
//
// The second paragraph is the contract, so the first line is scaffolding.
type Options struct{}

// A block doc describes the group, never one name.
const (
	A = 1
	B = 2
)
