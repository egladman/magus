package std

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"

	buzz "github.com/egladman/gopherbuzz"
)

func ioModule(sess *buzz.Session) buzz.Value {
	m := mod()
	m.MapSet("FileMode", buzz.EnumDefValue("FileMode", []string{"read", "write", "update"}))
	fileDef := mod()
	fileDef.MapSet("open", fn("File.open", fileOpen))
	m.MapSet("File", fileDef)
	m.MapSet("stdin", makeFileValue(os.Stdin))
	m.MapSet("stdout", makeFileValue(os.Stdout))
	m.MapSet("stderr", makeFileValue(os.Stderr))
	m.MapSet("runFile", fn("io.runFile", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("io.runFile: requires a str path argument")
		}
		src, err := os.ReadFile(args[0].AsString())
		if err != nil {
			return buzz.Null, fmt.Errorf("io.runFile: %w", err)
		}
		return buzz.Null, sess.Exec(ctx, string(src))
	}))
	return m
}

func fileOpen(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 2 {
		return buzz.Null, fmt.Errorf("File.open: requires (str filename, FileMode mode)")
	}
	if !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("File.open: filename must be str, got %s", args[0].Kind())
	}
	if args[1].Kind() != "enum" {
		return buzz.Null, fmt.Errorf("File.open: mode must be a FileMode enum value, got %s", args[1].Kind())
	}

	filename := args[0].AsString()
	modeStr := args[1].String() // "FileMode.read" etc.

	var flags int
	switch {
	case len(modeStr) >= 4 && modeStr[len(modeStr)-4:] == "read":
		flags = os.O_RDONLY
	case len(modeStr) >= 5 && modeStr[len(modeStr)-5:] == "write":
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	default: // update
		flags = os.O_RDWR | os.O_CREATE
	}

	f, err := os.OpenFile(filename, flags, 0o644)
	if err != nil {
		return buzz.Null, fmt.Errorf("File.open: %w", err)
	}
	return makeFileValue(f), nil
}

func makeFileValue(f *os.File) buzz.Value {
	m := mod()
	// bufio.ReadWriter routes both reads and writes through the same buffered
	// layer so that interleaved read+write on an update-mode file sees a
	// consistent cursor position. Writes are flushed immediately after each call.
	rw := bufio.NewReadWriter(bufio.NewReader(f), bufio.NewWriter(f))

	m.MapSet("collect", fn("File.collect", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.Null, f.Close()
	}))
	m.MapSet("close", fn("File.close", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.Null, f.Close()
	}))
	m.MapSet("isTTY", fn("File.isTTY", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		fi, err := f.Stat()
		if err != nil {
			return buzz.False, nil
		}
		return buzz.BoolValue(fi.Mode()&os.ModeCharDevice != 0), nil
	}))
	m.MapSet("readAll", fn("File.readAll", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		var limit int64 = -1
		if len(args) >= 1 && args[0].IsInt() {
			limit = args[0].AsInt()
		}
		var data []byte
		var err error
		if limit >= 0 {
			data = make([]byte, limit)
			n, rerr := io.ReadFull(rw, data)
			data = data[:n]
			// ErrUnexpectedEOF = partial read; EOF = nothing available.
			// Both mean "return what we got" rather than propagating an error.
			if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
				err = rerr
			}
		} else {
			data, err = io.ReadAll(rw)
		}
		if err != nil {
			return buzz.Null, fmt.Errorf("File.readAll: %w", err)
		}
		return buzz.StrValue(string(data)), nil
	}))
	m.MapSet("readLine", fn("File.readLine", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		line, err := rw.ReadString('\n')
		if err != nil {
			if err == io.EOF && len(line) > 0 {
				// partial line at EOF — return it
				return buzz.StrValue(line), nil
			}
			if err == io.EOF {
				return buzz.Null, nil
			}
			return buzz.Null, fmt.Errorf("File.readLine: %w", err)
		}
		if len(line) > 0 && line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		return buzz.StrValue(line), nil
	}))
	m.MapSet("read", fn("File.read", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		n := int64(1)
		if len(args) >= 1 && args[0].IsInt() {
			n = args[0].AsInt()
		}
		if n <= 0 {
			return buzz.Null, fmt.Errorf("File.read: n must be > 0")
		}
		buf := make([]byte, n)
		count, err := rw.Read(buf)
		if err != nil {
			if err == io.EOF {
				return buzz.Null, nil
			}
			return buzz.Null, fmt.Errorf("File.read: %w", err)
		}
		return buzz.StrValue(string(buf[:count])), nil
	}))
	m.MapSet("write", fn("File.write", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("File.write: requires a str bytes argument")
		}
		if _, err := rw.WriteString(args[0].AsString()); err != nil {
			return buzz.Null, fmt.Errorf("File.write: %w", err)
		}
		if err := rw.Flush(); err != nil {
			return buzz.Null, fmt.Errorf("File.write: %w", err)
		}
		return buzz.Null, nil
	}))
	return m
}
