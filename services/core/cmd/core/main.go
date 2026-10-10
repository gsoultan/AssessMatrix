// Command core is the AssessMatrix API server.
//
//	core migrate                       apply schema migrations as the owner
//	core serve                         run the API and the mail worker
//	core tenant <name> <staff-email>   create a tenant and its first staff member
//
// Configuration is environment only; see config.go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/gsoultan/assessmatrix/services/core/internal/api"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "migrate":
		err = migrate(ctx, log)
	case "serve":
		err = serve(ctx, log)
	case "tenant":
		err = createTenant(ctx, log, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q (want migrate, serve or tenant)", cmd)
	}
	if err != nil {
		log.Error("exit", "cmd", cmd, "err", err)
		os.Exit(1)
	}
}

func migrate(ctx context.Context, log *slog.Logger) error {
	cfg, err := loadConfig(false)
	if err != nil {
		return err
	}
	applied, err := db.Migrate(ctx, cfg.MigrateURL, cfg.AppRole)
	if err != nil {
		return err
	}
	log.Info("migrations applied", "applied", applied)
	return nil
}

func createTenant(ctx context.Context, log *slog.Logger, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: core tenant <name> <staff-email>")
	}
	cfg, err := loadConfig(false)
	if err != nil {
		return err
	}
	conn, err := pgx.Connect(ctx, cfg.MigrateURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	var tenant, staff string
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, args[0]).Scan(&tenant); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `INSERT INTO staff (tenant_id, email) VALUES ($1, $2) RETURNING id`, tenant, args[1]).Scan(&staff)
	})
	if err != nil {
		return err
	}
	log.Info("tenant created", "tenant", tenant, "staff", staff)
	return nil
}

func serve(ctx context.Context, log *slog.Logger) error {
	cfg, err := loadConfig(true)
	if err != nil {
		return err
	}
	keys, err := secure.NewKeys(cfg.Secret)
	if err != nil {
		return err
	}
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.CheckRole(ctx); err != nil {
		return err
	}

	var sender mail.Sender
	var mailbox *mail.Mailbox
	switch cfg.Mailer {
	case "smtp":
		sender = cfg.SMTP
	case "mailbox":
		mailbox = &mail.Mailbox{}
		sender = mailbox
	default:
		sender = mail.Log{L: log}
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &mail.Worker{DB: database, Sender: sender})
	jobs, err := river.NewClient(riverpgxv5.New(database.Pool), &river.Config{
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
		Workers: workers,
		Logger:  log,
	})
	if err != nil {
		return err
	}
	if err := jobs.Start(ctx); err != nil {
		return err
	}

	api.TrustProxy = cfg.TrustProxy
	authn := &auth.Authenticator{DB: database, Keys: keys, Policy: api.Policy(), SecureCookies: cfg.SecureCookies}
	srv := &api.Server{DB: database, Keys: keys, Auth: authn, Jobs: jobs, Log: log, PublicURL: cfg.PublicURL}

	mux := http.NewServeMux()
	rpc := srv.Handler()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		c, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := database.Pool.Ping(c); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ready"))
	})
	if mailbox != nil {
		// Development only: lets the browser test read the mail a candidate gets.
		mux.HandleFunc("GET /dev/mailbox", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mailbox.List())
		})
	}
	if cfg.StaticDir != "" {
		mux.Handle("/", spa(cfg.StaticDir))
	}
	// Connect procedures are /assessmatrix.v1.<Service>/<Method>.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assessmatrix.v1.") {
			rpc.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequests(log, sameOrigin(cfg.PublicURL, root)),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Listen, "mailer", cfg.Mailer)
		errc <- server.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// Stop taking requests first, then let in-flight mail jobs finish.
	err = errors.Join(server.Shutdown(shutdown), jobs.Stop(shutdown))
	return err
}

// sameOrigin refuses a browser POST from another origin. SameSite=Strict
// cookies already stop cross-site requests carrying a session; this is the
// second lock on the same door.
func sameOrigin(publicURL string, next http.Handler) http.Handler {
	want := strings.TrimRight(publicURL, "/")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet && o != want {
			http.Error(w, "cross-origin request refused", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// logRequests writes one structured line per request: method, path, status
// and duration. Bodies are never logged; they carry answers and tokens.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return
		}
		log.Info("request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"ms", time.Since(start).Milliseconds(), "request_id", r.Header.Get("X-Request-Id"))
	})
}

// spa serves the built web app, falling back to index.html.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}
