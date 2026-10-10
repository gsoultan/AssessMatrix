package limit

import (
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(2, time.Minute, 2)
	l.now = func() time.Time { return now }

	if !l.Allow("a") || !l.Allow("a") {
		t.Fatal("first two events for a key must pass")
	}
	if l.Allow("a") {
		t.Fatal("third event in the window must be refused")
	}
	if !l.Allow("b") {
		t.Fatal("a second key fits under capacity")
	}
	if l.Allow("c") {
		t.Fatal("a new key beyond capacity must be refused while nothing has expired")
	}
	now = now.Add(time.Minute)
	if !l.Allow("c") {
		t.Fatal("expired windows are swept to make room")
	}
	if !l.Allow("a") {
		t.Fatal("a key's window resets after the period")
	}
}
