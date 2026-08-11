// Package hex is the "encoding/hex" host module. See std/encoding/register.go
// for why base64's nine siblings each get their own leaf package instead of
// living in std's flat root, and how this directory's Module reaches the rest
// of magus without std importing back down to collect it.
package hex

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/egladman/magus/std"
)

//go:generate go run ../../../cmd/magus-utils bindings -module hex -lang buzz -out ../../../internal/interp/bindings/gen/hex.go

// Module is the "encoding/hex" host module. Buzz strings carry a `.hex()`
// method already; this is the decode half, which the language has no form of,
// plus the encode half so a caller reaches both through one import rather than
// switching between a string method and a module.
var Module = std.Module{
	Name: "hex",
	WASM: true,
	Path: "encoding/hex",
	Doc:  "Hex text codec.",
	Methods: []std.Method{
		{
			Name:    "encode",
			Doc:     "Encode data as lowercase hex.",
			Args:    []std.Arg{{Name: "data", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Impl:    HexEncode,
		},
		{
			Name:    "decode",
			Doc:     "Decode a hex string; errors on malformed input.",
			Args:    []std.Arg{{Name: "s", Type: std.TypeString}},
			Returns: []std.Ret{{Type: std.TypeString}},
			Raises:  true,
			Impl:    HexDecode,
		},
	},
}

// HexEncode encodes data as lowercase hex.
func HexEncode(_ context.Context, data string) (string, error) {
	return hex.EncodeToString([]byte(data)), nil
}

// HexDecode decodes a hex string.
func HexDecode(_ context.Context, s string) (string, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return "", fmt.Errorf("hex.decode: %w", err)
	}
	return string(b), nil
}
