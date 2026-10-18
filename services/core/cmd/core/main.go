// Command core is the AssessMatrix API server.
//
//	core migrate                       apply schema migrations as the owner
//	core serve                         run the API and the mail worker
//	core tenant <name> <staff-email>   create a tenant and its first staff member
//	core setup-code                    print the code first-run setup at /setup asks for
//
// Configuration is environment only; see config.go.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"

	"github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1/assessmatrixv1connect"
	"github.com/gsoultan/assessmatrix/services/core/internal/api"
	"github.com/gsoultan/assessmatrix/services/core/internal/auth"
	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/mail"
	"github.com/gsoultan/assessmatrix/services/core/internal/media"
	"github.com/gsoultan/assessmatrix/services/core/internal/metrics"
	"github.com/gsoultan/assessmatrix/services/core/internal/secure"
	"github.com/gsoultan/assessmatrix/services/core/internal/webhook"
)

// version is set at build time (deploy/Dockerfile).
var version = "dev"

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
	case "setup-code":
		err = printSetupCode()
	default:
		err = fmt.Errorf("unknown command %q (want migrate, serve, tenant or setup-code)", cmd)
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
	if os.Getenv("AM_SECRET") == "" {
		log.Warn("AM_SECRET is not set: attempts from before sealing stay unsealed and read as altered until migrate runs with it")
		return nil
	}
	secret, err := decodeSecret(os.Getenv("AM_SECRET"))
	if err != nil {
		return err
	}
	keys, err := secure.NewKeys(secret)
	if err != nil {
		return err
	}
	return sealLegacy(ctx, log, cfg.MigrateURL, keys)
}

// sealLegacy seals attempts written before attempt rows were sealed. Cheap
// when there are none, so it runs after every migrate.
func sealLegacy(ctx context.Context, log *slog.Logger, ownerURL string, keys *secure.Keys) error {
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	sealed, left, err := api.SealUnsealed(ctx, conn, keys)
	if err != nil {
		return fmt.Errorf("seal attempts: %w", err)
	}
	if sealed > 0 || left > 0 {
		log.Info("attempts sealed", "sealed", sealed, "altered_left_unsealed", left)
	}
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
	err = conn.QueryRow(ctx, `SELECT tenant_id, staff_id FROM am_create_tenant($1, '', $2, '')`, args[0], args[1]).Scan(&tenant, &staff)
	if err != nil {
		return err
	}
	log.Info("tenant created", "tenant", tenant, "staff", staff)
	return nil
}

