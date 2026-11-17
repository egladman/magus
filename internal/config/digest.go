package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// EnvDigests maps each variable in this process's environment to EnvDigest of its value.
func EnvDigests() map[string]string {
	out := make(map[string]string)
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		out[name] = EnvDigest(value)
	}
	return out
}

// EnvDigest is the 16-byte sha256 prefix of one variable's value, hex-encoded.
func EnvDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:16])
}

// ReadDigest fingerprints the configuration a graph read depends on. The fields
// cleared here shape the process (logging, concurrency, the server and its endpoints) and
// never an answer; a difference anywhere else makes a server decline. "" when cfg cannot
// be encoded, which no server accepts.
func ReadDigest(cfg Config) string {
	cfg.Telemetry = Telemetry{}
	cfg.Server = Server{}
	cfg.MCP = MCP{}
	cfg.Console = Console{}
	cfg.Report = Report{}
	cfg.Log = Log{}
	cfg.Hints = Hints{}
	cfg.Jobs = Jobs{}
	cfg.Concurrency = 0
	cfg.ConcurrencyProfile = ""
	cfg.Broker = ""
	cfg.ShutdownGrace = 0
	cfg.MaxFailures = 0
	cfg.TargetTimeout = 0
	cfg.StallTimeout = 0
	cfg.DryRun = false
	cfg.DefaultCharms = nil
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
