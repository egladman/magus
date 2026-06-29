package std

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/egladman/gopherbuzz/vm"
)

// osModule builds the "os" module matching Buzz's os reference:
// https://buzz-lang.dev/0.5.0/reference/std/os.html
func osModule() vm.Value {
	m := mod()
	m.MapSet("sleep", fn("os.sleep", osSleep))
	m.MapSet("time", fn("os.time", osTime))
	m.MapSet("env", fn("os.env", osEnv))
	m.MapSet("tmpDir", fn("os.tmpDir", osTmpDir))
	m.MapSet("tmpFilename", fn("os.tmpFilename", osTmpFilename))
	m.MapSet("exit", fn("os.exit", osExit))
	m.MapSet("execute", fn("os.execute", osExecute))

	m.MapSet("SocketProtocol", vm.EnumDefValue("SocketProtocol", []string{"tcp", "udp", "ipc"}))

	socketDef := mod()
	socketDef.MapSet("connect", fn("Socket.connect", socketConnect))
	m.MapSet("Socket", socketDef)

	serverDef := mod()
	serverDef.MapSet("init", fn("TcpServer.init", tcpServerInit))
	m.MapSet("TcpServer", serverDef)

	return m
}

func osSleep(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 {
		return vm.Null, fmt.Errorf("os.sleep: requires a double argument (milliseconds)")
	}
	var ms float64
	switch {
	case args[0].IsFloat():
		ms = args[0].AsFloat()
	case args[0].IsInt():
		ms = float64(args[0].AsInt())
	default:
		return vm.Null, fmt.Errorf("os.sleep: argument must be double, got %s", args[0].Kind())
	}
	if ms > 0 {
		time.Sleep(time.Duration(ms * float64(time.Millisecond)))
	}
	return vm.Null, nil
}

func osTime(_ context.Context, _ []vm.Value) (vm.Value, error) {
	return vm.FloatValue(float64(time.Now().UnixMilli())), nil
}

func osEnv(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("os.env: requires a str key argument")
	}
	v, ok := os.LookupEnv(args[0].AsString())
	if !ok {
		return vm.Null, nil
	}
	return vm.StrValue(v), nil
}

func osTmpDir(_ context.Context, _ []vm.Value) (vm.Value, error) {
	return vm.StrValue(os.TempDir()), nil
}

func osTmpFilename(_ context.Context, args []vm.Value) (vm.Value, error) {
	prefix := "buzz"
	if len(args) >= 1 && args[0].IsStr() {
		prefix = args[0].AsString()
	}
	f, err := os.CreateTemp("", prefix+"*")
	if err != nil {
		return vm.Null, fmt.Errorf("os.tmpFilename: %w", err)
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name) // return the name only, don't leave the file
	return vm.StrValue(name), nil
}

func osExit(_ context.Context, args []vm.Value) (vm.Value, error) {
	code := 0
	if len(args) >= 1 && args[0].IsInt() {
		code = int(args[0].AsInt())
	}
	os.Exit(code)
	return vm.Null, nil // unreachable
}

// Buzz signature: fun execute([str] command) > int !> FileSystemError, UnexpectedError
func osExecute(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsList() {
		return vm.Null, fmt.Errorf("os.execute: requires a [str] command argument")
	}
	items := args[0].ListItems()
	if len(items) == 0 {
		return vm.Null, fmt.Errorf("os.execute: command list is empty")
	}
	argv := make([]string, len(items))
	for i, it := range items {
		if !it.IsStr() {
			return vm.Null, fmt.Errorf("os.execute: command argument %d must be str, got %s", i, it.Kind())
		}
		argv[i] = it.AsString()
	}
	//nolint:gosec -- argv is supplied by the Buzz script; the caller controls what it executes
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = cwdFromContext(ctx) // run in the embedder-set cwd ("" inherits the process cwd)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return vm.IntValue(int64(ee.ExitCode())), nil
		}
		return vm.Null, fmt.Errorf("os.execute: %w", err)
	}
	return vm.IntValue(0), nil
}

