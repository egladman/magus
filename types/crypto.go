package types

// SignAlgorithm names the signature scheme a crypto\sign call uses.
//
// A named type with a declared case list rather than a bare string, so a magusfile
// naming an algorithm magus does not implement is a CHECKER error at load rather
// than a runtime throw halfway through a release job. The registry between here and
// Buzz is cmd/magus-utils/boundary_types.go; adding a case there and here is what
// makes the validator, the error message, and the Buzz mirror all follow.
//
// One case today. The type exists anyway because the alternative - baking the
// algorithm into the method name, as ed25519Sign / ed25519SignFile / ed25519Verify /
// ed25519Public - costs four new names per algorithm and leaves callers no way to
// pass the choice around.
type SignAlgorithm string

// SignEd25519 is the only scheme magus signs and verifies with. It is what every
// magus signature already uses: SHA256SUMS.sig, the release index, and the registry.
const SignEd25519 SignAlgorithm = "ed25519"
