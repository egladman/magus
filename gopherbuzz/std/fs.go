package std

import (
	"context"
	"fmt"
	"os"

	"github.com/egladman/gopherbuzz/vm"
)

// fsModule builds the "fs" module matching Buzz's fs reference:
// https://buzz-lang.dev/0.5.0/reference/std/fs.html
func fsModule() vm.Value {
	m := mod()
	m.MapSet("currentDirectory", fn("fs.currentDirectory", fsCurrentDirectory))
	m.MapSet("makeDirectory", fn("fs.makeDirectory", fsMakeDirectory))
	m.MapSet("delete", fn("fs.delete", fsDelete))
	m.MapSet("move", fn("fs.move", fsMove))
	m.MapSet("list", fn("fs.list", fsList))
	m.MapSet("exists", fn("fs.exists", fsExists))
	m.MapSet("modified", fn("fs.modified", fsModified))
	return m
}

func fsCurrentDirectory(ctx context.Context, _ []vm.Value) (vm.Value, error) {
	// The embedder-set cwd is the script's effective working directory; fall back
	// to the process cwd when none is set.
	if cwd := cwdFromContext(ctx); cwd != "" {
		return vm.StrValue(cwd), nil
	}
	dir, err := os.Getwd()
	if err != nil {
		return vm.Null, fmt.Errorf("fs.currentDirectory: %w", err)
	}
	return vm.StrValue(dir), nil
}

func fsMakeDirectory(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("fs.makeDirectory: requires a str path argument")
	}
	if err := os.MkdirAll(resolve(ctx, args[0].AsString()), 0o755); err != nil {
		return vm.Null, fmt.Errorf("fs.makeDirectory: %w", err)
	}
	return vm.Null, nil
}

func fsDelete(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("fs.delete: requires a str path argument")
	}
	if err := os.RemoveAll(resolve(ctx, args[0].AsString())); err != nil {
		return vm.Null, fmt.Errorf("fs.delete: %w", err)
	}
	return vm.Null, nil
}

func fsMove(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 || !args[0].IsStr() || !args[1].IsStr() {
		return vm.Null, fmt.Errorf("fs.move: requires source and destination str arguments")
	}
	if err := os.Rename(resolve(ctx, args[0].AsString()), resolve(ctx, args[1].AsString())); err != nil {
		return vm.Null, fmt.Errorf("fs.move: %w", err)
	}
	return vm.Null, nil
}

func fsList(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("fs.list: requires a str path argument")
	}
	entries, err := os.ReadDir(resolve(ctx, args[0].AsString()))
	if err != nil {
		return vm.Null, fmt.Errorf("fs.list: %w", err)
	}
	items := make([]vm.Value, len(entries))
	for i, e := range entries {
		items[i] = vm.StrValue(e.Name())
	}
	return vm.ListValue(items), nil
}

func fsExists(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("fs.exists: requires a str path argument")
	}
	_, err := os.Stat(resolve(ctx, args[0].AsString()))
	if err == nil {
		return vm.True, nil
	}
	if os.IsNotExist(err) {
		return vm.False, nil
	}
	return vm.Null, fmt.Errorf("fs.exists: %w", err)
}

// fsModified returns the file's modification time in milliseconds since the
// Unix epoch, or null when the path cannot be stat'ed (missing file included).
// Null-on-absence rather than an error makes it directly usable as a change
// poller: watch for the value to move, including through create and delete.
func fsModified(ctx context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("fs.modified: requires a str path argument")
	}
	info, err := os.Stat(resolve(ctx, args[0].AsString()))
	if err != nil {
		return vm.Null, nil
	}
	return vm.FloatValue(float64(info.ModTime().UnixMilli())), nil
}
