package domain

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

// Integrity signals (docs/02 §3, tiers 1 and 2). The runner sends them as
// ordinary attempt events, so they sit in the same hash chain as the answers.
// Numbers travel in Values, so the rules below never parse free text.
const (
	KindFocusLost        = "focus.lost"            // the exam was hidden or lost focus
	KindFocusReturned    = "focus.returned"        // values[0]: ms away
	KindPasted           = "answer.pasted"         // item; values[0]: characters
	KindItemViewed       = "item.viewed"           // item; values[0]: ms on the question
	KindDeviceSeen       = "device.seen"           // values[0]: device hash; values[1]: class
	KindSecondTab        = "session.second_tab"    // another tab was refused this sitting
	KindFullscreenExited = "fullscreen.exited"     // tier 2
	KindFaceMissing      = "vision.face_missing"   // tier 2; values[0]: ms with no face
	KindFacesMultiple    = "vision.faces_multiple" // tier 2; values[0]: ms; values[1]: most faces seen
	KindVisionStopped    = "vision.stopped"        // tier 2; detail: one of the CameraStopped codes

	// Consent rows, written by the server at start from what the candidate
	// agreed to. One kind per purpose: consent.<purpose>.
	KindConsentPrefix = "consent."
	// The candidate taking a consent back mid-attempt, from the runner:
	// consent.<purpose>.withdrawn. Nothing is collected under the purpose
	// after it, and it is never given again within the attempt.
	KindWithdrawnSuffix = ".withdrawn"
)

// Purposes a candidate consents to, each separately (UU PDP No. 27/2022).
const (
	PurposeSignals    = "signals"    // focus, paste, timing, device, second tab
	PurposeFullscreen = "fullscreen" // full-screen lock
	PurposeCamera     = "camera"     // on-device face presence; no video leaves the device
)

// RequiredConsents is what a tier needs before an attempt can start. Tier 0
// collects nothing beyond the answers, so it asks for nothing.
func RequiredConsents(tier int32) []string {
	switch {
	case tier <= 0:
		return nil
	case tier == 1:
		return []string{PurposeSignals}
	default:
		return []string{PurposeSignals, PurposeFullscreen, PurposeCamera}
	}
}

// Purposes lists every purpose, with how a reason names it.
var Purposes = map[string][2]string{
	PurposeSignals:    {"the activity signals (focus, paste, timing, device)", "sinyal aktivitas (fokus, tempel, waktu, perangkat)"},
	PurposeFullscreen: {"the full-screen lock", "kunci layar penuh"},
	PurposeCamera:     {"the camera", "kamera"},
}

// WithdrawalOf is the purpose a withdrawal event takes back, or "" when the
// kind is not one.
func WithdrawalOf(kind string) string {
	p, ok := strings.CutPrefix(kind, KindConsentPrefix)
	if !ok {
		return ""
	}
	if p, ok = strings.CutSuffix(p, KindWithdrawnSuffix); !ok {
		return ""
	}
	if _, known := Purposes[p]; !known {
		return ""
	}
	return p
}

