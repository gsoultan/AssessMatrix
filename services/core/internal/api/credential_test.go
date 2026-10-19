package api

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"strings"
	"testing"

	"connectrpc.com/connect"

	v1 "github.com/gsoultan/assessmatrix/services/core/gen/assessmatrix/v1"
)

func fetchJSON(t *testing.T, url string, into any) int {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		if err := json.NewDecoder(res.Body).Decode(into); err != nil {
			t.Fatal(err)
		}
	}
	return res.StatusCode
}

// A passing result with nothing left to score earns an Open Badges 3.0
// credential that anyone can verify with the issuer's public key, names its
// recipient only as a salted email hash, and is revoked if a later review
// takes the result below the bar.
func TestPassingEarnsAVerifiableCredential(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	staff := h.tenant("Badges")
	f := h.publishedFixture(staff)
	cand := h.redeem(f)
	att := get(cand.candidate.StartAttempt(ctx, connect.NewRequest(&v1.StartAttemptRequest{Consents: allConsents}))).Msg.GetAttempt()
	get(cand.candidate.AppendEvents(ctx, connect.NewRequest(&v1.AppendEventsRequest{AttemptId: att.GetId(), BatchId: "b",
		Events: []*v1.AttemptEvent{
			{Kind: "response.changed", ItemId: "q1", Values: []string{"b"}},
			{Kind: "response.typed", ItemId: "q4", Text: "An essay."},
		}})))
	sub := get(cand.candidate.SubmitAttempt(ctx, connect.NewRequest(&v1.SubmitAttemptRequest{AttemptId: att.GetId()}))).Msg
	if sub.GetCredentialUrl() != "" {
		t.Fatal("no credential while an essay still waits for a person")
	}

	rev := get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 5}))).Msg
	if !rev.GetScore().GetPassed() || !strings.HasPrefix(rev.GetCredentialUrl(), "http://exam.test/verify/#") {
		t.Fatalf("the review that completes a pass must issue the credential, got %+v", rev)
	}
	id := strings.TrimPrefix(rev.GetCredentialUrl(), "http://exam.test/verify/#")
	if mail := h.mail(f.email, "credential"); !strings.Contains(mail, rev.GetCredentialUrl()) {
		t.Fatalf("the candidate must be mailed the link, got %q", mail)
	}

	var pub struct {
		JWT     string `json:"jwt"`
		Revoked bool   `json:"revoked"`
	}
	if code := fetchJSON(t, h.http.URL+"/credentials/"+id, &pub); code != 200 || pub.Revoked {
		t.Fatalf("public credential: %d revoked=%v", code, pub.Revoked)
	}
	parts := strings.Split(pub.JWT, ".")
	if len(parts) != 3 {
		t.Fatalf("not a compact JWS: %q", pub.JWT)
	}
	dec := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var head struct{ Alg, Kid string }
	var vc map[string]any
	_ = json.Unmarshal(dec(parts[0]), &head)
	_ = json.Unmarshal(dec(parts[1]), &vc)
	if head.Alg != "RS256" || !strings.Contains(fmtJSON(vc["type"]), "OpenBadgeCredential") || vc["jti"] != "urn:uuid:"+id {
		t.Fatalf("header %+v, payload %v", head, vc)
	}
	iss, _ := vc["iss"].(string)
	issuerPath := strings.TrimPrefix(iss, "http://exam.test")
	var jwks struct {
		Keys []struct{ Kid, N, E string } `json:"keys"`
	}
	if code := fetchJSON(t, h.http.URL+issuerPath+"/jwks", &jwks); code != 200 || len(jwks.Keys) != 1 || jwks.Keys[0].Kid != head.Kid {
		t.Fatalf("issuer keys: %d %+v", code, jwks)
	}
	key := &rsa.PublicKey{N: new(big.Int).SetBytes(dec(jwks.Keys[0].N)), E: int(new(big.Int).SetBytes(dec(jwks.Keys[0].E)).Int64())}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], dec(parts[2])); err != nil {
		t.Fatalf("the signature must verify with the issuer's published key: %v", err)
	}
	if strings.Contains(string(dec(parts[1])), f.email) {
		t.Fatal("the public credential must not carry the email in the clear")
	}
	ident := vc["credentialSubject"].(map[string]any)["identifier"].([]any)[0].(map[string]any)
	want := sha256.Sum256([]byte(strings.ToLower(f.email) + ident["salt"].(string)))
	if ident["identityHash"] != "sha256$"+hex.EncodeToString(want[:]) {
		t.Fatalf("the recipient hash must match their email and salt, got %v", ident)
	}
	var profile map[string]any
	if code := fetchJSON(t, h.http.URL+issuerPath, &profile); code != 200 || profile["name"] != "Badges" {
		t.Fatalf("issuer profile: %d %v", code, profile)
	}

	// Any verifier holding only the signed credential can check revocation.
	status, _ := vc["credentialStatus"].(map[string]any)
	if status["type"] != "1EdTechRevocationList" || status["id"] != "http://exam.test/credentials/"+id+"/status" {
		t.Fatalf("credentialStatus = %v", vc["credentialStatus"])
	}
	type revocations struct {
		Issuer  string `json:"issuer"`
		Revoked []struct {
			ID     string `json:"id"`
			Reason string `json:"revocationReason"`
		} `json:"revokedCredentials"`
	}
	var list revocations
	if code := fetchJSON(t, h.http.URL+"/credentials/"+id+"/status", &list); code != 200 || len(list.Revoked) != 0 || list.Issuer != iss {
		t.Fatalf("status of a standing credential: %d %+v", code, list)
	}

	// Coming back to the link later, the candidate sees the review and the credential.
	back := get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg
	if back.GetCredentialUrl() != rev.GetCredentialUrl() {
		t.Fatalf("credential on return = %q, want %q", back.GetCredentialUrl(), rev.GetCredentialUrl())
	}
	if rs := back.GetAttempt().GetReviews(); len(rs) != 1 || rs[0].GetItemId() != "q4" || rs[0].GetPoints() != 5 || rs[0].GetMaxPoints() != 5 {
		t.Fatalf("reviews on return = %+v", rs)
	}

	// A second look marks the essay down: the result no longer passes.
	get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 0, Note: "Tidak menjawab pertanyaan."})))
	if fetchJSON(t, h.http.URL+"/credentials/"+id, &pub); !pub.Revoked {
		t.Fatal("a credential whose result no longer passes must read as revoked")
	}
	if mail := h.mail(f.email, "result"); !strings.Contains(mail, "Skor akhir Anda") || strings.Contains(mail, "Hasil: lulus") {
		t.Fatalf("the candidate must be told the result that took the credential back, got %q", mail)
	}
	list = revocations{}
	if fetchJSON(t, h.http.URL+"/credentials/"+id+"/status", &list); len(list.Revoked) != 1 || list.Revoked[0].ID != "urn:uuid:"+id || list.Revoked[0].Reason == "" {
		t.Fatalf("the revocation list must name it, got %+v", list)
	}
	back = get(cand.candidate.GetSession(ctx, connect.NewRequest(&v1.GetSessionRequest{}))).Msg
	if back.GetCredentialUrl() != "" || back.GetAttempt().GetReviews()[0].GetNote() != "Tidak menjawab pertanyaan." {
		t.Fatalf("after the re-review: credential %q, reviews %+v", back.GetCredentialUrl(), back.GetAttempt().GetReviews())
	}
	again := get(staff.results.ScoreAnswer(ctx, connect.NewRequest(&v1.ScoreAnswerRequest{AttemptId: att.GetId(), ItemId: "q4", Points: 5}))).Msg
	if fetchJSON(t, h.http.URL+"/credentials/"+id, &pub); pub.Revoked || again.GetCredentialUrl() != rev.GetCredentialUrl() {
		t.Fatal("passing again restores the same credential")
	}

	if code := fetchJSON(t, h.http.URL+"/credentials/00000000-0000-0000-0000-000000000000", &pub); code != 404 {
		t.Fatalf("an unknown credential is not found, got %d", code)
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
