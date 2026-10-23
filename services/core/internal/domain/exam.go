// Package domain holds the rules that do not depend on storage: what a valid
// instrument is, how an attempt's answers fold out of its event log, and how
// it is scored. They are ports of apps/studio/src/store.ts, so the prototype
// and the server agree on every result.
package domain

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
	"google.golang.org/protobuf/proto"
)

// Limits keep one instrument from exhausting memory or the row size.
const (
	MaxSections       = 50
	MaxItems          = 500
	MaxChoices        = 20
	MaxText           = 20_000
	MaxIDLen          = 64
	MaxMediaURL       = 2_000_000 // data: URLs, until media upload lands
	MaxScalePoints    = 11
	MaxSectionMinutes = 24 * 60
)

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid instrument")

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

func AllItems(e *v1.Exam) []*v1.Item {
	var out []*v1.Item
	for _, s := range e.GetSections() {
		out = append(out, s.GetItems()...)
	}
	return out
}

func TotalMinutes(e *v1.Exam) int32 {
	var n int32
	for _, s := range e.GetSections() {
		n += s.GetMinutes()
	}
	return n
}

func IsScale(t v1.ItemType) bool {
	return t == v1.ItemType_ITEM_TYPE_LIKERT || t == v1.ItemType_ITEM_TYPE_RATING
}

