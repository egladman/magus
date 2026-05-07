package std

import (
	"context"
	"fmt"
	"os"

	buzz "github.com/egladman/gopherbuzz"
)

// fsModule builds the "fs" module matching Buzz's fs reference:
// https://buzz-lang.dev/0.5.0/reference/std/fs.html
func fsModule() buzz.Value {
	m := mod()
	m.MapSet("currentDirectory", fn("fs.currentDirectory", fsCurrentDirectory))
	m.MapSet("makeDirectory", fn("fs.makeDirectory", fsMakeDirectory))
	m.MapSet("delete", fn("fs.delete", fsDelete))
	m.MapSet("move", fn("fs.move", fsMove))
	m.MapSet("list", fn("fs.list", fsList))
	m.MapSet("exists", fn("fs.exists", fsExists))
	return m
}

func fsCurrentDirectory(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
	dir, err := os.Getwd()
	if err != nil {
		return buzz.Null, fmt.Errorf("fs.currentDirectory: %w", err)
	}
	return buzz.StrValue(dir), nil
}

func fsMakeDirectory(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("fs.makeDirectory: requires a str path argument")
	}
	if err := os.MkdirAll(args[0].AsString(), 0o755); err != nil {
		return buzz.Null, fmt.Errorf("fs.makeDirectory: %w", err)
	}
	return buzz.Null, nil
}

func fsDelete(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("fs.delete: requires a str path argument")
	}
	if err := os.RemoveAll(args[0].AsString()); err != nil {
		return buzz.Null, fmt.Errorf("fs.delete: %w", err)
	}
	return buzz.Null, nil
}

func fsMove(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 2 || !args[0].IsStr() || !args[1].IsStr() {
		return buzz.Null, fmt.Errorf("fs.move: requires source and destination str arguments")
	}
	if err := os.Rename(args[0].AsString(), args[1].AsString()); err != nil {
		return buzz.Null, fmt.Errorf("fs.move: %w", err)
	}
	return buzz.Null, nil
}

func fsList(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("fs.list: requires a str path argument")
	}
	entries, err := os.ReadDir(args[0].AsString())
	if err != nil {
		return buzz.Null, fmt.Errorf("fs.list: %w", err)
	}
	items := make([]buzz.Value, len(entries))
	for i, e := range entries {
		items[i] = buzz.StrValue(e.Name())
	}
	return buzz.ListValue(items), nil
}

func fsExists(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("fs.exists: requires a str path argument")
	}
	_, err := os.Stat(args[0].AsString())
	if err == nil {
		return buzz.True, nil
	}
	if os.IsNotExist(err) {
		return buzz.False, nil
	}
	return buzz.Null, fmt.Errorf("fs.exists: %w", err)
}
