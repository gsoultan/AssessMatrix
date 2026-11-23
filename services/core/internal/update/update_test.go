package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVersionOrder(t *testing.T) {
	// Each is older than the next.
	order := []string{"v0.9.9", "v1.0.0-alpha", "v1.0.0-alpha.1", "v1.0.0-alpha.beta", "v1.0.0-beta.2", "v1.0.0-beta.11",
		"v1.0.0-rc.1", "v1.0.0", "1.0.1", "v1.1.0", "v1.10.0", "v2.0.0+build.5"}
	for i := 0; i+1 < len(order); i++ {
		a, err := ParseVersion(order[i])
		if err != nil {
			t.Fatal(err)
		}
		b, err := ParseVersion(order[i+1])
		if err != nil {
			t.Fatal(err)
		}
		if a.Compare(b) != -1 || b.Compare(a) != 1 || a.Compare(a) != 0 {
			t.Errorf("%s should come before %s", order[i], order[i+1])
		}
	}
	for _, bad := range []string{"dev", "", "v1.2", "v1.2.3.4", "v01.2.3", "v1.x.3", "v-1.0.0"} {
		if _, err := ParseVersion(bad); err == nil {
			t.Errorf("%q parsed as a version", bad)
		}
	}
	v, _ := ParseVersion("1.2.3")
	for to, want := range map[string]string{"v1.2.4": "patch", "v1.3.0": "minor", "v2.0.0": "major", "v1.2.3": "", "v1.0.0": ""} {
		o, _ := ParseVersion(to)
		if got := v.Kind(o); got != want {
			t.Errorf("v1.2.3 → %s is %q, want %q", to, got, want)
		}
	}
}

func rel(v, minFrom string) Release {
	ver, _ := ParseVersion(v)
	return Release{Version: ver, Manifest: Manifest{Version: v, MinUpgradeFrom: minFrom}}
}

// An installation too old for the newest release is sent through the newest
// one it can reach, then onwards: every hop is offered in turn.
func TestPlanGoesThroughRequiredReleases(t *testing.T) {
	releases := []Release{rel("v1.0.0", ""), rel("v1.1.0", ""), rel("v1.2.0", "v1.0.0"), rel("v2.0.0", "v1.2.0"), rel("v2.1.0", "v1.2.0")}
	for _, c := range []struct{ from, next, latest string }{
		{"v0.9.0", "v1.1.0", "v2.1.0"},
		{"v1.0.0", "v1.2.0", "v2.1.0"},
		{"v1.2.0", "v2.1.0", "v2.1.0"},
		{"v2.1.0", "", ""},
	} {
		cur, _ := ParseVersion(c.from)
		p := PlanFrom(cur, releases)
		next, latest := "", ""
		if p.Next != nil {
			next = p.Next.Version.String()
		}
		if p.Latest != nil {
			latest = p.Latest.Version.String()
		}
		if next != c.next || latest != c.latest {
			t.Errorf("from %s: next %q latest %q, want %q %q", c.from, next, latest, c.next, c.latest)
		}
	}
}

const digest = "@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// A fake GitHub: a release list and a manifest per release.
func fakeGitHub(t *testing.T, releases map[string]Manifest, extra ...map[string]any) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "assessmatrix-update-check" {
			t.Errorf("User-Agent %q", r.Header.Get("User-Agent"))
		}
		if r.URL.Path == "/repos/acme/am/releases" {
			var list []map[string]any
			for tag := range releases {
				list = append(list, map[string]any{"tag_name": tag, "html_url": "https://example.test/" + tag,
					"body": "Notes for " + tag, "published_at": time.Now().Format(time.RFC3339),
					"assets": []map[string]any{{"name": ManifestAsset, "browser_download_url": srv.URL + "/m/" + tag}}})
			}
			list = append(list, extra...)
			json.NewEncoder(w).Encode(list)
			return
		}
		if tag, ok := strings.CutPrefix(r.URL.Path, "/m/"); ok {
			json.NewEncoder(w).Encode(releases[tag])
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGitHubFeed(t *testing.T) {
	good := func(v string) Manifest {
		return Manifest{Version: v, Image: "ghcr.io/acme/assessmatrix" + digest, Schema: "0013_updates.sql"}
	}
	srv := fakeGitHub(t, map[string]Manifest{
		"v1.0.0": good("v1.0.0"),
		"v1.1.0": good("v1.1.0"),
		// A tag, not a digest: not installable, so not offered.
		"v1.2.0": {Version: "v1.2.0", Image: "ghcr.io/acme/assessmatrix:1.2.0"},
		// The manifest names another version.
		"v1.3.0": good("v1.2.9"),
	},
		map[string]any{"tag_name": "v9.0.0", "draft": true},
		map[string]any{"tag_name": "v8.0.0-rc.1", "prerelease": true,
			"assets": []map[string]any{{"name": ManifestAsset, "browser_download_url": "http://unused"}}},
		map[string]any{"tag_name": "v7.0.0"}, // no manifest
	)
	cur, _ := ParseVersion("v1.0.0")
	got, err := GitHub{Repo: "acme/am", BaseURL: srv.URL}.Releases(context.Background(), cur)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Version.String() != "v1.1.0" || got[0].Notes != "Notes for v1.1.0" || got[0].Manifest.Image == "" {
		t.Fatalf("got %+v, want only v1.1.0", got)
	}
}

type countingFeed struct {
	calls int
	err   error
	list  []Release
}

func (f *countingFeed) Releases(context.Context, Version) ([]Release, error) {
	f.calls++
	return f.list, f.err
}

func TestCheckerKeepsLastPlanOnError(t *testing.T) {
	feed := &countingFeed{list: []Release{rel("v1.1.0", "")}}
	now := time.Now()
	c := NewChecker(feed, "v1.0.0", nil)
	c.Now = func() time.Time { return now }
	if st := c.Check(context.Background()); st.Plan.Next == nil || st.Plan.Next.Version.String() != "v1.1.0" {
		t.Fatalf("plan %+v", st.Plan)
	}
	// Pressed again at once: no second request.
	c.Check(context.Background())
	if feed.calls != 1 {
		t.Errorf("%d requests, want 1", feed.calls)
	}
	now = now.Add(time.Hour)
	feed.err = fmt.Errorf("offline")
	st := c.Check(context.Background())
	if st.Err != "offline" || st.Plan.Next == nil {
		t.Errorf("after a failed check: %+v", st)
	}
}

func TestDevelopmentBuildIsNeverOffered(t *testing.T) {
	feed := &countingFeed{list: []Release{rel("v1.1.0", "")}}
	for _, c := range []*Checker{NewChecker(feed, "dev", nil), NewChecker(nil, "v1.0.0", nil)} {
		if st := c.Check(context.Background()); st.Enabled || st.Plan.Next != nil {
			t.Errorf("checks ran: %+v", st)
		}
	}
	if feed.calls != 0 {
		t.Errorf("the feed was asked %d times", feed.calls)
	}
}
