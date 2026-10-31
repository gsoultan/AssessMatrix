package domain

import (
	"testing"
	"time"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

func exam() *v1.Exam {
	return &v1.Exam{PassPct: 70, Sections: []*v1.Section{{Id: "s", Minutes: 10, Items: []*v1.Item{
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 1, Choices: []*v1.Choice{{Id: "x"}, {Id: "y"}}, Correct: []string{"y"}},
		{Id: "b", Type: v1.ItemType_ITEM_TYPE_MULTIPLE, Points: 2, Choices: []*v1.Choice{{Id: "x"}, {Id: "y"}, {Id: "z"}}, Correct: []string{"x", "z"}},
		{Id: "c", Type: v1.ItemType_ITEM_TYPE_SHORT, Points: 1, Correct: []string{"Bandung", "bdg"}},
		{Id: "d", Type: v1.ItemType_ITEM_TYPE_ESSAY, Points: 4},
		{Id: "e", Type: v1.ItemType_ITEM_TYPE_LIKERT, Points: 1, Scale: &v1.Scale{Points: 5}},
	}}}}
}

// Same cases as the prototype's score(): partial multiple-choice earns
// nothing, short answers ignore case and surrounding space, an unreviewed essay
// is reported as pending (scales are not scored), and rounding is half-up.
func TestScoreMatchesPrototype(t *testing.T) {
	e := exam()
	cases := []struct {
		name    string
		answers map[string]*v1.ItemAnswer
		raw     int32
		pct     int32
		passed  bool
	}{
		{"blank", nil, 0, 0, false},
		{"all right", map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "b": {Values: []string{"z", "x"}}, "c": {Text: " BDG "}}, 4, 100, true},
		{"partial multiple earns nothing", map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "b": {Values: []string{"x"}}, "c": {Text: "bandung"}}, 2, 50, false},
		{"over-selection earns nothing", map[string]*v1.ItemAnswer{"b": {Values: []string{"x", "y", "z"}}}, 0, 0, false},
		{"three of four rounds to 75", map[string]*v1.ItemAnswer{"b": {Values: []string{"x", "z"}}, "c": {Text: "Bandung"}}, 3, 75, true},
	}
	for _, c := range cases {
		// The essay waits, so there is no verdict yet, whatever the rest says.
		s := Score(e, c.answers, nil)
		if s.GetRaw() != c.raw || s.GetMax() != 4 || s.GetPct() != c.pct || s.GetPassed() || s.GetPendingHuman() != 4 {
			t.Errorf("%s: got %+v", c.name, s)
		}
		// Reviewed at 0 of 4, the essay counts and the verdict follows the total.
		s = Score(e, c.answers, map[string]Review{"d": {Points: 0}})
		if s.GetMax() != 8 || s.GetPendingHuman() != 0 || s.GetPassed() != (c.raw*100 >= 8*e.GetPassPct()) {
			t.Errorf("%s, reviewed: got %+v", c.name, s)
		}
	}
}

// A full machine score with an essay still waiting is not a pass: the
// essay's points can turn it either way, so the verdict waits too.
func TestNoVerdictWhileAnEssayWaits(t *testing.T) {
	e := exam()
	all := map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "b": {Values: []string{"z", "x"}}, "c": {Text: "bdg"}}
	if s := Score(e, all, nil); s.GetPassed() {
		t.Fatalf("passed with an essay pending: %+v", s)
	}
	if s := Score(e, all, map[string]Review{"d": {Points: 4}}); !s.GetPassed() {
		t.Fatalf("full marks once reviewed must pass: %+v", s)
	}
}

func TestFoldTakesTheLastAnswerAndFlags(t *testing.T) {
	evs := []*v1.AttemptEvent{
		{Kind: KindResponseChanged, ItemId: "a", Values: []string{"x"}},
		{Kind: KindItemFlagged, ItemId: "a"},
		{Kind: KindResponseChanged, ItemId: "a", Values: []string{"y"}},
		{Kind: KindResponseTyped, ItemId: "c", Text: "draft"},
		{Kind: KindResponseTyped, ItemId: "c", Text: ""},
		{Kind: "focus.lost"},
		{Kind: KindItemFlagged, ItemId: "b"},
		{Kind: KindItemUnflagged, ItemId: "a"},
	}
	answers, flagged := Fold(evs, time.Time{})
	if got := answers["a"].GetValues(); len(got) != 1 || got[0] != "y" {
		t.Errorf("a = %v, want the last selection", got)
	}
	if _, ok := answers["c"]; ok {
		t.Error("clearing a typed answer leaves no answer")
	}
	if len(flagged) != 1 || flagged[0] != "b" {
		t.Errorf("flagged = %v, want [b]", flagged)
	}
}

