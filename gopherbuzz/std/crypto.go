package std

import (
	"context"
	"crypto/md5"  //nolint:gosec
	"crypto/sha1" //nolint:gosec
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"strings"

	"github.com/egladman/gopherbuzz/vm"
	"golang.org/x/crypto/sha3"
)

// cryptoModule builds the "crypto" module matching Buzz's crypto reference:
// https://buzz-lang.dev/0.5.0/reference/std/crypto.html
func cryptoModule() vm.Value {
	m := mod()
	m.MapSet("HashAlgorithm", vm.EnumDefValue("HashAlgorithm", []string{
		"Md5",
		"Sha1",
		"Sha224",
		"Sha256",
		"Sha384",
		"Sha512",
		"Sha512256",
		"Sha512T256",
		"Sha3224",
		"Sha3256",
		"Sha3384",
		"Sha3512",
	}))
	m.MapSet("hash", fn("crypto.hash", cryptoHash))
	return m
}

func cryptoHash(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 {
		return vm.Null, fmt.Errorf("crypto.hash: requires (HashAlgorithm algo, str data)")
	}
	if args[0].Kind() != "enum" {
		return vm.Null, fmt.Errorf("crypto.hash: first argument must be a HashAlgorithm enum value, got %s", args[0].Kind())
	}
	if !args[1].IsStr() {
		return vm.Null, fmt.Errorf("crypto.hash: second argument must be str, got %s", args[1].Kind())
	}

	algoFull := args[0].String() // "HashAlgorithm.Md5" etc. — String() on enum returns "EnumName.CaseName"
	algoCase := algoFull
	if idx := strings.LastIndex(algoFull, "."); idx >= 0 {
		algoCase = algoFull[idx+1:]
	}

	data := []byte(args[1].AsString())
	var h hash.Hash
	switch algoCase {
	case "Md5":
		h = md5.New() //nolint:gosec
	case "Sha1":
		h = sha1.New() //nolint:gosec
	case "Sha224":
		h = sha256.New224()
	case "Sha256":
		h = sha256.New()
	case "Sha384":
		h = sha512.New384()
	case "Sha512":
		h = sha512.New()
	case "Sha512256":
		h = sha512.New512_256()
	case "Sha512T256":
		h = sha512.New512_256() // SHA-512/256
	case "Sha3224":
		h = sha3.New224()
	case "Sha3256":
		h = sha3.New256()
	case "Sha3384":
		h = sha3.New384()
	case "Sha3512":
		h = sha3.New512()
	default:
		return vm.Null, fmt.Errorf("crypto.hash: unknown HashAlgorithm case %q", algoCase)
	}
	h.Write(data)
	return vm.StrValue(hex.EncodeToString(h.Sum(nil))), nil
}
