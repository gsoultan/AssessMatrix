// Package secure holds the few primitives every credential in the system is
// built from: random tokens, six-digit codes, and keyed hashes.
package secure

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Keys are derived from one server secret, one per purpose, so a hash made
// for one thing can never be replayed as another.
type Keys struct {
	link, code, session, chain []byte
}

// MinSecretBytes is the least secret the core will start with.
const MinSecretBytes = 32

func NewKeys(secret []byte) (*Keys, error) {
	if len(secret) < MinSecretBytes {
		return nil, fmt.Errorf("server secret must be at least %d bytes, got %d", MinSecretBytes, len(secret))
	}
	derive := func(purpose string) []byte {
		m := hmac.New(sha256.New, secret)
		m.Write([]byte("assessmatrix/v1/" + purpose))
		return m.Sum(nil)
	}
	return &Keys{link: derive("link"), code: derive("code"), session: derive("session"), chain: derive("attempt-chain")}, nil
}

func mac(key []byte, parts ...[]byte) []byte {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(p)))
		m.Write(n[:]) // length-prefixed, so ("ab","c") never equals ("a","bc")
		m.Write(p)
	}
	return m.Sum(nil)
}

// LinkHash is what is stored for an invitation token.
func (k *Keys) LinkHash(token string) []byte { return mac(k.link, []byte(token)) }

// CodeHash binds a code to its owner, so one table of a million codes does not
// serve every invitation, and the server key stops an offline guess.
func (k *Keys) CodeHash(owner, code string) []byte { return mac(k.code, []byte(owner), []byte(code)) }

// SessionHash is what is stored for a session cookie.
func (k *Keys) SessionHash(token string) []byte { return mac(k.session, []byte(token)) }

// ChainHash links an attempt event to the one before it.
func (k *Keys) ChainHash(prev, body []byte) []byte { return mac(k.chain, prev, body) }

// Equal compares in constant time.
func Equal(a, b []byte) bool { return len(a) > 0 && hmac.Equal(a, b) }

// Token is 256 bits from the CSPRNG as 43 base64url characters.
func Token() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("secure: CSPRNG failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b[:])
}

// ValidToken reports whether s has the shape Token produces. Anything else is
// refused before it is hashed or looked up.
func ValidToken(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, r := range s {
		if !(r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// Code is six digits with no modulo bias.
func Code() string {
	const limit = (1 << 32) / 1_000_000 * 1_000_000
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic("secure: CSPRNG failed: " + err.Error())
		}
		if v := binary.BigEndian.Uint32(b[:]); v < limit {
			return fmt.Sprintf("%06d", v%1_000_000)
		}
	}
}

// MaskEmail turns budi@example.id into b•••@example.id.
func MaskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at < 0 {
		return "•••"
	}
	first, size := utf8.DecodeRuneInString(email)
	if at == 0 || first == utf8.RuneError && size <= 1 {
		return "•••" + email[at:]
	}
	return string(first) + "•••" + email[at:]
}