func validID(s string) bool {
	if s == "" || len(s) > MaxIDLen {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

var uploaded = regexp.MustCompile(`^/media/[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// MediaURL reports whether u is a place an image may be: one uploaded to this
// server (/media/<uuid>), an inline data:image URL, or https.
func MediaURL(u string) bool {
	return uploaded.MatchString(u) || strings.HasPrefix(u, "data:image/") || strings.HasPrefix(u, "https://")
}

func validMedia(m *v1.Media, where string) error {
	if m == nil {
		return nil
	}
	if len(m.GetUrl()) > MaxMediaURL {
		return invalid("%s: image is too large", where)
	}
	if !MediaURL(m.GetUrl()) {
		return invalid("%s: image must be an uploaded image, a data:image URL or https", where)
	}
	if len(m.GetAlt()) > MaxText {
		return invalid("%s: alt text is too long", where)
	}
	return nil
}

// ValidateSections checks the authored content. It does not judge whether the
// instrument is ready to publish; see ReadyToPublish.
func ValidateSections(e *v1.Exam) error {
	if len(e.GetSections()) == 0 {
		return invalid("an instrument needs at least one section")
	}
	if len(e.GetSections()) > MaxSections {
		return invalid("more than %d sections", MaxSections)
	}
	if len(e.GetTitle()) > 300 || len(e.GetIssuer()) > 300 {
		return invalid("title and issuer are limited to 300 characters")
	}
	if e.GetTier() < 0 || e.GetTier() > 3 {
		return invalid("tier must be 0 to 3")
	}
	if e.GetPassPct() < 0 || e.GetPassPct() > 100 {
		return invalid("pass mark must be 0 to 100")
	}
	ids := map[string]bool{}
	count := 0
	for _, s := range e.GetSections() {
		if !validID(s.GetId()) || ids[s.GetId()] {
			return invalid("section id %q is missing, malformed or repeated", s.GetId())
		}
		ids[s.GetId()] = true
		if s.GetMinutes() < 0 || s.GetMinutes() > MaxSectionMinutes {
			return invalid("section %q: minutes out of range", s.GetName())
		}
		if len(s.GetName()) > 300 {
			return invalid("section name is limited to 300 characters")
		}
		for _, it := range s.GetItems() {
			count++
			if count > MaxItems {
				return invalid("more than %d questions", MaxItems)
			}
			if err := validateItem(it, ids); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateItem(it *v1.Item, ids map[string]bool) error {
	if !validID(it.GetId()) || ids[it.GetId()] {
		return invalid("question id %q is missing, malformed or repeated", it.GetId())
	}
	ids[it.GetId()] = true
	where := "question " + it.GetId()
	if it.GetType() == v1.ItemType_ITEM_TYPE_UNSPECIFIED || int32(it.GetType()) > int32(v1.ItemType_ITEM_TYPE_RATING) {
		return invalid("%s: unknown type", where)
	}
	if len(it.GetStem()) > MaxText || len(it.GetSkill()) > 300 {
		return invalid("%s: text too long", where)
	}
	if it.GetPoints() < 0 || it.GetPoints() > 1000 {
		return invalid("%s: points out of range", where)
	}
	if it.GetPenalty() < 0 || it.GetPenalty() > it.GetPoints() {
		return invalid("%s: a penalty runs from 0 to the question's points", where)
	}
	if it.GetPenalty() > 0 && !AutoScored(it.GetType()) {
		return invalid("%s: only machine-scored questions can carry a penalty", where)
	}
	if it.GetPartialCredit() && it.GetType() != v1.ItemType_ITEM_TYPE_MULTIPLE {
		return invalid("%s: partial credit is for multiple-answer questions", where)
	}
	if err := validMedia(it.GetImage(), where); err != nil {
		return err
	}
	if len(it.GetChoices()) > MaxChoices {
		return invalid("%s: more than %d options", where, MaxChoices)
	}
	choiceIDs := map[string]bool{}
	for _, c := range it.GetChoices() {
		if !validID(c.GetId()) || choiceIDs[c.GetId()] {
			return invalid("%s: option id %q is missing, malformed or repeated", where, c.GetId())
		}
		choiceIDs[c.GetId()] = true
		if len(c.GetText()) > MaxText {
			return invalid("%s: option text too long", where)
		}
		if err := validMedia(c.GetImage(), where); err != nil {
			return err
		}
	}
	if len(it.GetCorrect()) > MaxChoices {
		return invalid("%s: too many keyed answers", where)
	}
	switch it.GetType() {
	case v1.ItemType_ITEM_TYPE_SINGLE, v1.ItemType_ITEM_TYPE_MULTIPLE, v1.ItemType_ITEM_TYPE_BOOLEAN:
		for _, k := range it.GetCorrect() {
			if !choiceIDs[k] {
				return invalid("%s: answer key names an option that does not exist", where)
			}
		}
	case v1.ItemType_ITEM_TYPE_SHORT:
		for _, k := range it.GetCorrect() {
			if len(k) > 500 {
				return invalid("%s: accepted answer too long", where)
			}
		}
	case v1.ItemType_ITEM_TYPE_LIKERT, v1.ItemType_ITEM_TYPE_RATING:
		p := it.GetScale().GetPoints()
		if p < 2 || p > MaxScalePoints {
			return invalid("%s: a scale needs 2 to %d points", where, MaxScalePoints)
		}
	}
	return nil
}

// ReadyToPublish is the extra bar for going live: there is something to sit.
func ReadyToPublish(e *v1.Exam) error {
	if len(AllItems(e)) == 0 {
		return invalid("an instrument with no questions cannot be published")
	}
	return nil
}

// ForCandidate is a deep copy with every answer key removed. Anything served
// to a candidate goes through here.
func ForCandidate(e *v1.Exam) *v1.Exam {
	c := proto.Clone(e).(*v1.Exam)
	for _, it := range AllItems(c) {
		it.Correct = nil
	}
	return c
}

// Score is the attempt's score so far. Choice and short-answer items are
// machine-scored. Essays are never machine-scored: one a person has reviewed
// counts with the points they gave; one still waiting is reported as pending
// and left out of raw, max and pct, so pct is always over what has been
// scored. Scales measure opinion and are not scored at all. Passed is false
// while points are pending: the verdict waits for the person, since the
// pending points can turn it either way.
func Score(e *v1.Exam, answers map[string]*v1.ItemAnswer, reviews map[string]Review) *v1.Score {
	var raw, max, pending int32
	for _, it := range AllItems(e) {
		if IsScale(it.GetType()) {
			continue
		}
		if it.GetType() == v1.ItemType_ITEM_TYPE_ESSAY {
			if r, ok := reviews[it.GetId()]; ok {
				raw += min(max32(r.Points, 0), it.GetPoints())
				max += it.GetPoints()
			} else {
				pending += it.GetPoints()
			}
			continue
		}
		max += it.GetPoints()
		raw += Earned(it, answers[it.GetId()]) - Penalty(it, answers[it.GetId()])
	}
	// Penalties can outweigh what was earned; the total stops at zero.
	raw = max32(raw, 0)
	var pct int32
	if max > 0 {
		pct = int32((float64(raw)/float64(max))*100 + 0.5)
	}
	return &v1.Score{Raw: raw, Max: max, Pct: pct, Passed: pending == 0 && pct >= e.GetPassPct(), PendingHuman: pending}
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

// Earned is what a machine-scored item's answer earns: all or nothing.
func Earned(it *v1.Item, a *v1.ItemAnswer) int32 {
	if it.GetPartialCredit() && it.GetType() == v1.ItemType_ITEM_TYPE_MULTIPLE {
		return partial(it, a)
	}
	if it.GetType() == v1.ItemType_ITEM_TYPE_SHORT {
		given := strings.ToLower(strings.TrimSpace(a.GetText()))
		if given != "" && slices.ContainsFunc(it.GetCorrect(), func(k string) bool {
			return strings.ToLower(strings.TrimSpace(k)) == given
		}) {
			return it.GetPoints()
		}
		return 0
	}
	given := slices.Sorted(slices.Values(a.GetValues()))
	want := slices.Sorted(slices.Values(it.GetCorrect()))
	if len(given) > 0 && slices.Equal(given, want) {
		return it.GetPoints()
	}
	return 0
}

// partial is a multiple-answer item's share: each keyed option picked earns
// points/len(correct), each wrong one picked takes that back, rounded down
// and never below zero.
func partial(it *v1.Item, a *v1.ItemAnswer) int32 {
	keyed := it.GetCorrect()
	if len(keyed) == 0 {
		return 0
	}
	var net int32
	for _, v := range slices.Compact(slices.Sorted(slices.Values(a.GetValues()))) {
		if slices.Contains(keyed, v) {
			net++
		} else {
			net--
		}
	}
	if net <= 0 {
		return 0
	}
	return it.GetPoints() * net / int32(len(keyed))
}

// Penalty is what a wrong answer takes off the total: the item's penalty when
// something was answered and it earned nothing. A blank costs nothing.
func Penalty(it *v1.Item, a *v1.ItemAnswer) int32 {
	if it.GetPenalty() <= 0 || !AutoScored(it.GetType()) {
		return 0
	}
	if len(a.GetValues()) == 0 && strings.TrimSpace(a.GetText()) == "" {
		return 0
	}
	if Earned(it, a) > 0 {
		return 0
	}
	return it.GetPenalty()
}

// AutoScored is whether the machine marks an item: everything but essays and
// scales.
func AutoScored(t v1.ItemType) bool {
	return t != v1.ItemType_ITEM_TYPE_ESSAY && !IsScale(t)
}

// Unanswered lists required questions with no answer.
func Unanswered(e *v1.Exam, answers map[string]*v1.ItemAnswer) []string {
	var out []string
	for _, it := range AllItems(e) {
		a := answers[it.GetId()]
		if it.GetRequired() && len(a.GetValues()) == 0 && strings.TrimSpace(a.GetText()) == "" {
			out = append(out, it.GetId())
		}
	}
	return out
}
