package broker

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A protocol bump moves the socket with it, so two protocols never share one.
func TestTheSocketNameNamesTheProtocol(t *testing.T) {
	assert.Equal(t, fmt.Sprintf("broker-%d.sock", ProtocolVersion), SocketName)
}
