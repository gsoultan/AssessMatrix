package update

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sync"
	"time"
)

// Plan is what this installation can do about the releases it knows of.
type Plan struct {
	// Next is the newest release it may update to directly. Nil when none is newer.
	Next *Release
	// Latest is the newest release there is. It differs from Next when the
	// installation must go through Next first.
	Latest *Release
}

// PlanFrom picks the next step from current among releases. A release whose
// min_upgrade_from is newer than current is reached through an older one.
func PlanFrom(current Version, releases []Release) Plan {
	newer := slices.DeleteFunc(slices.Clone(releases), func(r Release) bool { return r.Version.Compare(current) <= 0 })
	slices.SortFunc(newer, func(a, b Release) int { return b.Version.Compare(a.Version) })
	var p Plan
	if len(newer) == 0 {
		return p
	}
	p.Latest = &newer[0]
	for i := range newer {
		r := &newer[i]
		if r.Manifest.MinUpgradeFrom == "" {
			p.Next = r
			break
		}
		if min, err := ParseVersion(r.Manifest.MinUpgradeFrom); err == nil && current.Compare(min) >= 0 {
			p.Next = r
			break
		}
	}
	return p
}

// Status is what the last check found.
type Status struct {
	// Current is this build's version; empty for a development build.
	Current string
	// Enabled is false when checks are off (AM_UPDATE_CHECK=off) or the
	// build has no version to compare.
	Enabled   bool
	CheckedAt time.Time
	// Err is why the last check failed, if it did. The plan from the check
	// before stays.
	Err  string
	Plan Plan
}

// Checker asks a feed on a schedule and keeps the answer in memory. Each
// replica asks for itself; a check is one request to the feed plus one per
// newer release, a few times a day.
type Checker struct {
	Feed     Feed
	Current  string
	Interval time.Duration // default 12h
	Log      *slog.Logger
	Now      func() time.Time

	mu      sync.Mutex
	status  Status
	running sync.Mutex
}

// NewChecker returns a checker for this build. feed nil means checks are off.
func NewChecker(feed Feed, current string, log *slog.Logger) *Checker {
	c := &Checker{Feed: feed, Current: current, Log: log}
	_, err := ParseVersion(current)
	c.status = Status{Enabled: feed != nil && err == nil}
	if err == nil {
		c.status.Current = current
	}
	return c
}

func (c *Checker) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Status is the last check's answer.
func (c *Checker) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// minGap stops a button pressed repeatedly from hammering the feed.
const minGap = 30 * time.Second

// Check asks the feed now, unless it was asked moments ago, and returns the answer.
func (c *Checker) Check(ctx context.Context) Status {
	st := c.Status()
	if !st.Enabled {
		return st
	}
	c.running.Lock()
	defer c.running.Unlock()
	st = c.Status()
	if !st.CheckedAt.IsZero() && c.now().Sub(st.CheckedAt) < minGap {
		return st
	}
	cur, _ := ParseVersion(c.Current)
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	releases, err := c.Feed.Releases(ctx, cur)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.status.CheckedAt = c.now()
	if err != nil {
		c.status.Err = err.Error()
		if c.Log != nil {
			c.Log.Warn("update check failed", "err", err)
		}
		return c.status
	}
	c.status.Err = ""
	c.status.Plan = PlanFrom(cur, releases)
	if p := c.status.Plan; p.Next != nil && c.Log != nil {
		c.Log.Info("an update is available", "current", c.Current, "next", p.Next.Version.String(),
			"latest", p.Latest.Version.String(), "security", p.Next.Manifest.Security)
	}
	return c.status
}

// Run checks soon after start, then every Interval with a little jitter,
// until ctx ends.
func (c *Checker) Run(ctx context.Context) {
	if !c.Status().Enabled {
		return
	}
	every := c.Interval
	if every <= 0 {
		every = 12 * time.Hour
	}
	wait := time.Minute + rand.N(time.Minute)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		c.Check(ctx)
		wait = every + rand.N(every/10+1)
	}
}
