package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBrokerUnitsRenderWhatTheSupervisorReads pins the units byte for byte, with a
// binary path that needs quoting and a socket path carrying systemd's specifier
// character: a unit that parses differently from what was printed is a broker that
// never starts.
func TestBrokerUnitsRenderWhatTheSupervisorReads(t *testing.T) {
	f := unitFacts{
		exe:       "/opt/my tools/magus",
		socket:    "/run/user/1000/magus%/broker.sock",
		log:       "/home/eli/.local/state/magus/broker.log",
		home:      "/home/eli",
		configDir: "/home/eli/.config",
		env:       [][2]string{{"TMPDIR", "/var/folders/x&y/T/"}},
	}

	systemd, err := renderBrokerUnits(supervisorSystemd, f)
	require.NoError(t, err)
	assert.Equal(t, []brokerUnit{
		{Supervisor: "systemd", Path: "/home/eli/.config/systemd/user/magus-broker.socket", Content: `[Unit]
Description=magus broker socket

[Socket]
ListenStream=/run/user/1000/magus%%/broker.sock
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`},
		{Supervisor: "systemd", Path: "/home/eli/.config/systemd/user/magus-broker.service", Content: `[Unit]
Description=magus broker: this host's capacity and shared services
Requires=magus-broker.socket
After=magus-broker.socket

[Service]
ExecStart="/opt/my tools/magus" broker
`},
	}, systemd)

	launchd, err := renderBrokerUnits(supervisorLaunchd, f)
	require.NoError(t, err)
	assert.Equal(t, []brokerUnit{{Supervisor: "launchd", Path: "/home/eli/Library/LaunchAgents/magus.broker.plist", Content: `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>magus.broker</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/my tools/magus</string>
    <string>broker</string>
    <string>--idle-exit</string>
    <string>0</string>
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>TMPDIR</key>
    <string>/var/folders/x&amp;y/T/</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>StandardErrorPath</key>
  <string>/home/eli/.local/state/magus/broker.log</string>
</dict>
</plist>
`}}, launchd)

	_, err = renderBrokerUnits("runit", f)
	assert.Error(t, err, "a supervisor magus prints nothing for is refused, not guessed at")
}

func TestBrokerUnitsQuoteOnlyWhatNeedsIt(t *testing.T) {
	assert.Equal(t, "/usr/local/bin/magus", systemdQuote("/usr/local/bin/magus"))
	assert.Equal(t, `"/a \"b\"/c\\d"`, systemdQuote(`/a "b"/c\d`))
	assert.Equal(t, "/x%%y", systemdQuote("/x%y"))
}
