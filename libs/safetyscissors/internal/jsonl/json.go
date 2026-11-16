package jsonl

import (
	"bytes"
	"encoding/json"
)

// Marshal encodes v as one line of JSON. Unlike encoding/json's Marshal it leaves <, >
// and & unescaped, the bytes magus's own encoding/json/v2 codec writes, so a line reads
// the same whichever side wrote it.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), nil
}

// Unmarshal decodes data into v.
func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
