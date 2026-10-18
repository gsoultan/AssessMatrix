// Package secure holds the few primitives every credential in the system is
// built from: random tokens, six-digit codes, and keyed hashes.
package secure

import (
	"crypto/aes"
	"crypto/cipher"
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
	link, code, session, chain, vault, setup []byte
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
	return &Keys{link: derive("link"), code: derive("code"), session: derive("session"), chain: derive("attempt-chain"),
		vault: derive("vault"), setup: derive("setup")}, nil
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

// setupAlphabet leaves out 0/O and 1/I/L, so a code read off a log is typed right.
const setupAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// SetupCode opens first-run setup: 12 characters (59 bits) as XXXX-XXXX-XXXX.
// It is derived from the server secret, so every replica and the
// `core setup-code` command agree on it, and it is worthless once setup is
// done, because the database then refuses a second first organisation.
func (k *Keys) SetupCode() string {
	sum := mac(k.setup, []byte("first-run"))
	v := binary.BigEndian.Uint64(sum[:8])
	var b strings.Builder
	for i := 0; i < 12; i++ {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		b.WriteByte(setupAlphabet[v%uint64(len(setupAlphabet))])
		v /= uint64(len(setupAlphabet))
	}
	return b.String()
}

// SetupCodeMatches compares a typed code in constant time, ignoring case,
// spaces and dashes.
func (k *Keys) SetupCodeMatches(typed string) bool {
	norm := func(s string) []byte {
		var out []byte
		for _, r := range strings.ToUpper(s) {
			if r != '-' && r != ' ' {
				out = append(out, string(r)...)
			}
		}
		return out
	}
	return Equal(norm(typed), norm(k.SetupCode()))
}

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

// Seal encrypts a secret kept in the database (an issuer's signing key) with
// AES-256-GCM under a key derived from the server secret. The label is bound
// in as associated data, so a sealed value cannot be moved to another row.
func (k *Keys) Seal(label string, plain []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, []byte(label)), nil
}

// Open reverses Seal.
func (k *Keys) Open(label string, sealed []byte) ([]byte, error) {
	gcm, err := k.gcm()
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("sealed value too short")
	}
	return gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], []byte(label))
}

func (k *Keys) gcm() (cipher.AEAD, error) {
	b, err := aes.NewCipher(k.vault)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}
