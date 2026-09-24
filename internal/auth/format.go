package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math/big"
	"strings"

	"github.com/egladman/magus/types"
)

// Every magus token, whatever its class, has one layout (GitHub's newer token format):
//
//	<class prefix><43 base62: 256-bit crypto/rand><6 base62: CRC32 of the body>
//
// The prefix names the class, so a verifier routes a token to the one store that can hold
// it before hashing anything, and a secret scanner recognizes every class with
// `mg[oslx]_[0-9A-Za-z]{49}`. The checksum rejects a typo or a truncated paste offline. The
// body has a ~10^77 keyspace, so one fast SHA-256 at rest is the right hash: there is
// nothing to brute-force, and a slow hash would only add latency to every request.
const (
	prefixOperator = "mgo_"
	prefixStored   = "mgs_"
	prefixShare    = "mgl_"
	prefixExchange = "mgx_"

	// 62^43 just exceeds 2^256, so 43 characters hold any 256-bit body; 42 would not.
	tokenBodyLen  = 43
	tokenCheckLen = 6
	tokenLen      = len(prefixStored) + tokenBodyLen + tokenCheckLen

	base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

var classPrefixes = map[types.CredentialClass]string{
	types.ClassOperator: prefixOperator,
	types.ClassStored:   prefixStored,
	types.ClassShare:    prefixShare,
	types.ClassExchange: prefixExchange,
}

// classOf reads a token's class from its prefix and checks its length, alphabet and
// checksum. It touches no store: a string that fails here is refused before any work is done
// on it, and one that passes is only well-formed, not valid.
func classOf(token string) (types.CredentialClass, bool) {
	for class, prefix := range classPrefixes {
		if rest, ok := strings.CutPrefix(token, prefix); ok {
			return class, validBody(rest)
		}
	}
	return "", false
}

func validBody(rest string) bool {
	if len(rest) != tokenBodyLen+tokenCheckLen {
		return false
	}
	body, check := rest[:tokenBodyLen], rest[tokenBodyLen:]
	if !isBase62(body) || !isBase62(check) {
		return false
	}
	want, err := checksum(body)
	return err == nil && check == want
}

// mintSecret returns a fresh token of class.
func mintSecret(class types.CredentialClass) (string, error) {
	prefix, ok := classPrefixes[class]
	if !ok {
		return "", fmt.Errorf("auth: no token format for class %q", class)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: read random: %w", err)
	}
	body, err := base62Encode(raw, tokenBodyLen)
	if err != nil {
		return "", err
	}
	check, err := checksum(body)
	if err != nil {
		return "", err
	}
	return prefix + body + check, nil
}

func checksum(body string) (string, error) {
	sum := crc32.ChecksumIEEE([]byte(body))
	return base62Encode([]byte{byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum)}, tokenCheckLen)
}

// base62Encode encodes b as a big-endian base62 number left-padded to width characters.
func base62Encode(b []byte, width int) (string, error) {
	n := new(big.Int).SetBytes(b)
	base := big.NewInt(62)
	rem := new(big.Int)
	buf := make([]byte, width)
	for i := width - 1; i >= 0; i-- {
		n.DivMod(n, base, rem)
		buf[i] = base62Alphabet[rem.Int64()]
	}
	if n.Sign() != 0 {
		return "", fmt.Errorf("auth: base62 value overflows width %d", width)
	}
	return string(buf), nil
}

func isBase62(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(base62Alphabet, s[i]) < 0 {
			return false
		}
	}
	return true
}

// digest is the hex SHA-256 of a secret: what the store keeps in place of it.
func digest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// TokenID is a token's stable, non-reversible id: the first 8 hex of its SHA-256. It is what
// listings print and what a record names a credential by.
func TokenID(token string) string { return digest(token)[:8] }

// looksLikeID reports a string of exactly 8 lowercase hex digits, the shape of an id. A name
// may not take it, so revoking by one can never mean the other.
func looksLikeID(s string) bool {
	if len(s) != 8 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !strings.ContainsRune("0123456789abcdef", rune(s[i])) {
			return false
		}
	}
	return true
}

// validDigest reports a 64-character lowercase hex SHA-256.
func validDigest(s string) bool {
	return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == ""
}