func TestCountsLateAnswers(t *testing.T) {
	d := time.Unix(1_000_000, 0)
	ev := func(server, client time.Duration) *v1.AttemptEvent {
		return &v1.AttemptEvent{ServerTime: d.Add(server).UnixMilli(), ClientTime: d.Add(client).UnixMilli()}
	}
	cases := []struct {
		name string
		ev   *v1.AttemptEvent
		want bool
	}{
		{"before the deadline", ev(-time.Minute, -time.Minute), true},
		{"within ordinary latency", ev(SyncGrace, time.Hour), true},
		{"offline, done in time, synced late", ev(20*time.Minute, -time.Second), true},
		{"offline, but synced too late", ev(OfflineGrace+time.Second, -time.Second), false},
		{"after the deadline by the device's own clock", ev(5*time.Minute, time.Second), false},
	}
	for _, c := range cases {
		if got := Counts(c.ev, d); got != c.want {
			t.Errorf("%s: counts=%v, want %v", c.name, got, c.want)
		}
	}
	if !Counts(ev(24*time.Hour, 24*time.Hour), time.Time{}) {
		t.Error("an untimed attempt has no deadline")
	}
}

func TestForCandidateStripsKeysOnly(t *testing.T) {
	e := exam()
	c := ForCandidate(e)
	for _, it := range AllItems(c) {
		if len(it.GetCorrect()) > 0 {
			t.Fatalf("%s kept its key", it.GetId())
		}
	}
	if len(AllItems(e)[0].GetCorrect()) == 0 {
		t.Fatal("stripping must not touch the original")
	}
}

func TestValidateRejectsBrokenKeys(t *testing.T) {
	e := exam()
	if err := ValidateSections(e); err != nil {
		t.Fatalf("a valid instrument was refused: %v", err)
	}
	e.Sections[0].Items[0].Correct = []string{"nope"}
	if err := ValidateSections(e); err == nil {
		t.Fatal("a key naming no option must be refused")
	}
	e = exam()
	e.Sections[0].Items[1].Id = "a"
	if err := ValidateSections(e); err == nil {
		t.Fatal("repeated ids must be refused")
	}
	e = exam()
	e.Sections[0].Items[0].Image = &v1.Media{Url: "javascript:alert(1)"}
	if err := ValidateSections(e); err == nil {
		t.Fatal("a non-image URL must be refused")
	}
}

// A reviewed essay counts with the points a person gave, capped at the item's
// points; until then pct is over the machine-scored part only.
func TestReviewedEssayCounts(t *testing.T) {
	e := exam()
	all := map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "b": {Values: []string{"z", "x"}}, "c": {Text: "bdg"}, "d": {Text: "an essay"}}
	s := Score(e, all, map[string]Review{"d": {Points: 2}})
	if s.GetRaw() != 6 || s.GetMax() != 8 || s.GetPct() != 75 || s.GetPendingHuman() != 0 || !s.GetPassed() {
		t.Fatalf("got %+v, want 6/8 75%% passed, nothing pending", s)
	}
	if s := Score(e, all, map[string]Review{"d": {Points: 99}}); s.GetRaw() != 8 {
		t.Fatalf("points beyond the item's must be capped, got raw %d", s.GetRaw())
	}
	ev := []*v1.AttemptEvent{
		{Kind: KindReviewScored, ItemId: "d", Values: []string{"1"}},
		{Kind: KindReviewScored, ItemId: "d", Values: []string{"3"}, Detail: "clear argument"},
	}
	if r := Reviews(ev)["d"]; r.Points != 3 || r.Note != "clear argument" {
		t.Fatalf("the latest review must win, got %+v", r)
	}
}

