// Package update finds out whether a newer release of AssessMatrix exists,
// and which one this installation can move to next.
//
// It never changes the installation. The core asks the release feed, shows
// a super administrator what it found, and records their request; an
// updater on the host applies it (deploy/updater, docs/13 §2).
package update

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"
)

// Version is a release's semantic version: vMAJOR.MINOR.PATCH, with an
// optional pre-release part (v1.4.0-rc.1). Build metadata is ignored.
type Version struct {
	Major, Minor, Patch int
	Pre                 string
}

// ParseVersion reads "v1.2.3" or "1.2.3". A development build ("dev") is
// not a version: it is never offered an update.
func ParseVersion(s string) (Version, error) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexByte(s, '+'); i >= 0 {
		s = s[:i]
	}
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return Version{}, fmt.Errorf("%q is not a version like v1.2.3", s)
	}
	var n [3]int
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil || v < 0 || (len(p) > 1 && p[0] == '0') {
			return Version{}, fmt.Errorf("%q is not a version like v1.2.3", s)
		}
		n[i] = v
	}
	return Version{Major: n[0], Minor: n[1], Patch: n[2], Pre: pre}, nil
}

func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	if v.Pre != "" {
		s += "-" + v.Pre
	}
	return s
}

// Compare orders versions by semver precedence: -1, 0 or 1.
func (v Version) Compare(o Version) int {
	if c := cmp.Compare(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmp.Compare(v.Patch, o.Patch); c != 0 {
		return c
	}
	// A release outranks its own pre-releases.
	switch {
	case v.Pre == o.Pre:
		return 0
	case v.Pre == "":
		return 1
	case o.Pre == "":
		return -1
	}
	return comparePre(v.Pre, o.Pre)
}

// comparePre compares dot-separated pre-release identifiers: numbers
// numerically, below words, and a shorter list first when one is a prefix.
func comparePre(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		var c int
		switch {
		case aerr == nil && berr == nil:
			c = cmp.Compare(an, bn)
		case aerr == nil:
			c = -1
		case berr == nil:
			c = 1
		default:
			c = strings.Compare(as[i], bs[i])
		}
		if c != 0 {
			return c
		}
	}
	return cmp.Compare(len(as), len(bs))
}

// Kind says how big a step from v to o is: "major", "minor", "patch", or ""
// when o is not newer.
func (v Version) Kind(o Version) string {
	switch {
	case o.Compare(v) <= 0:
		return ""
	case o.Major != v.Major:
		return "major"
	case o.Minor != v.Minor:
		return "minor"
	}
	return "patch"
}
