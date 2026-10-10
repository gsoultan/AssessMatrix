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
// nothing, short answers ignore case and surrounding space, essays and scales
// are reported as pending, and rounding is half-up.
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
		s := Score(e, c.answers)
		if s.GetRaw() != c.raw || s.GetMax() != 4 || s.GetPct() != c.pct || s.GetPassed() != c.passed || s.GetPendingHuman() != 5 {
			t.Errorf("%s: got %+v", c.name, s)
		}
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
