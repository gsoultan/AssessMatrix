package main

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
)

// config is read from the environment once at startup. A missing or unsafe
// value stops the process rather than starting it half-configured.
type config struct {
	DatabaseURL   string // AM_DATABASE_URL: the unprivileged application role
	MigrateURL    string // AM_MIGRATE_DATABASE_URL: the schema owner
	AppRole       string // AM_APP_ROLE: granted access by migrate (default am_app)
	Secret        []byte // AM_SECRET: hex or base64, at least 32 bytes
	Listen        string // AM_LISTEN (default :8080)
	PublicURL     string // AM_PUBLIC_URL: origin candidates open links on
	Mailer        string // AM_MAILER: log | smtp | mailbox (mailbox needs AM_DEV=1)
	SMTP          mail.SMTP
	SecureCookies bool   // false only with AM_DEV=1
	TrustProxy    bool   // AM_TRUST_PROXY=1 behind a proxy that sets X-Forwarded-For
	StaticDir     string // AM_STATIC_DIR: serve the built web app from here
	Dev           bool   // AM_DEV=1
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig(serving bool) (*config, error) {
	c := &config{
		DatabaseURL: os.Getenv("AM_DATABASE_URL"),
		MigrateURL:  os.Getenv("AM_MIGRATE_DATABASE_URL"),
		AppRole:     env("AM_APP_ROLE", "am_app"),
		Listen:      env("AM_LISTEN", ":8080"),
		PublicURL:   strings.TrimRight(os.Getenv("AM_PUBLIC_URL"), "/"),
		Mailer:      env("AM_MAILER", "log"),
		TrustProxy:  os.Getenv("AM_TRUST_PROXY") == "1",
		StaticDir:   os.Getenv("AM_STATIC_DIR"),
		Dev:         os.Getenv("AM_DEV") == "1",
	}
	c.SecureCookies = !c.Dev
	if !serving {
		if c.MigrateURL == "" {
			return nil, errors.New("AM_MIGRATE_DATABASE_URL is required")
		}
		return c, nil
	}
	var errs []error
	if c.DatabaseURL == "" {
		errs = append(errs, errors.New("AM_DATABASE_URL is required"))
	}
	if c.PublicURL == "" {
		errs = append(errs, errors.New("AM_PUBLIC_URL is required (the origin invitation links point at)"))
	} else if !c.Dev && !strings.HasPrefix(c.PublicURL, "https://") {
		errs = append(errs, errors.New("AM_PUBLIC_URL must be https outside development"))
	}
	secret, err := decodeSecret(os.Getenv("AM_SECRET"))
	if err != nil {
		errs = append(errs, err)
	}
	c.Secret = secret
	switch c.Mailer {
	case "log":
	case "mailbox":
		if !c.Dev {
			errs = append(errs, errors.New("AM_MAILER=mailbox exposes mail over HTTP and needs AM_DEV=1"))
		}
	case "smtp":
		c.SMTP = mail.SMTP{Addr: os.Getenv("AM_SMTP_ADDR"), From: os.Getenv("AM_SMTP_FROM"),
			User: os.Getenv("AM_SMTP_USER"), Pass: os.Getenv("AM_SMTP_PASS")}
		if c.SMTP.Addr == "" || c.SMTP.From == "" {
			errs = append(errs, errors.New("AM_SMTP_ADDR and AM_SMTP_FROM are required for AM_MAILER=smtp"))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown AM_MAILER %q", c.Mailer))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return c, nil
}

func decodeSecret(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("AM_SECRET is required: 32+ random bytes, hex or base64 (openssl rand -hex 32)")
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b, nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return nil, errors.New("AM_SECRET must be hex or base64")
}
