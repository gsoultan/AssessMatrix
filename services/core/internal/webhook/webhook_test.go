package webhook

import (
	"testing"
	"time"
)

func TestVerifyRejectsTamperingAndReplay(t *testing.T) {
	secret, body, now := "whsec_test", []byte(`{"id":"e1","type":"attempt.submitted"}`), time.Unix(1_800_000_000, 0)
	sig := Sign(secret, now, body)
	if !Verify(secret, sig, body, now.Add(time.Minute), 5*time.Minute) {
		t.Fatal("a fresh, untouched delivery must verify")
	}
	for name, ok := range map[string]bool{
		"other body":   Verify(secret, sig, []byte(`{"id":"e1","type":"attempt.expired"}`), now, 5*time.Minute),
		"other secret": Verify("whsec_other", sig, body, now, 5*time.Minute),
		"replayed":     Verify(secret, sig, body, now.Add(10*time.Minute), 5*time.Minute),
		"no header":    Verify(secret, "", body, now, 5*time.Minute),
		"bad header":   Verify(secret, "t=x,v1=00", body, now, 5*time.Minute),
	} {
		if ok {
			t.Errorf("%s: verified", name)
		}
	}
}

func TestCheckURL(t *testing.T) {
	strict, dev := Policy{}, Policy{AllowPrivate: true}
	for _, u := range []string{"https://hooks.example.com/am", "https://hooks.example.com:8443/a?b=c", "https://8.8.8.8/x"} {
		if err := strict.CheckURL(u); err != nil {
			t.Errorf("%q refused: %v", u, err)
		}
	}
	for _, u := range []string{"http://hooks.example.com/am", "https://127.0.0.1/", "https://x.localhost/", "https://[fe80::1]/",
		"https://a@hooks.example.com/", "https://hooks.example.com/#x", "mailto:a@b.c", "/relative"} {
		if strict.CheckURL(u) == nil {
			t.Errorf("%q accepted", u)
		}
	}
	if err := dev.CheckURL("http://127.0.0.1:9000/hook"); err != nil {
		t.Errorf("development should reach a local receiver: %v", err)
	}
}
