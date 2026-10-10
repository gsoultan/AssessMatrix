package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	img.Set(1, 1, color.RGBA{R: 200, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *client) upload(t *testing.T, body []byte) (int, string) {
	t.Helper()
	res, err := c.http.Post(c.base+"/media", "application/octet-stream", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out struct{ URL string }
	if res.StatusCode == http.StatusCreated {
		if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode, out.URL
}

func (c *client) fetch(t *testing.T, path string) (int, string, []byte) {
	t.Helper()
	res, err := c.http.Get(c.base + path)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, res.Header.Get("Content-Type"), b
}

func TestMediaIsStoredAndReadOnlyWithinItsTenant(t *testing.T) {
	h := newHarness(t)
	a, b := h.tenant("MediaA"), h.tenant("MediaB")
	img := pngBytes(t)

	st, url := a.upload(t, img)
	if st != http.StatusCreated || url == "" {
		t.Fatalf("upload: status %d url %q", st, url)
	}
	st, ctype, got := a.fetch(t, url)
	if st != http.StatusOK || ctype != "image/png" || !bytes.Equal(got, img) {
		t.Fatalf("own tenant read: status %d type %q, %d bytes", st, ctype, len(got))
	}
	if st, _, _ := b.fetch(t, url); st != http.StatusNotFound {
		t.Fatalf("another tenant read an image: status %d", st)
	}
	if st, _, _ := h.browser().fetch(t, url); st != http.StatusUnauthorized {
		t.Fatalf("an anonymous browser read an image: status %d", st)
	}
	if st, _ := h.browser().upload(t, img); st != http.StatusUnauthorized {
		t.Fatalf("an anonymous browser uploaded: status %d", st)
	}
}

func TestMediaRefusesWhatIsNotAnImage(t *testing.T) {
	h := newHarness(t)
	a := h.tenant("MediaKind")
	if st, _ := a.upload(t, []byte("<svg onload=alert(1)></svg>")); st != http.StatusUnsupportedMediaType {
		t.Fatalf("an SVG was accepted: status %d", st)
	}
	if st, _ := a.upload(t, nil); st != http.StatusUnsupportedMediaType {
		t.Fatalf("an empty body was accepted: status %d", st)
	}
}

func TestMediaStaysInItsRegion(t *testing.T) {
	h := newHarness(t)
	a := h.tenant("MediaRegion")
	if _, err := h.owner.Exec(context.Background(), `UPDATE tenants SET region = 'sg' WHERE id = $1`, h.lastTenant); err != nil {
		t.Fatal(err)
	}
	if st, _ := a.upload(t, pngBytes(t)); st != http.StatusConflict {
		t.Fatalf("a tenant from another region stored an image here: status %d", st)
	}
}

func TestCandidateReadsTheirTenantsImagesAndAttachesTheirOwn(t *testing.T) {
	h := newHarness(t)
	staff := h.tenant("MediaCand")
	_, figure := staff.upload(t, pngBytes(t))
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	if st, _, _ := cand.fetch(t, figure); st != http.StatusOK {
		t.Fatalf("candidate could not read their exam's image: status %d", st)
	}
	st, mine := cand.upload(t, pngBytes(t))
	if st != http.StatusCreated {
		t.Fatalf("candidate upload: status %d", st)
	}
	ctx := context.Background()
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "m1",
		Events: []*v1.AttemptEvent{{Kind: "answer.attached", ItemId: "q4", File: &v1.Media{Id: "m", Url: mine}}}})))
	_, err := cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "m2",
		Events: []*v1.AttemptEvent{{Kind: "answer.attached", ItemId: "q4", File: &v1.Media{Id: "m", Url: "https://example.com/x.png"}}}}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a candidate attached an outside URL: %v", err)
	}
}

func TestExamAcceptsUploadedImagesOnly(t *testing.T) {
	h := newHarness(t)
	staff := h.tenant("MediaExam")
	_, url := staff.upload(t, pngBytes(t))
	ctx := context.Background()
	e := get(staff.exams.CreateExam(ctx, connect.NewRequest(&v1.CreateExamRequest{Kind: v1.Kind_KIND_EXAM}))).Msg.GetExam()
	e.Sections[0].Items[0].Image = &v1.Media{Id: "f", Url: url, Alt: "a figure"}
	e = get(staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_DRAFT}))).Msg.GetExam()
	e.Sections[0].Items[0].Image = &v1.Media{Id: "f", Url: "/media/../../etc/passwd", Alt: "x"}
	_, err := staff.exams.SaveExam(ctx, connect.NewRequest(&v1.SaveExamRequest{Exam: e, Mode: v1.SaveMode_SAVE_MODE_DRAFT}))
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("a malformed media path was accepted: %v", err)
	}
}
