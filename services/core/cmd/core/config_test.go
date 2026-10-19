package main

import (
	"strings"
	"testing"
)

func TestRateLimitsAreConfigurable(t *testing.T) {
	t.Setenv("AM_DATABASE_URL", "postgres://x")
	t.Setenv("AM_PUBLIC_URL", "https://exam.test")
	t.Setenv("AM_SECRET", strings.Repeat("ab", 32))
	t.Setenv("AM_LIMIT_PER_IP", "600")
	c, err := loadConfig(true)
	if err != nil {
		t.Fatal(err)
	}
	if c.PerIP != 600 || c.PerTenant != 0 {
		t.Fatalf("per IP %d, per tenant %d", c.PerIP, c.PerTenant)
	}
	for _, bad := range []string{"0", "-5", "lots"} {
		t.Setenv("AM_LIMIT_PER_TENANT", bad)
		if _, err := loadConfig(true); err == nil {
			t.Fatalf("AM_LIMIT_PER_TENANT=%q must stop the server", bad)
		}
	}
}