// For ipc the host is treated as the Unix socket path; port is ignored.
// Buzz signature: static fun connect(SocketProtocol protocol, str host, int port) > Socket
func socketConnect(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 3 {
		return vm.Null, fmt.Errorf("Socket.connect: requires (SocketProtocol, str host, int port)")
	}
	if args[0].Kind() != "enum" {
		return vm.Null, fmt.Errorf("Socket.connect: first argument must be a SocketProtocol enum value")
	}
	if !args[1].IsStr() {
		return vm.Null, fmt.Errorf("Socket.connect: host must be str")
	}
	if !args[2].IsInt() {
		return vm.Null, fmt.Errorf("Socket.connect: port must be int")
	}

	protoFull := args[0].String()
	proto := protoFull[strings.LastIndex(protoFull, ".")+1:]
	host := args[1].AsString()
	port := args[2].AsInt()

	if port < 0 || port > 65535 {
		return vm.Null, fmt.Errorf("Socket.connect: port %d out of range [0, 65535]", port)
	}

	var network, address string
	switch proto {
	case "ipc":
		network = "unix"
		address = host // host = socket file path for IPC
	case "udp":
		network = "udp"
		address = fmt.Sprintf("%s:%d", host, port)
	default: // tcp
		network = "tcp"
		address = fmt.Sprintf("%s:%d", host, port)
	}

	conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
	if err != nil {
		return vm.Null, fmt.Errorf("Socket.connect: %w", err)
	}
	return makeSocketValue(conn), nil
}

func makeSocketValue(conn net.Conn) vm.Value {
	m := mod()

	m.MapSet("send", fn("Socket.send", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsList() {
			return vm.Null, fmt.Errorf("Socket.send: requires a [int] bytes argument")
		}
		items := args[0].ListItems()
		data := make([]byte, len(items))
		for i, item := range items {
			if !item.IsInt() {
				return vm.Null, fmt.Errorf("Socket.send: list must contain int values")
			}
			data[i] = byte(item.AsInt())
		}
		_, err := conn.Write(data)
		return vm.Null, err
	}))

	m.MapSet("receive", fn("Socket.receive", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		n := 1024
		if len(args) >= 1 && args[0].IsInt() {
			n = int(args[0].AsInt())
		}
		buf := make([]byte, n)
		count, err := conn.Read(buf)
		if err != nil {
			return vm.Null, fmt.Errorf("Socket.receive: %w", err)
		}
		items := make([]vm.Value, count)
		for i, b := range buf[:count] {
			items[i] = vm.IntValue(int64(b))
		}
		return vm.ListValue(items), nil
	}))

	m.MapSet("close", fn("Socket.close", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.Null, conn.Close()
	}))

	m.MapSet("collect", fn("Socket.collect", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.Null, conn.Close()
	}))

	return m
}

// Buzz signature: static fun init(int port, SocketProtocol protocol) > TcpServer
func tcpServerInit(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 {
		return vm.Null, fmt.Errorf("TcpServer.init: requires (int port, SocketProtocol protocol)")
	}
	if !args[0].IsInt() {
		return vm.Null, fmt.Errorf("TcpServer.init: port must be int")
	}
	if args[1].Kind() != "enum" {
		return vm.Null, fmt.Errorf("TcpServer.init: second argument must be a SocketProtocol enum value")
	}

	port := args[0].AsInt()
	if port < 0 || port > 65535 {
		return vm.Null, fmt.Errorf("TcpServer.init: port %d out of range [0, 65535]", port)
	}

	protoFull := args[1].String()
	proto := protoFull[strings.LastIndex(protoFull, ".")+1:]

	network := "tcp"
	if proto == "udp" {
		network = "udp"
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, network, fmt.Sprintf(":%d", port))
	if err != nil {
		return vm.Null, fmt.Errorf("TcpServer.init: %w", err)
	}
	return makeTCPServerValue(ln), nil
}

func makeTCPServerValue(ln net.Listener) vm.Value {
	m := mod()

	m.MapSet("accept", fn("TcpServer.accept", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		conn, err := ln.Accept()
		if err != nil {
			return vm.Null, fmt.Errorf("TcpServer.accept: %w", err)
		}
		return makeSocketValue(conn), nil
	}))

	m.MapSet("close", fn("TcpServer.close", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.Null, ln.Close()
	}))

	m.MapSet("collect", fn("TcpServer.collect", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.Null, ln.Close()
	}))

	return m
}
