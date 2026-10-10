package secure

import (
	"bytes"
	"strings"
	"testing"
)

func TestTokenShape(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		tok := Token()
		if !ValidToken(tok) {
			t.Fatalf("Token produced %q, which ValidToken refuses", tok)
		}
		if seen[tok] {
			t.Fatal("duplicate token")
		}
		seen[tok] = true
	}
	for _, bad := range []string{"", "x", strings.Repeat("A", 42), strings.Repeat("A", 44), strings.Repeat("=", 43)} {
		if ValidToken(bad) {
			t.Errorf("ValidToken accepted %q", bad)
		}
	}
}

func TestCodeIsSixDigits(t *testing.T) {
	for range 1000 {
		c := Code()
		if len(c) != 6 || strings.Trim(c, "0123456789") != "" {
			t.Fatalf("bad code %q", c)
		}
	}
}

func TestKeysArePurposeBound(t *testing.T) {
	if _, err := NewKeys([]byte("short")); err == nil {
		t.Fatal("a short secret must be refused")
	}
	k, _ := NewKeys(bytes.Repeat([]byte{1}, 32))
	k2, _ := NewKeys(bytes.Repeat([]byte{2}, 32))
	if bytes.Equal(k.LinkHash("t"), k.SessionHash("t")) {
		t.Fatal("a link hash must not equal a session hash of the same value")
	}
	if bytes.Equal(k.LinkHash("t"), k2.LinkHash("t")) {
		t.Fatal("hashes must depend on the server secret")
	}
	if bytes.Equal(k.CodeHash("a1", "23456"), k.CodeHash("a12", "3456")) {
		t.Fatal("hash inputs must be length-prefixed")
	}
}

func TestMaskEmail(t *testing.T) {
	for in, want := range map[string]string{
		"budi@example.id": "b•••@example.id",
		"élise@x.fr":      "é•••@x.fr",
		"@x":              "•••@x",
		"nope":            "•••",
	} {
		if got := MaskEmail(in); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", in, got, want)
		}
	}
}
