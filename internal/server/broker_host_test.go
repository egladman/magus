package server

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func TestAnUnreadablePeerSaysWhatToRun(t *testing.T) {
	err := unknownBrokerError("/run/broker.sock", errors.ErrUnsupported, types.PeerFromAnotherBuild)
	assert.ErrorContains(t, err, "`lsof -U`")
	assert.ErrorContains(t, err, "/run/broker.sock")
	assert.ErrorContains(t, err, "`kill -TERM <pid>`")
}

func TestAdmissionsLiveBesideTheBrokerLog(t *testing.T) {
	assert.Equal(t, filepath.Join(filepath.Dir(BrokerLogPath()), "admissions.jsonl"), AdmissionLogPath())
}
