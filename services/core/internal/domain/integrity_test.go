package domain

import (
	"regexp"
	"strconv"
	"testing"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

func ev(kind, item string, values ...int64) *v1.AttemptEvent {
	e := &v1.AttemptEvent{Kind: kind, ItemId: item}
	for _, v := range values {
		e.Values = append(e.Values, strconv.FormatInt(v, 10))
	}
	return e
}

// A log that trips one flag type, for each type.
var trips = map[string]func() (events []*v1.AttemptEvent, intact bool){
	FlagRecordAltered: func() ([]*v1.AttemptEvent, bool) { return nil, false },
	FlagFocusAway: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{ev(KindFocusLost, ""), ev(KindFocusReturned, "", 70_000)}, true
	},
	FlagPaste: func() ([]*v1.AttemptEvent, bool) { return []*v1.AttemptEvent{ev(KindPasted, "i_4", 320)}, true },
	FlagRapidAnswers: func() ([]*v1.AttemptEvent, bool) {
		var out []*v1.AttemptEvent
		for _, id := range []string{"a", "b", "c", "d", "e"} {
			out = append(out, ev(KindItemViewed, id, 1200), &v1.AttemptEvent{Kind: KindResponseChanged, ItemId: id, Values: []string{"x"}})
		}
		return out, true
	},
	FlagDeviceChanged: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{{Kind: KindDeviceSeen, Values: []string{"aa", "mobile"}}, {Kind: KindDeviceSeen, Values: []string{"bb", "desktop"}}}, true
	},
	FlagSecondTab: func() ([]*v1.AttemptEvent, bool) { return []*v1.AttemptEvent{ev(KindSecondTab, "")}, true },
	FlagFullscreen: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{ev(KindFullscreenExited, ""), ev(KindFullscreenExited, "")}, true
	},
	FlagFaceMissing: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{ev(KindFaceMissing, "", 20_000)}, true
	},
	FlagFacesMultiple: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{ev(KindFacesMultiple, "", 9_000, 2)}, true
	},
	FlagCameraStopped: func() ([]*v1.AttemptEvent, bool) {
		return []*v1.AttemptEvent{{Kind: KindVisionStopped, Detail: "camera unplugged"}}, true
	},
}

// The integrity gate (AGENTS.md): every flag type renders a plain-language
// reason a candidate could contest. A contestable reason says what was
// observed with its number, in both languages, and no internal words.
func TestEveryFlagTypeHasAReasonACandidateCouldContest(t *testing.T) {
	jargon := regexp.MustCompile(`(?i)\b(seq|hash|chain|vision\.|focus\.|answer\.|values?\[|ms\b|null|nil)`)
	digit := regexp.MustCompile(`\d`)
	q := func(string) int { return 4 }
	for _, typ := range FlagTypes {
		trip, ok := trips[typ]
		if !ok {
			t.Errorf("%s: no test log trips it; add one", typ)
			continue
		}
		events, intact := trip()
		var got *Flag
		for _, f := range Flags(events, q, intact, 7) {
			if f.Type == typ {
				got = &f
			}
		}
		if got == nil {
			t.Errorf("%s: the log meant to trip it raised no such flag", typ)
			continue
		}
		for lang, r := range map[string]string{"en": got.Reason, "id": got.ReasonID} {
			if len(r) < 25 || !digit.MatchString(r) {
				t.Errorf("%s (%s): reason does not state what was observed: %q", typ, lang, r)
			}
			if jargon.MatchString(r) {
				t.Errorf("%s (%s): reason uses internal words: %q", typ, lang, r)
			}
		}
		if got.Severity == "" {
			t.Errorf("%s: no severity", typ)
		}
	}
}

// Below the thresholds, ordinary life raises nothing.
func TestOrdinaryAttemptRaisesNoFlags(t *testing.T) {
	events := []*v1.AttemptEvent{
		ev(KindFocusLost, ""), ev(KindFocusReturned, "", 4_000),
		ev(KindPasted, "i_1", 12),
		{Kind: KindDeviceSeen, Values: []string{"aa", "mobile"}}, {Kind: KindDeviceSeen, Values: []string{"aa", "mobile"}},
		ev(KindFaceMissing, "", 3_000), ev(KindFacesMultiple, "", 1_000, 2),
	}
	if fs := Flags(events, func(string) int { return 1 }, true, 0); len(fs) != 0 {
		t.Errorf("flags on an ordinary attempt: %+v", fs)
	}
}

// Every signal kind is collected under a purpose the candidate consented to.
func TestEverySignalNeedsAConsent(t *testing.T) {
	for _, k := range []string{KindFocusLost, KindFocusReturned, KindPasted, KindItemViewed, KindDeviceSeen, KindSecondTab,
		KindFullscreenExited, KindFaceMissing, KindFacesMultiple, KindVisionStopped} {
		if PurposeOf(k) == "" {
			t.Errorf("%s has no purpose", k)
		}
	}
	if PurposeOf(KindResponseChanged) != "" {
		t.Error("answers must not need a monitoring consent")
	}
	if !ServerOnly[KindConsentPrefix+PurposeCamera] {
		t.Error("a client must not be able to write its own consent")
	}
}
