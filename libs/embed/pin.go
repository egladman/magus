package embed

import (
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// The table itself is an OCI artifact pulled on first use; only its digest ships in
// the binary.
//
//go:embed gen/table.digest
var tableDigest string

// Pin returns the hex sha256 of the table this module pins, the digest its OCI
// artifact's layer and tag carry. It errors when gen/table.digest is empty or is not
// one line of 64 lowercase hex characters; `magus run table libs/embed` rewrites it.
func Pin() (string, error) {
	return parsePin(tableDigest)
}

func parsePin(text string) (string, error) {
	if text == "" {
		return "", errors.New("gen/table.digest is empty: run `magus run table libs/embed`")
	}
	digest, ok := strings.CutSuffix(text, "\n")
	if _, err := hex.DecodeString(digest); !ok || err != nil || len(digest) != 64 || strings.ToLower(digest) != digest {
		return "", fmt.Errorf("gen/table.digest holds %q, want one line of 64 lowercase hex characters", text)
	}
	return digest, nil
}
