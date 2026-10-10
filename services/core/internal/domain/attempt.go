package domain

import (
	"strconv"
	"time"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

// Event kinds that change what the candidate has answered. Every other kind
// is stored as evidence and never changes an answer.
const (
	KindResponseChanged = "response.changed"
	KindResponseTyped   = "response.typed"
	KindAnswerAttached  = "answer.attached"
	KindAnswerDetached  = "answer.detached"
	KindItemFlagged     = "item.flagged"
	KindItemUnflagged   = "item.unflagged"

	// Written by the server, never accepted from a client.
	KindAttemptStarted   = "attempt.started"
	KindIdentityVerified = "identity.verified"
	KindAttemptSubmitted = "attempt.submitted"
	KindAttemptExpired   = "attempt.expired"
	// A person scored an essay: item_id, values[0] the points, detail their
	// note to the candidate, text the reviewer ("staff:<id>"). The latest for
	// an item wins, so a second look is a new event, never an edit.
	KindReviewScored = "review.scored"
)

// ServerOnly kinds are refused in AppendEvents.
var ServerOnly = map[string]bool{
	KindAttemptStarted: true, KindIdentityVerified: true, KindAttemptSubmitted: true, KindAttemptExpired: true,
	KindReviewScored: true,
}

// Review is a person's score for one essay.
type Review struct {
	Points int32
	Note   string
	By     string
	At     int64
}

// Reviews folds the log's review events: the latest per item.
func Reviews(events []*v1.AttemptEvent) map[string]Review {
	out := map[string]Review{}
	for _, ev := range events {
		if ev.GetKind() != KindReviewScored || ev.GetItemId() == "" || len(ev.GetValues()) == 0 {
			continue
		}
		n, err := strconv.ParseInt(ev.GetValues()[0], 10, 32)
		if err != nil {
			continue
		}
		out[ev.GetItemId()] = Review{Points: int32(n), Note: ev.GetDetail(), By: ev.GetText(), At: ev.GetServerTime()}
	}
	return out
}

// Grace periods for answers that reach the server after the deadline.
//
// SyncGrace covers ordinary latency. OfflineGrace covers a candidate who
// answered before the deadline while disconnected and synced afterwards: their
// device time must say the answer came before the deadline, and it must reach
// us within this window. Losing work that was done in time is the worst bug
// this system can have; letting a manipulated clock extend an exam
// indefinitely is the second worst. These two numbers are the trade.
const (
	SyncGrace    = 2 * time.Minute
	OfflineGrace = 30 * time.Minute
)

// Counts reports whether an answer-changing event falls inside the time limit.
func Counts(ev *v1.AttemptEvent, deadline time.Time) bool {
	if deadline.IsZero() {
		return true
	}
	server := time.UnixMilli(ev.GetServerTime())
	if !server.After(deadline.Add(SyncGrace)) {
		return true
	}
	client := time.UnixMilli(ev.GetClientTime())
	return !client.After(deadline) && !server.After(deadline.Add(OfflineGrace))
}

// Fold replays the log into the candidate's current answers.
func Fold(events []*v1.AttemptEvent, deadline time.Time) (map[string]*v1.ItemAnswer, []string) {
	answers := map[string]*v1.ItemAnswer{}
	flagged := []string{}
	get := func(id string) *v1.ItemAnswer {
		a := answers[id]
		if a == nil {
			a = &v1.ItemAnswer{}
			answers[id] = a
		}
		return a
	}
	for _, ev := range events {
		id := ev.GetItemId()
		switch ev.GetKind() {
		case KindResponseChanged, KindResponseTyped, KindAnswerAttached, KindAnswerDetached:
			if id == "" || !Counts(ev, deadline) {
				continue
			}
		}
		switch ev.GetKind() {
		case KindResponseChanged:
			get(id).Values = append([]string(nil), ev.GetValues()...)
		case KindResponseTyped:
			get(id).Text = ev.GetText()
		case KindAnswerAttached:
			get(id).File = ev.GetFile()
		case KindAnswerDetached:
			get(id).File = nil
		case KindItemFlagged:
			if id != "" && !contains(flagged, id) {
				flagged = append(flagged, id)
			}
		case KindItemUnflagged:
			flagged = remove(flagged, id)
		}
	}
	for id, a := range answers {
		if len(a.GetValues()) == 0 && a.GetText() == "" && a.GetFile() == nil {
			delete(answers, id)
		}
	}
	return answers, flagged
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func remove(xs []string, s string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}
