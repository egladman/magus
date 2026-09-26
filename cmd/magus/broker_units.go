package main

import (
	"bytes"
	"encoding/xml"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/broker"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/service"
)

// Supervisors `magus broker units` prints for.
const (
	supervisorSystemd = "systemd"
	supervisorLaunchd = "launchd"
)

// launchdLabel names the broker's launchd agent, and its plist file.
const launchdLabel = "magus.broker"

// stopMargin is how long past shutdown_grace a supervisor waits before SIGKILL: the
// broker stops its services only once the drain ends, a teardown bounded by
// service.DefaultShutdownTimeout.
const stopMargin = service.DefaultShutdownTimeout + 20*time.Second

// brokerUnit is one file a supervisor reads to run the broker.
type brokerUnit struct {
	Supervisor string `json:"supervisor" yaml:"supervisor"`
	// Path is where that supervisor looks for the file.
	Path    string `json:"path" yaml:"path"`
	Content string `json:"content" yaml:"content"`
}

// unitFacts is what the units are rendered from, gathered once so rendering is pure.
type unitFacts struct {
	exe       string // the magus binary the supervisor runs
	socket    string // the path every run dials
	log       string // the file a launchd agent's broker logs to
	grace     time.Duration
	home      string
	configDir string
	// env pins the variables the socket path is derived from, for a supervisor that
	// starts the broker with an environment of its own.
	env [][2]string
}

// brokerUnits is `magus broker units [systemd|launchd]`: print the files a supervisor
// needs to run the broker, for the person to install. The default is launchd on macOS
// and systemd elsewhere.
func brokerUnits(args []string) error {
	rest, err := cmdParse("broker units", args, func(fs *flag.FlagSet) {
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "usage: magus broker units [systemd|launchd] [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Print the units a supervisor needs to run the broker: a systemd socket and")
			fmt.Fprintln(os.Stderr, "service that socket-activate it, or a launchd agent that keeps it alive.")
			fmt.Fprintln(os.Stderr, "magus prints them; installing them is yours.")
		}
	})
	if err != nil {
		return err
	}
	supervisor := supervisorSystemd
	if runtime.GOOS == "darwin" {
		supervisor = supervisorLaunchd
	}
	switch len(rest) {
	case 0:
	case 1:
		supervisor = rest[0]
	default:
		return usagef("magus broker units: takes at most one supervisor (got %q)", rest)
	}
	facts, err := gatherUnitFacts()
	if err != nil {
		return fmt.Errorf("magus broker units: %w", err)
	}
	units, err := renderBrokerUnits(supervisor, facts)
	if err != nil {
		return usagef("magus broker units: %v", err)
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	if opts.Format != outputText {
		return emitFormatted(opts, units)
	}
	for i, u := range units {
		if i > 0 {
			fmt.Println()
		}
		fmt.Printf("# %s\n%s", u.Path, u.Content)
	}
	fmt.Fprintln(os.Stderr, "\nmagus: write each file above to the path in its header, then:")
	switch supervisor {
	case supervisorSystemd:
		fmt.Fprintln(os.Stderr, "  systemctl --user daemon-reload && systemctl --user enable --now magus-broker.socket")
	case supervisorLaunchd:
		fmt.Fprintf(os.Stderr, "  launchctl bootstrap gui/%d %s\n", os.Getuid(), units[0].Path)
	}
	return nil
}

func gatherUnitFacts() (unitFacts, error) {
	exe, err := os.Executable()
	if err != nil {
		return unitFacts{}, err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return unitFacts{}, err
	}
	configDir, err := config.UserConfigDir()
	if err != nil {
		return unitFacts{}, err
	}
	f := unitFacts{
		exe:       exe,
		socket:    strings.TrimPrefix(broker.DefaultAddr(), "unix://"),
		log:       brokerLogPath(),
		grace:     globalCfg.ShutdownGrace,
		home:      home,
		configDir: configDir,
	}
	for _, k := range []string{"XDG_RUNTIME_DIR", "TMPDIR"} {
		if v, ok := os.LookupEnv(k); ok {
			f.env = append(f.env, [2]string{k, v})
		}
	}
	return f, nil
}

// renderBrokerUnits renders the units for supervisor.
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
func renderBrokerUnits(supervisor string, f unitFacts) ([]brokerUnit, error) {
	grace := f.grace.String()
	stopSecs := strconv.FormatInt(int64((f.grace+stopMargin+time.Second-1)/time.Second), 10)
	switch supervisor {
	case supervisorSystemd:
		dir := filepath.Join(f.configDir, "systemd", "user")
		socket := fmt.Sprintf(`[Unit]
Description=magus broker socket

[Socket]
ListenStream=%s
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`, systemdEscape(f.socket))
		svc := fmt.Sprintf(`[Unit]
Description=magus broker: this host's capacity and shared services
Requires=magus-broker.socket
After=magus-broker.socket

[Service]
ExecStart=%s broker --shutdown-grace=%s
ExecReload=kill -HUP $MAINPID
KillMode=mixed
TimeoutStopSec=%s
`, systemdQuote(f.exe), grace, stopSecs)
		return []brokerUnit{
			{Supervisor: supervisor, Path: filepath.Join(dir, "magus-broker.socket"), Content: socket},
			{Supervisor: supervisor, Path: filepath.Join(dir, "magus-broker.service"), Content: svc},
		}, nil

	case supervisorLaunchd:
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + launchdLabel + `</string>
  <key>ProgramArguments</key>
  <array>
    <string>` + xmlText(f.exe) + `</string>
    <string>broker</string>
    <string>--idle-exit</string>
    <string>0</string>
    <string>--shutdown-grace</string>
    <string>` + grace + `</string>
    <string>--log</string>
    <string>` + xmlText(f.log) + `</string>
  </array>
`)
		if len(f.env) > 0 {
			b.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
			for _, kv := range f.env {
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
  <string>` + xmlText(f.log) + `</string>
</dict>
</plist>
`)
		path := filepath.Join(f.home, "Library", "LaunchAgents", launchdLabel+".plist")
		return []brokerUnit{{Supervisor: supervisor, Path: path, Content: b.String()}}, nil

	default:
		return nil, fmt.Errorf("unknown supervisor %q (want %s or %s)", supervisor, supervisorSystemd, supervisorLaunchd)
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
