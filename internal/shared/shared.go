package shared

import (
	"crypto/sha256"
	"encoding/hex"
)

// ShortHash is the digest node names carry, so the address or host they identify stays
// out of the name.
func ShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:4]
}
