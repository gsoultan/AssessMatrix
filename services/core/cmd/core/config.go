package main

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strconv"
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
	MetricsListen string // AM_METRICS_LISTEN: Prometheus /metrics, kept off the public port (default :9090; "off" disables)
	PublicURL     string // AM_PUBLIC_URL: origin of the studio
	RunnerURL     string // AM_RUNNER_URL: origin candidates open links on (default AM_PUBLIC_URL)
	Mailer        string // AM_MAILER: log | smtp | mailbox (mailbox needs AM_DEV=1)
	SMTP          mail.SMTP
	SecureCookies bool   // false only with AM_DEV=1
	TrustProxy    bool   // AM_TRUST_PROXY=1 behind a proxy that sets X-Forwarded-For
	StaticDir     string // AM_STATIC_DIR: serve the built web app from here
	Dev           bool   // AM_DEV=1
	// AM_WEBHOOK_ALLOW_PRIVATE=1 lets webhooks reach http and private
	// addresses, for a receiver on a developer's machine. Needs AM_DEV=1.
	WebhookAllowPrivate bool
	Media               mediaConfig
	// AM_LIMIT_PER_IP: calls a minute from one address to the candidate and
	// sign-in endpoints (default 60); raise it for an exam hall behind one NAT
	// address. AM_LIMIT_PER_TENANT: codes mailed or guessed and links reissued
	// a minute across all of one tenant's invitations (default 600).
	PerIP, PerTenant int
}

// mediaConfig picks where uploaded images go. One store serves one region;
// tenants of any other region cannot upload here.
type mediaConfig struct {
	Store    string // AM_MEDIA_STORE: fs | s3 | none (default none: uploads refused)
	Region   string // AM_MEDIA_REGION: the region the store is in (default id)
	Dir      string // AM_MEDIA_DIR, for fs
	Endpoint string // AM_S3_ENDPOINT host[:port], for s3
	Bucket   string // AM_S3_BUCKET
	Key      string // AM_S3_ACCESS_KEY
	Secret   string // AM_S3_SECRET_KEY
	Insecure bool   // AM_S3_INSECURE=1: plain http, development only
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func loadConfig(serving bool) (*config, error) {
	c := &config{
		DatabaseURL:         os.Getenv("AM_DATABASE_URL"),
		MigrateURL:          os.Getenv("AM_MIGRATE_DATABASE_URL"),
		AppRole:             env("AM_APP_ROLE", "am_app"),
		Listen:              env("AM_LISTEN", ":8080"),
		MetricsListen:       env("AM_METRICS_LISTEN", ":9090"),
		PublicURL:           strings.TrimRight(os.Getenv("AM_PUBLIC_URL"), "/"),
		RunnerURL:           strings.TrimRight(os.Getenv("AM_RUNNER_URL"), "/"),
		Mailer:              env("AM_MAILER", "log"),
		TrustProxy:          os.Getenv("AM_TRUST_PROXY") == "1",
		StaticDir:           os.Getenv("AM_STATIC_DIR"),
		Dev:                 os.Getenv("AM_DEV") == "1",
		WebhookAllowPrivate: os.Getenv("AM_WEBHOOK_ALLOW_PRIVATE") == "1",
		Media: mediaConfig{
			Store:    env("AM_MEDIA_STORE", "none"),
			Region:   env("AM_MEDIA_REGION", "id"),
			Dir:      os.Getenv("AM_MEDIA_DIR"),
			Endpoint: os.Getenv("AM_S3_ENDPOINT"),
			Bucket:   os.Getenv("AM_S3_BUCKET"),
			Key:      os.Getenv("AM_S3_ACCESS_KEY"),
			Secret:   os.Getenv("AM_S3_SECRET_KEY"),
			Insecure: os.Getenv("AM_S3_INSECURE") == "1",
		},
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
	if c.RunnerURL == "" {
		c.RunnerURL = c.PublicURL
	} else if !c.Dev && !strings.HasPrefix(c.RunnerURL, "https://") {
		errs = append(errs, errors.New("AM_RUNNER_URL must be https outside development"))
	}
	for _, l := range []struct {
		key string
		to  *int
	}{{"AM_LIMIT_PER_IP", &c.PerIP}, {"AM_LIMIT_PER_TENANT", &c.PerTenant}} {
		if v := os.Getenv(l.key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				errs = append(errs, fmt.Errorf("%s must be a whole number of calls a minute, got %q", l.key, v))
			}
			*l.to = n
		}
	}
	secret, err := decodeSecret(os.Getenv("AM_SECRET"))
	if err != nil {
		errs = append(errs, err)
	}
	c.Secret = secret
	if c.WebhookAllowPrivate && !c.Dev {
		errs = append(errs, errors.New("AM_WEBHOOK_ALLOW_PRIVATE=1 lets tenants reach internal addresses and needs AM_DEV=1"))
	}
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
	switch m := c.Media; m.Store {
	case "none":
	case "fs":
		if m.Dir == "" {
			errs = append(errs, errors.New("AM_MEDIA_DIR is required for AM_MEDIA_STORE=fs"))
		}
	case "s3":
		if m.Endpoint == "" || m.Bucket == "" || m.Key == "" || m.Secret == "" {
			errs = append(errs, errors.New("AM_S3_ENDPOINT, AM_S3_BUCKET, AM_S3_ACCESS_KEY and AM_S3_SECRET_KEY are required for AM_MEDIA_STORE=s3"))
		}
		if m.Insecure && !c.Dev {
			errs = append(errs, errors.New("AM_S3_INSECURE=1 sends images in the clear and needs AM_DEV=1"))
		}
	default:
		errs = append(errs, fmt.Errorf("unknown AM_MEDIA_STORE %q (want fs, s3 or none)", m.Store))
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
