package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadDigestIgnoresWhatOnlyShapesTheProcess(t *testing.T) {
	base := Config{}
	shaped := Config{Concurrency: 8, DryRun: true}
	shaped.Server.Enabled = true
	assert.Equal(t, ReadDigest(base), ReadDigest(shaped))
}

func TestReadDigestSeesWhatShapesAnAnswer(t *testing.T) {
	changed := Config{}
	changed.Cache.Dir = "elsewhere"
	assert.NotEqual(t, ReadDigest(Config{}), ReadDigest(changed))
}

func TestEnvDigestHidesTheValue(t *testing.T) {
	d := EnvDigest("secret")
	assert.Len(t, d, 32)
	assert.NotContains(t, d, "secret")
	assert.Equal(t, d, EnvDigest("secret"))
}