// printSetupCode prints the code /setup asks for. It needs only AM_SECRET.
func printSetupCode() error {
	secret, err := decodeSecret(os.Getenv("AM_SECRET"))
	if err != nil {
		return err
	}
	keys, err := secure.NewKeys(secret)
	if err != nil {
		return err
	}
	fmt.Println(keys.SetupCode())
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
	// A single-machine install may hand serve the owner too, so the first
	// start sets up the schema by itself. The owner connection is closed
	// before anything is served; requests only ever run as the app role.
	if cfg.MigrateURL != "" {
		applied, err := db.Migrate(ctx, cfg.MigrateURL, cfg.AppRole)
		if err != nil {
			return fmt.Errorf("set up the database: %w", err)
		}
		log.Info("migrations applied", "applied", applied)
		if err := sealLegacy(ctx, log, cfg.MigrateURL, keys); err != nil {
			return err
		}
	}
	database, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.CheckRole(ctx); err != nil {
		return err
	}
	if err := checkSchema(ctx, database); err != nil {
		return err
	}

	var sender mail.Sender
	var mailbox *mail.Mailbox
	switch cfg.Mailer {
	case "smtp":
		sender = cfg.SMTP
	case "mailbox":
		keep, _ := strconv.Atoi(os.Getenv("AM_DEV_MAILBOX_KEEP"))
		mailbox = &mail.Mailbox{Keep: keep}
		sender = mailbox
	default:
		sender = mail.Log{L: log}
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &mail.Worker{DB: database, Sender: sender})
	hooks := webhook.Policy{AllowPrivate: cfg.WebhookAllowPrivate}
	river.AddWorker(workers, &webhook.Worker{DB: database, Keys: keys, Client: webhook.NewClient(hooks)})
	retention := &api.RetentionWorker{}
	river.AddWorker(workers, retention)
	jobs, err := river.NewClient(riverpgxv5.New(database.Pool), &river.Config{
		// Webhooks have their own queue: a slow receiver never delays mail.
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}, webhook.Queue: {MaxWorkers: 20}},
		Workers: workers,
		Logger:  log,
		// Records past their retention period go within the hour (docs/13).
		PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Hour),
			func() (river.JobArgs, *river.InsertOpts) { return api.RetentionArgs{}, nil },
			&river.PeriodicJobOpts{RunOnStart: true})},
	})
	if err != nil {
		return err
	}

	api.TrustProxy = cfg.TrustProxy
	authn := &auth.Authenticator{DB: database, Keys: keys, Policy: api.Policy(), SecureCookies: cfg.SecureCookies}
	store, err := openMedia(ctx, cfg.Media)
	if err != nil {
		return err
	}
	srv := &api.Server{DB: database, Keys: keys, Auth: authn, Jobs: jobs, Log: log, PublicURL: cfg.PublicURL, RunnerURL: cfg.RunnerURL,
		Media: media.Regional{Store: store, Region: cfg.Media.Region}, WebhookPolicy: hooks}
	retention.Server = srv
	if err := jobs.Start(ctx); err != nil {
		return err
	}
	announceSetup(ctx, log, database, keys, cfg.PublicURL)

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
		mux.HandleFunc("GET /dev/mailbox", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(mailbox.List(r.URL.Query().Get("to")))
		})
	}
	images := srv.MediaHandler()
	// Anyone may check a credential: no session, ids only.
	public := srv.PublicHandler()
	mux.Handle("GET /credentials/", public)
	mux.Handle("GET /issuers/", public)
	mux.Handle(api.MediaPrefix, images)
	mux.Handle("POST /media", images)
	if cfg.StaticDir != "" {
		mux.Handle("/", spa(cfg.StaticDir))
	}
	// Connect procedures are /assessmatrix.v1.<Service>/<Method>.
	root := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/assessmatrix.v1.") {
			// A monitor stream outlives the server's write timeout by design;
			// it ends itself after api.WatchFor.
			if r.URL.Path == assessmatrixv1connect.ProctorServiceWatchExamProcedure {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(api.WatchFor + time.Minute))
			}
			rpc.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           logRequests(log, securityHeaders(sameOrigin(root, cfg.PublicURL, cfg.RunnerURL), strings.HasPrefix(cfg.PublicURL, "https://"))),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	errc := make(chan error, 2)
	if cfg.MetricsListen != "off" {
		// Its own port, so the public proxy never exposes it.
		m := http.NewServeMux()
		m.Handle("GET /metrics", metrics.Handler())
		ms := &http.Server{Addr: cfg.MetricsListen, Handler: m, ReadHeaderTimeout: 5 * time.Second}
		go func() { errc <- ms.ListenAndServe() }()
		defer ms.Close()
	}
	go func() {
		log.Info("listening", "addr", cfg.Listen, "mailer", cfg.Mailer, "version", version)
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

// checkSchema stops a server whose database is older than its code, with
// the command that fixes it, instead of failing on the first request.
func checkSchema(ctx context.Context, d *db.DB) error {
	var have string
	err := d.Pool.QueryRow(ctx, `SELECT am_schema_version()`).Scan(&have)
	want := db.LatestMigration()
	if err != nil {
		return fmt.Errorf("the database schema is not set up for this build (%v): run `core migrate`, "+
			"or set AM_MIGRATE_DATABASE_URL so serve applies it", err)
	}
	if have < want {
		return fmt.Errorf("the database schema is at %s but this build needs %s: run `core migrate`, "+
			"or set AM_MIGRATE_DATABASE_URL so serve applies it", have, want)
	}
	return nil
}

// announceSetup prints where and with what code to finish first-run setup,
// while there is no organisation yet. The code is useless once setup is done.
func announceSetup(ctx context.Context, log *slog.Logger, d *db.DB, keys *secure.Keys, publicURL string) {
	var needed bool
	if err := d.Pool.QueryRow(ctx, `SELECT am_setup_needed()`).Scan(&needed); err != nil || !needed {
		return
	}
	log.Warn("first-run setup is open: finish it at the URL with this code", "url", publicURL+"/setup",
		"setup_code", keys.SetupCode())
}

func openMedia(ctx context.Context, m mediaConfig) (media.Store, error) {
	switch m.Store {
	case "fs":
		return media.FS{Dir: m.Dir}, nil
	case "s3":
		s3, err := media.NewS3(m.Endpoint, m.Key, m.Secret, m.Bucket, !m.Insecure)
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return s3, s3.Check(c)
	}
	return nil, nil
}

// sameOrigin refuses a browser POST from another origin. SameSite=Strict
// cookies already stop cross-site requests carrying a session; this is the
// second lock on the same door.
func sameOrigin(next http.Handler, origins ...string) http.Handler {
	ok := map[string]bool{}
	for _, o := range origins {
		ok[strings.TrimRight(o, "/")] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && r.Method != http.MethodGet && !ok[o] {
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

// Unwrap lets http.ResponseController reach the connection, which a stream
// needs to flush each message and to outlive the write timeout.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush sends a stream's message now; connect needs it to stream at all.
func (w *statusWriter) Flush() { http.NewResponseController(w.ResponseWriter).Flush() }

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

// spa serves the built web app: the studio, the candidate runner under
// /take/ and the public credential check under /verify/. A path that is not
// a file falls back to its app's index. Hashed assets are cached for good;
// everything else is revalidated, so a deploy reaches the next page load (and
// the runner's service worker sees a new sw.js and replaces its shell).
func spa(dir string) http.Handler {
	// The runner's install manifest; Go's table does not know the extension.
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := filepath.Clean("/" + r.URL.Path)
		p := filepath.Join(dir, clean)
		if st, err := os.Stat(p); err != nil || st.IsDir() {
			index := "index.html"
			if clean == "/take" || strings.HasPrefix(clean, "/take/") {
				index = "take/index.html"
			} else if clean == "/verify" || strings.HasPrefix(clean, "/verify/") {
				index = "verify/index.html"
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, index))
			return
		}
		if strings.HasPrefix(clean, "/assets/") || strings.HasPrefix(clean, "/take/assets/") || strings.HasPrefix(clean, "/verify/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