// With partial credit on, each keyed option picked earns its share and each
// wrong one takes a share back, rounded down and never below zero. Off, the
// same answers earn nothing, as before.
func TestPartialCredit(t *testing.T) {
	it := &v1.Item{Id: "m", Type: v1.ItemType_ITEM_TYPE_MULTIPLE, Points: 4, PartialCredit: true,
		Choices: []*v1.Choice{{Id: "w"}, {Id: "x"}, {Id: "y"}, {Id: "z"}}, Correct: []string{"x", "z"}}
	cases := []struct {
		picked []string
		want   int32
	}{
		{nil, 0},
		{[]string{"x"}, 2},
		{[]string{"z", "x"}, 4},
		{[]string{"x", "y"}, 0},
		{[]string{"x", "z", "y"}, 2},
		{[]string{"w", "y"}, 0},
		{[]string{"x", "x"}, 2},
	}
	for _, c := range cases {
		if got := Earned(it, &v1.ItemAnswer{Values: c.picked}); got != c.want {
			t.Errorf("picked %v: earned %d, want %d", c.picked, got, c.want)
		}
	}
	it.Points = 3
	if got := Earned(it, &v1.ItemAnswer{Values: []string{"x"}}); got != 1 {
		t.Errorf("half of 3 must round down to 1, got %d", got)
	}
	it.PartialCredit = false
	if got := Earned(it, &v1.ItemAnswer{Values: []string{"x"}}); got != 0 {
		t.Errorf("without partial credit half right earns nothing, got %d", got)
	}
}

// A wrong answer costs the penalty, a blank costs nothing, and the total
// never goes below zero.
func TestPenalty(t *testing.T) {
	e := &v1.Exam{PassPct: 50, Sections: []*v1.Section{{Id: "s", Minutes: 10, Items: []*v1.Item{
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 4, Penalty: 1, Choices: []*v1.Choice{{Id: "x"}, {Id: "y"}}, Correct: []string{"y"}},
		{Id: "b", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 4, Penalty: 1, Choices: []*v1.Choice{{Id: "x"}, {Id: "y"}}, Correct: []string{"y"}},
		{Id: "c", Type: v1.ItemType_ITEM_TYPE_SHORT, Points: 2, Penalty: 2, Correct: []string{"bdg"}},
	}}}}
	if err := ValidateSections(e); err != nil {
		t.Fatalf("a valid instrument was refused: %v", err)
	}
	cases := []struct {
		name    string
		answers map[string]*v1.ItemAnswer
		raw     int32
	}{
		{"blank costs nothing", nil, 0},
		{"one right, one wrong", map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "b": {Values: []string{"x"}}}, 3},
		{"wrong short answer", map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "c": {Text: "jakarta"}}, 2},
		{"blank short answer", map[string]*v1.ItemAnswer{"a": {Values: []string{"y"}}, "c": {Text: "  "}}, 4},
		{"all wrong stops at zero", map[string]*v1.ItemAnswer{"a": {Values: []string{"x"}}, "b": {Values: []string{"x"}}, "c": {Text: "no"}}, 0},
	}
	for _, c := range cases {
		if s := Score(e, c.answers, nil); s.GetRaw() != c.raw || s.GetMax() != 10 {
			t.Errorf("%s: got %+v, want raw %d of 10", c.name, s, c.raw)
		}
	}
}

func TestValidateRejectsBadScoringRules(t *testing.T) {
	bad := []*v1.Item{
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 1, Penalty: 2, Choices: []*v1.Choice{{Id: "x"}}, Correct: []string{"x"}},
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 1, Penalty: -1, Choices: []*v1.Choice{{Id: "x"}}, Correct: []string{"x"}},
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_ESSAY, Points: 4, Penalty: 1},
		{Id: "a", Type: v1.ItemType_ITEM_TYPE_SINGLE, Points: 1, PartialCredit: true, Choices: []*v1.Choice{{Id: "x"}}, Correct: []string{"x"}},
	}
	for i, it := range bad {
		e := &v1.Exam{Sections: []*v1.Section{{Id: "s", Minutes: 10, Items: []*v1.Item{it}}}}
		if err := ValidateSections(e); err == nil {
			t.Errorf("case %d: must be refused", i)
		}
	}
}
