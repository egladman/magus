package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/libs/gopherbuzz/internal/stdgen"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	module, err := stdgen.Serialize("..")
	if err != nil {
		return err
	}
	if err := os.MkdirAll("gen", 0o755); err != nil {
		return err
	}
	for _, output := range []struct {
		name string
		data []byte
	}{
		{"serialize.buzz", module},
		{"serializecore.buzz", stdgen.SerializeCore()},
	} {
		if err := os.WriteFile(filepath.Join("gen", output.name), output.data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
