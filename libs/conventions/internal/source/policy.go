package source

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// PolicyKey decodes one top-level key of the YAML file at path into into,
// strictly, so a misspelled field fails. The other keys belong to other
// analyzers and are not read. A missing file or key leaves into untouched: the
// strict default.
func PolicyKey(path, key string, into any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var doc map[string]yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	node, ok := doc[key]
	if !ok {
		return nil
	}
	raw, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	return nil
}