// Withdrawn lists the purposes withdrawn in a log, in the order they were.
func Withdrawn(events []*v1.AttemptEvent) []string {
	var out []string
	for _, ev := range events {
		if p := WithdrawalOf(ev.GetKind()); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// PurposeOf is the consent an event kind is collected under; "" for answers
// and other events that need none. A withdrawal is under the purpose it
// takes back.
func PurposeOf(kind string) string {
	if p := WithdrawalOf(kind); p != "" {
		return p
	}
	switch kind {
	case KindFocusLost, KindFocusReturned, KindPasted, KindItemViewed, KindDeviceSeen, KindSecondTab:
		return PurposeSignals
	case KindFullscreenExited:
		return PurposeFullscreen
	case KindFaceMissing, KindFacesMultiple, KindVisionStopped:
		return PurposeCamera
	}
	return ""
}

func init() {
	for _, p := range []string{PurposeSignals, PurposeFullscreen, PurposeCamera} {
		ServerOnly[KindConsentPrefix+p] = true
	}
}

// Why the camera stopped, as the runner reports it. The reason a candidate
// reads is the server's sentence for the code, never the client's own words,
// so it is always in their language and a modified client cannot write into it.
var CameraStopped = map[string][2]string{
	"permission_refused": {"camera permission was refused", "izin kamera ditolak"},
	"no_camera":          {"no camera could be opened", "kamera tidak dapat dibuka"},
	"camera_ended":       {"the camera was turned off or disconnected", "kamera dimatikan atau terputus"},
	"check_failed":       {"the face check failed on this device", "pemeriksaan wajah gagal di perangkat ini"},
	"check_unavailable":  {"the face check could not load on this device", "pemeriksaan wajah tidak dapat dimuat di perangkat ini"},
}

var cameraStoppedUnknown = [2]string{"the browser gave no recognised reason", "peramban tidak memberikan alasan yang dikenali"}

// Two reports of the same stretch: the runner reports a long stretch in
// pieces, each starting where the last one ended (give or take this).
const stretchJoin = 2 * time.Second

// Flag severities, from "worth knowing" to "do not certify without a look".
const (
	SeverityInfo    = "info"
	SeverityReview  = "review"
	SeveritySerious = "serious"
)

// Flag is one reason a reviewer should look at an attempt. Every flag is
// derived deterministically from the log, so the same log always gives the
// same flags, and its reason says exactly what was observed, in words the
// candidate can check and contest. Reason is for staff; ReasonID is the same
// sentence for the candidate, in Indonesian.
type Flag struct {
	Type     string
	Severity string
	Reason   string
	ReasonID string
	ItemID   string
}

// Thresholds. Below these a signal is ordinary life (a notification, a
// glance away) and is kept in the log without raising a flag.
const (
	FocusAwayCount     = 3
	FocusAwayTotal     = 60 * time.Second
	PasteChars         = 50
	RapidAnswer        = 3 * time.Second
	RapidMinItems      = 5
	RapidShare         = 0.5
	FaceMissingTotal   = 30 * time.Second
	FaceMissingStretch = 15 * time.Second
	FacesMultipleTotal = 5 * time.Second
)

// Flag types. Each must produce a reason; flags_test checks every one.
const (
	FlagRecordAltered = "record_altered"
	FlagFocusAway     = "focus_away"
	FlagPaste         = "paste"
	FlagRapidAnswers  = "rapid_answers"
	FlagDeviceChanged = "device_changed"
	FlagSecondTab     = "second_tab"
	FlagFullscreen    = "fullscreen_exit"
	FlagFaceMissing   = "face_missing"
	FlagFacesMultiple = "multiple_faces"
	FlagCameraStopped = "camera_stopped"
	FlagWithdrawn     = "consent_withdrawn"
)

// FlagTypes lists every type, in the order a reviewer reads them.
var FlagTypes = []string{FlagRecordAltered, FlagFacesMultiple, FlagFaceMissing, FlagCameraStopped, FlagWithdrawn, FlagPaste,
	FlagDeviceChanged, FlagFocusAway, FlagFullscreen, FlagRapidAnswers, FlagSecondTab}

func ms(ev *v1.AttemptEvent, i int) time.Duration {
	if i >= len(ev.GetValues()) {
		return 0
	}
	n, err := strconv.ParseInt(ev.GetValues()[i], 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return time.Duration(n) * time.Millisecond
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

func num(ev *v1.AttemptEvent, i int) int64 {
	if i >= len(ev.GetValues()) {
		return 0
	}
	n, err := strconv.ParseInt(ev.GetValues()[i], 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// span says a duration the way a person would: "2 min 5 s", "40 s".
func span(d time.Duration) (en, id string) {
	s := int(d.Round(time.Second) / time.Second)
	if s < 60 {
		return fmt.Sprintf("%d s", s), fmt.Sprintf("%d detik", s)
	}
	m, s := s/60, s%60
	if s == 0 {
		return fmt.Sprintf("%d min", m), fmt.Sprintf("%d menit", m)
	}
	return fmt.Sprintf("%d min %d s", m, s), fmt.Sprintf("%d menit %d detik", m, s)
}

func times(n int) (en, id string) {
	if n == 1 {
		return "1 time", "1 kali"
	}
	return fmt.Sprintf("%d times", n), fmt.Sprintf("%d kali", n)
}

// Flags evaluates the rules over an attempt's log. numberOf gives a
// question's position as the candidate saw it ("question 4"); intact and
// brokenAt come from the chain check.
func Flags(events []*v1.AttemptEvent, numberOf func(itemID string) int, intact bool, brokenAt int64) []Flag {
	var out []Flag
	if !intact {
		out = append(out, Flag{Type: FlagRecordAltered, Severity: SeveritySerious,
			Reason:   fmt.Sprintf("The stored record no longer matches its seal from event %d onward: it was changed, or events were removed, after it was written. Nothing from that point can be relied on.", brokenAt),
			ReasonID: fmt.Sprintf("Catatan yang tersimpan tidak lagi cocok dengan segelnya mulai peristiwa ke-%d: catatan itu diubah, atau ada peristiwa yang dihapus, setelah ditulis. Isi sejak titik itu tidak dapat diandalkan.", brokenAt)})
	}

	var (
		awayN, awayOpen, noReturn int
		away, awayLongest         time.Duration
		pasted                    = map[string]int64{}
		viewed                    = map[string]time.Duration{}
		devices                   = map[string]bool{}
		secondTab, fullscreen     int
		missing, missingLongest   time.Duration
		stretch                   time.Duration // the face-missing stretch being reported
		stretchEnd                int64         // when its last piece was reported
		multiple                  time.Duration
		mostFaces                 int64
		stopped                   []string
		started                   int64
		withdrawn                 []*v1.AttemptEvent
		answered                  = map[string]bool{}
		order                     []string
	)
	for _, ev := range events {
		if WithdrawalOf(ev.GetKind()) != "" {
			withdrawn = append(withdrawn, ev)
			continue
		}
		switch ev.GetKind() {
		case KindAttemptStarted:
			started = ev.GetServerTime()
		case KindFocusLost:
			awayN++
			if awayOpen > 0 {
				noReturn++ // left again with no return in between
			}
			awayOpen = 1
		case KindFocusReturned:
			awayOpen = 0
			d := ms(ev, 0)
			away += d
			if d > awayLongest {
				awayLongest = d
			}
		case KindPasted:
			if _, ok := pasted[ev.GetItemId()]; !ok {
				order = append(order, ev.GetItemId())
			}
			pasted[ev.GetItemId()] += num(ev, 0)
		case KindItemViewed:
			viewed[ev.GetItemId()] += ms(ev, 0)
		case KindDeviceSeen:
			if len(ev.GetValues()) > 0 && ev.GetValues()[0] != "" {
				devices[ev.GetValues()[0]] = true
			}
		case KindSecondTab:
			secondTab++
		case KindFullscreenExited:
			fullscreen++
		case KindFaceMissing:
			d := ms(ev, 0)
			missing += d
			at := ev.GetClientTime()
			if stretchEnd != 0 && at != 0 && abs(at-d.Milliseconds()-stretchEnd) <= stretchJoin.Milliseconds() {
				stretch += d
			} else {
				stretch = d
			}
			stretchEnd = at
			if stretch > missingLongest {
				missingLongest = stretch
			}
		case KindFacesMultiple:
			multiple += ms(ev, 0)
			if n := num(ev, 1); n > mostFaces {
				mostFaces = n
			}
		case KindVisionStopped:
			stopped = append(stopped, strings.TrimSpace(ev.GetDetail()))
		case KindResponseChanged, KindResponseTyped:
			if ev.GetItemId() != "" {
				answered[ev.GetItemId()] = true
			}
		}
	}

	if multiple >= FacesMultipleTotal {
		en, id := span(multiple)
		out = append(out, Flag{Type: FlagFacesMultiple, Severity: SeveritySerious,
			Reason:   fmt.Sprintf("More than one face was in view of the camera for %s in total (up to %d at once).", en, max(mostFaces, 2)),
			ReasonID: fmt.Sprintf("Lebih dari satu wajah terlihat di kamera selama total %s (paling banyak %d sekaligus).", id, max(mostFaces, 2))})
	}
	if missing >= FaceMissingTotal || missingLongest >= FaceMissingStretch {
		en, id := span(missing)
		len_, lid := span(missingLongest)
		out = append(out, Flag{Type: FlagFaceMissing, Severity: SeverityReview,
			Reason:   fmt.Sprintf("No face was in view of the camera for %s in total; the longest stretch was %s.", en, len_),
			ReasonID: fmt.Sprintf("Tidak ada wajah yang terlihat di kamera selama total %s; yang terlama %s.", id, lid)})
	}
	if len(stopped) > 0 {
		en, id := times(len(stopped))
		why, ok := CameraStopped[stopped[len(stopped)-1]]
		if !ok {
			why = cameraStoppedUnknown
		}
		out = append(out, Flag{Type: FlagCameraStopped, Severity: SeverityReview,
			Reason:   fmt.Sprintf("The camera stopped during the exam %s (last: %s). Face presence was not checked while it was off.", en, why[0]),
			ReasonID: fmt.Sprintf("Kamera berhenti selama ujian %s (terakhir: %s). Kehadiran wajah tidak diperiksa selama kamera mati.", id, why[1])})
	}
	// Taking a consent back is the candidate's right, so it is said as what
	// it is, not as something going wrong; what it means for the result is a
	// person's call, so it is for review.
	for _, ev := range withdrawn {
		what := Purposes[WithdrawalOf(ev.GetKind())]
		when, whenID := "during the exam", "selama ujian"
		if at := ev.GetServerTime(); started > 0 && at >= started {
			en, id := span(time.Duration(at-started) * time.Millisecond)
			when, whenID = en+" into the exam", id+" setelah ujian dimulai"
		}
		out = append(out, Flag{Type: FlagWithdrawn, Severity: SeverityReview,
			Reason:   fmt.Sprintf("The candidate withdrew their consent to %s %s, as they may; nothing under it was collected after that.", what[0], when),
			ReasonID: fmt.Sprintf("Peserta menarik persetujuan untuk %s %s, sebagaimana haknya; sejak itu tidak ada yang dikumpulkan untuk keperluan tersebut.", what[1], whenID)})
	}
	for _, item := range order {
		if n := pasted[item]; n >= PasteChars {
			where, whereID := "a question not in this version of the exam", "soal yang tidak ada di versi ujian ini"
			if q := numberOf(item); q > 0 {
				where, whereID = fmt.Sprintf("question %d", q), fmt.Sprintf("soal nomor %d", q)
			}
			out = append(out, Flag{Type: FlagPaste, Severity: SeverityReview, ItemID: item,
				Reason:   fmt.Sprintf("%d characters were pasted into the answer to %s rather than typed.", n, where),
				ReasonID: fmt.Sprintf("%d karakter ditempelkan ke jawaban %s, bukan diketik.", n, whereID)})
		}
	}
	if len(devices) > 1 {
		out = append(out, Flag{Type: FlagDeviceChanged, Severity: SeverityReview,
			Reason:   fmt.Sprintf("The attempt was worked on from %d different devices.", len(devices)),
			ReasonID: fmt.Sprintf("Pengerjaan ini dilakukan dari %d perangkat yang berbeda.", len(devices))})
	}
	if awayN >= FocusAwayCount || away >= FocusAwayTotal {
		en, id := times(awayN)
		ten, tid := span(away)
		len_, lid := span(awayLongest)
		reason := fmt.Sprintf("Left the exam %s, for %s in total; the longest was %s.", en, ten, len_)
		reasonID := fmt.Sprintf("Meninggalkan halaman ujian %s, total %s; yang terlama %s.", id, tid, lid)
		// A tab killed while away never reports coming back: say so, rather
		// than letting a missing return read as "0 s away".
		if noReturn += awayOpen; noReturn > 0 {
			reason += fmt.Sprintf(" For %d of these no return to the exam was recorded, so the time away is not counted.", noReturn)
			reasonID += fmt.Sprintf(" Untuk %d di antaranya tidak tercatat kembali ke halaman ujian, jadi lamanya tidak dihitung.", noReturn)
		}
		out = append(out, Flag{Type: FlagFocusAway, Severity: SeverityReview, Reason: reason, ReasonID: reasonID})
	}
	if fullscreen > 0 {
		en, id := times(fullscreen)
		out = append(out, Flag{Type: FlagFullscreen, Severity: SeverityReview,
			Reason:   fmt.Sprintf("Left full screen %s.", en),
			ReasonID: fmt.Sprintf("Keluar dari layar penuh %s.", id)})
	}
	if len(answered) >= RapidMinItems {
		fast := 0
		for item := range answered {
			if d, ok := viewed[item]; ok && d < RapidAnswer {
				fast++
			}
		}
		if float64(fast) >= RapidShare*float64(len(answered)) {
			out = append(out, Flag{Type: FlagRapidAnswers, Severity: SeverityReview,
				Reason:   fmt.Sprintf("%d of %d answered questions were answered in under %d seconds each.", fast, len(answered), int(RapidAnswer/time.Second)),
				ReasonID: fmt.Sprintf("%d dari %d soal yang dijawab, dijawab dalam waktu kurang dari %d detik per soal.", fast, len(answered), int(RapidAnswer/time.Second))})
		}
	}
	if secondTab > 0 {
		en, id := times(secondTab)
		out = append(out, Flag{Type: FlagSecondTab, Severity: SeverityInfo,
			Reason:   fmt.Sprintf("A second browser tab tried to open this sitting %s and was refused; answers came from one tab only.", en),
			ReasonID: fmt.Sprintf("Tab peramban kedua mencoba membuka pengerjaan ini %s dan ditolak; jawaban hanya berasal dari satu tab.", id)})
	}

	rank := map[string]int{}
	for i, t := range FlagTypes {
		rank[t] = i
	}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].Type] < rank[out[j].Type] })
	return out
}
