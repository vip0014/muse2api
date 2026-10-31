package hatch

import (
	"crypto/sha256"
	"encoding/hex"
)

// identityCookies decide which Muse account a gateway connection belongs to.
// hatch_vml rotates during bootstrap and must not force a reconnect; the
// session cookie and device binding identify the account itself.
var identityCookies = []string{"hatch_sess", "hatch_native_auth_device"}

// credentialFingerprint returns a stable digest for the account identity in
// the given cookie set.
func credentialFingerprint(cookies map[string]string) string {
	hash := sha256.New()
	for _, name := range identityCookies {
		hash.Write([]byte(name))
		hash.Write([]byte{0})
		hash.Write([]byte(cookies[name]))
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}
