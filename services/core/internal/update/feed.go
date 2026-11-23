package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// ManifestAsset is the file every release carries next to its notes
// (.github/workflows/release.yml writes it). A release without one is not
// offered: there is nothing pinned to install.
const ManifestAsset = "assessmatrix-release.json"

// Manifest is what a release says about itself, beyond its version.
type Manifest struct {
	Version string `json:"version"`
	// The images, by digest: what the updater pulls, so a tag moved after
	// the release cannot change what is installed.
	Image              string `json:"image"`
	PsychometricsImage string `json:"psychometrics_image"`
	// The newest migration it ships.
	Schema string `json:"schema"`
	// The oldest version that may update to this one directly. Older
	// installations go through a release in between, which keeps every
	// migration's expand-then-contract steps in order.
	MinUpgradeFrom string `json:"min_upgrade_from"`
	// It fixes a security problem: the studio says so.
	Security bool `json:"security"`
}

// Release is one published release, with its manifest.
type Release struct {
	Version     Version
	PublishedAt time.Time
	NotesURL    string
	// The release notes, cut to a few thousand characters.
	Notes    string
	Manifest Manifest

	manifestURL string
}

// Feed lists published releases, newest or not, in any order.
type Feed interface {
	Releases(ctx context.Context, newerThan Version) ([]Release, error)
}

// digestRe is an image pinned by digest.
var digestRe = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-/:]*@sha256:[0-9a-f]{64}$`)

// GitHub reads a repository's GitHub Releases. Drafts are skipped, and
// pre-releases unless PreReleases is set.
type GitHub struct {
	Repo        string // owner/name
	BaseURL     string // default https://api.github.com
	Client      *http.Client
	PreReleases bool
}

type ghRelease struct {
	TagName     string    `json:"tag_name"`
	HTMLURL     string    `json:"html_url"`
	Body        string    `json:"body"`
	Draft       bool      `json:"draft"`
	Prerelease  bool      `json:"prerelease"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// maxManifests bounds the requests one check makes.
const maxManifests = 10

func (g GitHub) client() *http.Client {
	if g.Client != nil {
		return g.Client
	}
	return &http.Client{Timeout: 20 * time.Second}
}

func (g GitHub) Releases(ctx context.Context, newerThan Version) ([]Release, error) {
	base := strings.TrimRight(g.BaseURL, "/")
	if base == "" {
		base = "https://api.github.com"
	}
	var list []ghRelease
	if err := g.get(ctx, fmt.Sprintf("%s/repos/%s/releases?per_page=30", base, g.Repo), 4<<20, &list); err != nil {
		return nil, err
	}
	var out []Release
	for _, r := range list {
		if r.Draft || (r.Prerelease && !g.PreReleases) {
			continue
		}
		v, err := ParseVersion(r.TagName)
		if err != nil || v.Compare(newerThan) <= 0 {
			continue
		}
		rel := Release{Version: v, PublishedAt: r.PublishedAt, NotesURL: r.HTMLURL, Notes: cut(r.Body, 4000)}
		for _, a := range r.Assets {
			if a.Name == ManifestAsset {
				rel.manifestURL = a.URL
			}
		}
		if rel.manifestURL != "" {
			out = append(out, rel)
		}
	}
	// Newest first, and only as many manifests as could matter.
	slices.SortFunc(out, func(a, b Release) int { return b.Version.Compare(a.Version) })
	if len(out) > maxManifests {
		out = out[:maxManifests]
	}
	kept := out[:0]
	for _, rel := range out {
		var m Manifest
		if err := g.get(ctx, rel.manifestURL, 64<<10, &m); err != nil {
			return nil, fmt.Errorf("release %s: %w", rel.Version, err)
		}
		if err := m.check(rel.Version); err != nil {
			// A broken manifest makes that release unavailable, not the feed.
			continue
		}
		rel.Manifest = m
		kept = append(kept, rel)
	}
	return kept, nil
}

func (m Manifest) check(v Version) error {
	mv, err := ParseVersion(m.Version)
	if err != nil || mv.Compare(v) != 0 {
		return fmt.Errorf("manifest is for %q, not %s", m.Version, v)
	}
	if !digestRe.MatchString(m.Image) {
		return fmt.Errorf("manifest image %q is not pinned by digest", m.Image)
	}
	if m.PsychometricsImage != "" && !digestRe.MatchString(m.PsychometricsImage) {
		return fmt.Errorf("manifest psychometrics image %q is not pinned by digest", m.PsychometricsImage)
	}
	if m.MinUpgradeFrom != "" {
		if _, err := ParseVersion(m.MinUpgradeFrom); err != nil {
			return err
		}
	}
	return nil
}

func (g GitHub) get(ctx context.Context, url string, limit int64, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	// Nothing about the installation goes with it: no host, tenant or count.
	req.Header.Set("User-Agent", "assessmatrix-update-check")
	res, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return err
	}
	if int64(len(body)) > limit {
		return errors.New("answer too large")
	}
	return json.Unmarshal(body, into)
}

func cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	// Do not split a UTF-8 sequence.
	for n > 0 && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}
