package broker

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/service"
)

// Supervisors RenderUnits prints for.
const (
	SupervisorSystemd = "systemd"
	SupervisorLaunchd = "launchd"
)

// launchdLabel names the broker's launchd agent, and its plist file.
const launchdLabel = "magus.broker"

// stopMargin is how long past shutdown_grace a supervisor waits before SIGKILL: the
// broker stops its services only once the drain ends, a teardown bounded by
// service.DefaultShutdownTimeout.
const stopMargin = service.DefaultShutdownTimeout + 20*time.Second

// Unit is one file a supervisor reads to run the broker.
type Unit struct {
	Supervisor string `json:"supervisor" yaml:"supervisor"`
	// Path is where that supervisor looks for the file.
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content" yaml:"content"`
}

// UnitFacts is what the units are rendered from, gathered once so rendering is pure.
type UnitFacts struct {
	Exe       string // the magus binary the supervisor runs
	Socket    string // the path every run dials
	Log       string // the file a launchd agent's broker logs to
	Grace     time.Duration
	Home      string
	ConfigDir string
	// Env pins the variables the socket path is derived from, for a supervisor that
	// starts the broker with an environment of its own.
	Env [][2]string
}

// RenderUnits renders the units for supervisor, one of the Supervisor constants.
//
// Under systemd the socket unit owns broker.sock and starts the broker on the first
// connection; the broker takes the socket over (LISTEN_FDS) and still exits when idle,
// since the next connection starts it again.
//
// launchd hands a socket over only through launch_activate_socket, a C call magus does
// not make, so its agent starts the broker at login, keeps it alive and turns idle exit
// off; the broker binds broker.sock itself. The agent pins the variables the socket
// path comes from, because launchd starts agents with an environment of its own.
//
// Both pin shutdown_grace on the command line and give the supervisor that long plus
// stopMargin between SIGTERM and SIGKILL, so the supervisor waits out the drain the
// broker actually runs, whatever config it would otherwise read. KillMode=mixed sends
// systemd's SIGTERM to the broker alone: the services it hosts share its cgroup, and
// the runs it is draining still use them.
//
// The launchd broker logs through --log so SIGHUP's reopen works under a rotator;
// StandardErrorPath names the same file for what is written before the broker opens
// it. Under systemd stderr goes to the journal, which needs no reopen.
func RenderUnits(supervisor string, f UnitFacts) ([]Unit, error) {
	grace := f.Grace.String()
	stopSecs := strconv.FormatInt(int64((f.Grace+stopMargin+time.Second-1)/time.Second), 10)
	switch supervisor {
	case SupervisorSystemd:
		dir := filepath.Join(f.ConfigDir, "systemd", "user")
		socket := fmt.Sprintf(`[Unit]
Description=magus broker socket

[Socket]
ListenStream=%s
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`, systemdEscape(f.Socket))
		svc := fmt.Sprintf(`[Unit]
Description=magus broker: this host's capacity and shared services
Requires=magus-broker.socket
After=magus-broker.socket

[Service]
ExecStart=%s broker --shutdown-grace=%s
ExecReload=kill -HUP $MAINPID
KillMode=mixed
TimeoutStopSec=%s
`, systemdQuote(f.Exe), grace, stopSecs)
		return []Unit{
			{Supervisor: supervisor, Path: filepath.Join(dir, "magus-broker.socket"), Content: socket},
			{Supervisor: supervisor, Path: filepath.Join(dir, "magus-broker.service"), Content: svc},
		}, nil

	case SupervisorLaunchd:
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + launchdLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlText(f.Exe) + `</string>
    <string>broker</string>
    <string>--idle-exit</string>
    <string>0</string>
    <string>--shutdown-grace</string>
    <string>` + grace + `</string>
    <string>--log</string>
    <string>` + xmlText(f.Log) + `</string>
  </array>
`)
		if len(f.Env) > 0 {
			b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
			for _, kv := range f.Env {
				b.WriteString("    <key>" + xmlText(kv[0]) + "</key>\n    <string>" + xmlText(kv[1]) + "</string>\n")
			}
			b.WriteString("  </dict>\n")
		}
		b.WriteString(`  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Background</string>
  <key>ExitTimeOut</key>
  <integer>` + stopSecs + `</integer>
  <key>StandardErrorPath</key>
  <string>` + xmlText(f.Log) + `</string>
</dict>
</plist>
`)
		path := filepath.Join(f.Home, "Library", "LaunchAgents", launchdLabel+".plist")
		return []Unit{{Supervisor: supervisor, Path: path, Content: b.String()}}, nil

	default:
		return nil, fmt.Errorf("unknown supervisor %q (want %s or %s)", supervisor, SupervisorSystemd, SupervisorLaunchd)
	}
}

// systemdEscape escapes the specifier character, which systemd expands in every value.
func systemdEscape(s string) string { return strings.ReplaceAll(s, "%", "%%") }

// systemdQuote makes s one word of an ExecStart line.
func systemdQuote(s string) string {
	s = systemdEscape(s)
	if !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func xmlText(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
