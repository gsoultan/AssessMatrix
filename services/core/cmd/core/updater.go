package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/gsoultan/storm"
	"github.com/gsoultan/storm/runtime"

	"github.com/gsoultan/assessmatrix/services/core/internal/db"
	"github.com/gsoultan/assessmatrix/services/core/internal/store/queries"
)

// UpdaterProtocol versions what `core update` prints and accepts. The
// updater on the host checks it, so an updater script from one release
// keeps working with the core of the next, and refuses one it cannot read.
// Add fields freely; bump it only for a change an old updater would misread.
const UpdaterProtocol = 1

// Claim is what `core update claim` prints: the update to run now, or only
// the protocol when there is none.
type Claim struct {
	Protocol           int    `json:"protocol"`
	ID                 string `json:"id,omitempty"`
	FromVersion        string `json:"from_version,omitempty"`
	TargetVersion      string `json:"target_version,omitempty"`
	Image              string `json:"image,omitempty"`
	PsychometricsImage string `json:"psychometrics_image,omitempty"`
}

var reportStates = map[string]bool{"running": true, "succeeded": true, "failed": true, "rolled_back": true}

// updaterCommand is the host updater's way in. It runs as the schema owner
// (AM_MIGRATE_DATABASE_URL), which the application role is not: nothing a
// browser reaches can claim or close an update.
func updaterCommand(ctx context.Context, args []string, out io.Writer) error {
	usage := errors.New("usage: core update claim [host] | core update report <id> <running|succeeded|failed|rolled_back> <message>")
	if len(args) == 0 {
		return usage
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
	return runUpdaterCommand(ctx, db.Conn(conn), args, out, usage)
}

func runUpdaterCommand(ctx context.Context, ex runtime.Executor, args []string, out io.Writer, usage error) error {
	switch args[0] {
	case "claim":
		host := ""
		if len(args) > 1 {
			host = args[1]
		} else {
			host, _ = os.Hostname()
		}
		c := Claim{Protocol: UpdaterProtocol}
		row, ok, err := queries.UpdateClaim.One(ctx, ex, host)
		if err != nil {
			return err
		}
		if ok {
			c.ID, c.FromVersion, c.TargetVersion = storm.UUID(row.ID).String(), row.FromVersion, row.TargetVersion
			c.Image, c.PsychometricsImage = row.Image, row.PsychometricsImage
		}
		return json.NewEncoder(out).Encode(c)
	case "report":
		if len(args) < 4 || !reportStates[args[2]] {
			return usage
		}
		id, err := storm.ParseUUID(args[1])
		if err != nil {
			return fmt.Errorf("update id: %w", err)
		}
		r, _, err := queries.UpdateReport.One(ctx, ex, [16]byte(id), args[2], strings.Join(args[3:], " "))
		if err != nil {
			return err
		}
		if !r.Yes {
			return fmt.Errorf("update %s is not running", args[1])
		}
		return nil
	}
	return usage
}

// probe asks the server on AM_LISTEN whether it is ready, for the updater's
// health check: the image has no shell or curl to do it with.
func probe(ctx context.Context) error {
	addr := env("AM_LISTEN", ":8080")
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/readyz", nil)
	if err != nil {
		return err
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("not ready: %s", res.Status)
	}
	fmt.Println(version)
	return nil
}
